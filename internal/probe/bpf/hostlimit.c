// SPDX-License-Identifier: GPL-2.0
// Host/Mixed application classifier and shared download policer.

#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <linux/in.h>
#include <linux/ip.h>
#include <linux/ipv6.h>
#include <linux/pkt_cls.h>
#include <bpf/bpf_endian.h>
#include <bpf/bpf_helpers.h>

struct app_config {
	__u64 generation;
	__u64 download_bytes_per_second;
	__u64 download_burst_bytes;
	__u32 upload_mark;
	__u32 mark_mask;
};

struct download_state {
	struct bpf_spin_lock lock;
	__u32 reserved;
	__u64 generation;
	__u64 token_nanobytes;
	__u64 last_refill_ns;
};

struct flow_key {
	__u8 local_address[16];
	__u8 remote_address[16];
	__be16 local_port;
	__be16 remote_port;
	__u8 family;
	__u8 protocol;
	__u8 padding[2];
};

struct parsed_packet {
	__u8 source_address[16];
	__u8 destination_address[16];
	__be16 source_port;
	__be16 destination_port;
	__u8 family;
	__u8 protocol;
};

struct ipv6_option_header {
	__u8 next_header;
	__u8 length;
};

struct ipv6_fragment_header {
	__u8 next_header;
	__u8 reserved;
	__be16 fragment_offset;
	__be32 identification;
};

struct {
	__uint(type, BPF_MAP_TYPE_ARRAY);
	__uint(max_entries, 1);
	__type(key, __u32);
	__type(value, struct app_config);
} config SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_ARRAY);
	__uint(max_entries, 1);
	__type(key, __u32);
	__type(value, struct download_state);
} download_states SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_SK_STORAGE);
	__uint(map_flags, BPF_F_NO_PREALLOC);
	__type(key, int);
	__type(value, __u8);
} socket_tags SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 64);
	__type(key, __u32);
	__type(value, __u8);
} bridge_ifindexes SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_LRU_HASH);
	__uint(max_entries, 16384);
	__type(key, struct flow_key);
	__type(value, __u64);
} flows SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_ARRAY);
	__uint(max_entries, 1);
	__type(key, __u32);
	__type(value, __u64);
} local_upload_bytes SEC(".maps");

static __always_inline int ipv4_is_private(__be32 address)
{
	__u32 host = bpf_ntohl(address);

	if ((host & 0xff000000U) == 0x0a000000U)
		return 1;
	if ((host & 0xfff00000U) == 0xac100000U)
		return 1;
	if ((host & 0xffff0000U) == 0xc0a80000U)
		return 1;
	if ((host & 0xff000000U) == 0x7f000000U)
		return 1;
	if ((host & 0xffff0000U) == 0xa9fe0000U)
		return 1;
	return 0;
}

static __always_inline int ipv6_is_private(const __u8 address[16])
{
	int index;

	if ((address[0] & 0xfe) == 0xfc)
		return 1;
	if (address[0] == 0xfe && (address[1] & 0xc0) == 0x80)
		return 1;
	#pragma unroll
	for (index = 0; index < 15; index++) {
		if (address[index] != 0)
			return 0;
	}
	return address[15] == 1;
}

static __always_inline int parse_packet(struct __sk_buff *skb,
					struct parsed_packet *packet)
{
	struct {
		__be16 source;
		__be16 destination;
	} ports;
	__u32 transport_offset;

	if (skb->protocol == bpf_htons(ETH_P_IP)) {
		struct iphdr ip = {};
		__u16 fragment_offset;

		if (bpf_skb_load_bytes_relative(skb, 0, &ip, sizeof(ip),
						BPF_HDR_START_NET) < 0 ||
		    ip.version != 4 || ip.ihl < 5)
			return 0;
		transport_offset = ip.ihl * 4;
		__builtin_memcpy(packet->source_address, &ip.saddr, sizeof(ip.saddr));
		__builtin_memcpy(packet->destination_address, &ip.daddr, sizeof(ip.daddr));
		packet->family = 4;
		packet->protocol = ip.protocol;
		fragment_offset = bpf_ntohs(ip.frag_off) & 0x1fff;
		if ((ip.protocol == IPPROTO_TCP || ip.protocol == IPPROTO_UDP) &&
		    fragment_offset == 0 &&
		    bpf_skb_load_bytes_relative(skb, transport_offset, &ports,
						 sizeof(ports), BPF_HDR_START_NET) == 0) {
			packet->source_port = ports.source;
			packet->destination_port = ports.destination;
		}
		return 1;
	} else if (skb->protocol == bpf_htons(ETH_P_IPV6)) {
		struct ipv6hdr ip = {};
		__u8 next_header;
		int index;

		if (bpf_skb_load_bytes_relative(skb, 0, &ip, sizeof(ip),
						BPF_HDR_START_NET) < 0 || ip.version != 6)
			return 0;
		__builtin_memcpy(packet->source_address, &ip.saddr, sizeof(ip.saddr));
		__builtin_memcpy(packet->destination_address, &ip.daddr, sizeof(ip.daddr));
		packet->family = 6;
		transport_offset = sizeof(ip);
		next_header = ip.nexthdr;

		// IPv6 extension chains are bounded so the verifier can prove that this
		// parser terminates. Six headers cover normal Hop-by-Hop, Routing,
		// Destination, AH and Fragment combinations; longer chains still retain
		// address/protocol classification and therefore cannot bypass policing.
		#pragma unroll
		for (index = 0; index < 6; index++) {
			packet->protocol = next_header;
			if (next_header == IPPROTO_TCP || next_header == IPPROTO_UDP) {
				if (bpf_skb_load_bytes_relative(skb, transport_offset, &ports,
							 sizeof(ports), BPF_HDR_START_NET) == 0) {
					packet->source_port = ports.source;
					packet->destination_port = ports.destination;
				}
				return 1;
			}
			if (next_header == IPPROTO_HOPOPTS ||
			    next_header == IPPROTO_ROUTING ||
			    next_header == IPPROTO_DSTOPTS) {
				struct ipv6_option_header option = {};

				if (bpf_skb_load_bytes_relative(skb, transport_offset, &option,
							 sizeof(option), BPF_HDR_START_NET) < 0)
					return 1;
				next_header = option.next_header;
				transport_offset += ((__u32)option.length + 1) * 8;
				continue;
			}
			if (next_header == IPPROTO_FRAGMENT) {
				struct ipv6_fragment_header fragment = {};

				if (bpf_skb_load_bytes_relative(skb, transport_offset, &fragment,
							 sizeof(fragment), BPF_HDR_START_NET) < 0)
					return 1;
				next_header = fragment.next_header;
				packet->protocol = next_header;
				transport_offset += sizeof(fragment);
				// Non-initial fragments do not contain transport ports. A zero-port
				// flow key still links their egress and ingress directions.
				if ((bpf_ntohs(fragment.fragment_offset) & 0xfff8) != 0)
					return 1;
				continue;
			}
			if (next_header == IPPROTO_AH) {
				struct ipv6_option_header auth = {};

				if (bpf_skb_load_bytes_relative(skb, transport_offset, &auth,
							 sizeof(auth), BPF_HDR_START_NET) < 0)
					return 1;
				next_header = auth.next_header;
				transport_offset += ((__u32)auth.length + 2) * 4;
				continue;
			}
			return 1;
		}
		return 1;
	} else {
		return 0;
	}
}

static __always_inline int peer_is_private(const struct parsed_packet *packet,
					    int ingress)
{
	const __u8 *address = ingress ? packet->source_address :
					packet->destination_address;

	if (packet->family == 4) {
		__be32 ipv4;

		__builtin_memcpy(&ipv4, address, sizeof(ipv4));
		return ipv4_is_private(ipv4);
	}
	return ipv6_is_private(address);
}

static __always_inline void flow_from_packet(struct flow_key *flow,
					      const struct parsed_packet *packet,
					      int ingress)
{
	if (ingress) {
		__builtin_memcpy(flow->local_address, packet->destination_address, 16);
		__builtin_memcpy(flow->remote_address, packet->source_address, 16);
		flow->local_port = packet->destination_port;
		flow->remote_port = packet->source_port;
	} else {
		__builtin_memcpy(flow->local_address, packet->source_address, 16);
		__builtin_memcpy(flow->remote_address, packet->destination_address, 16);
		flow->local_port = packet->source_port;
		flow->remote_port = packet->destination_port;
	}
	flow->family = packet->family;
	flow->protocol = packet->protocol;
}

static __always_inline int socket_is_tagged(struct __sk_buff *skb,
					     const struct parsed_packet *packet)
{
	struct bpf_sock_tuple tuple = {};
	struct bpf_sock *socket;
	__u8 *tag;

	if ((packet->protocol != IPPROTO_TCP && packet->protocol != IPPROTO_UDP) ||
	    packet->source_port == 0 || packet->destination_port == 0)
		return 0;

	if (packet->family == 4) {
		__builtin_memcpy(&tuple.ipv4.saddr, packet->source_address, 4);
		__builtin_memcpy(&tuple.ipv4.daddr, packet->destination_address, 4);
		tuple.ipv4.sport = packet->source_port;
		tuple.ipv4.dport = packet->destination_port;
		if (packet->protocol == IPPROTO_TCP)
			socket = bpf_sk_lookup_tcp(skb, &tuple, sizeof(tuple.ipv4),
						   BPF_F_CURRENT_NETNS, 0);
		else
			socket = bpf_sk_lookup_udp(skb, &tuple, sizeof(tuple.ipv4),
						   BPF_F_CURRENT_NETNS, 0);
	} else {
		__builtin_memcpy(tuple.ipv6.saddr, packet->source_address, 16);
		__builtin_memcpy(tuple.ipv6.daddr, packet->destination_address, 16);
		tuple.ipv6.sport = packet->source_port;
		tuple.ipv6.dport = packet->destination_port;
		if (packet->protocol == IPPROTO_TCP)
			socket = bpf_sk_lookup_tcp(skb, &tuple, sizeof(tuple.ipv6),
						   BPF_F_CURRENT_NETNS, 0);
		else
			socket = bpf_sk_lookup_udp(skb, &tuple, sizeof(tuple.ipv6),
						   BPF_F_CURRENT_NETNS, 0);
	}
	if (!socket)
		return 0;
	tag = bpf_sk_storage_get(&socket_tags, socket, 0, 0);
	bpf_sk_release(socket);
	return tag != 0;
}

static __always_inline void account_local_upload(struct __sk_buff *skb)
{
	__u64 *bytes;
	__u32 key = 0;

	bytes = bpf_map_lookup_elem(&local_upload_bytes, &key);
	if (bytes)
		__sync_fetch_and_add(bytes, (__u64)skb->len);
}

static __always_inline int allow_download(struct __sk_buff *skb,
					   const struct app_config *policy)
{
	const __u64 scale = 1000000000ULL;
	struct download_state *state;
	__u64 burst_nanobytes;
	__u64 packet_nanobytes;
	__u64 elapsed;
	__u64 refill;
	__u64 now;
	__u32 key = 0;
	int allow = 1;

	if (policy->download_bytes_per_second == 0 ||
	    policy->download_burst_bytes == 0)
		return 1;
	state = bpf_map_lookup_elem(&download_states, &key);
	if (!state)
		return 1;
	now = bpf_ktime_get_ns();
	burst_nanobytes = policy->download_burst_bytes * scale;
	packet_nanobytes = (__u64)skb->len * scale;
	bpf_spin_lock(&state->lock);
	if (state->generation != policy->generation) {
		state->generation = policy->generation;
		state->token_nanobytes = burst_nanobytes;
		state->last_refill_ns = now;
	}
	if (now > state->last_refill_ns &&
	    state->token_nanobytes < burst_nanobytes) {
		elapsed = now - state->last_refill_ns;
		if (elapsed >= (burst_nanobytes - state->token_nanobytes) /
			       policy->download_bytes_per_second) {
			state->token_nanobytes = burst_nanobytes;
		} else {
			refill = elapsed * policy->download_bytes_per_second;
			if (refill >= burst_nanobytes - state->token_nanobytes)
				state->token_nanobytes = burst_nanobytes;
			else
				state->token_nanobytes += refill;
		}
		state->last_refill_ns = now;
	}
	if (packet_nanobytes <= state->token_nanobytes)
		state->token_nanobytes -= packet_nanobytes;
	else
		allow = 0;
	bpf_spin_unlock(&state->lock);
	return allow;
}

SEC("cgroup/sock_create")
int hostlimit_tag_socket(struct bpf_sock *socket)
{
	__u8 tag = 1;

	bpf_sk_storage_get(&socket_tags, socket, &tag,
			   BPF_SK_STORAGE_GET_F_CREATE);
	return 1;
}

SEC("tc")
int hostlimit_tc_ingress(struct __sk_buff *skb)
{
	struct parsed_packet packet = {};
	struct flow_key flow = {};
	struct app_config *policy;
	__u64 *generation;
	__u32 key = 0;
	int matched = 0;

	policy = bpf_map_lookup_elem(&config, &key);
	if (!policy || policy->generation == 0 || !parse_packet(skb, &packet) ||
	    peer_is_private(&packet, 1))
		return TC_ACT_UNSPEC;
	if (socket_is_tagged(skb, &packet)) {
		matched = 1;
	} else {
		flow_from_packet(&flow, &packet, 1);
		generation = bpf_map_lookup_elem(&flows, &flow);
		if (generation && *generation == policy->generation)
			matched = 1;
	}
	if (!matched)
		return TC_ACT_UNSPEC;
	return allow_download(skb, policy) ? TC_ACT_OK : TC_ACT_SHOT;
}

SEC("tc")
int hostlimit_tc_egress(struct __sk_buff *skb)
{
	struct parsed_packet packet = {};
	struct flow_key flow = {};
	struct app_config *policy;
	struct bpf_sock *socket;
	__u64 *generation;
	__u8 *bridge;
	__u8 *tag;
	__u32 ingress_ifindex;
	__u32 key = 0;
	int matched = 0;

	policy = bpf_map_lookup_elem(&config, &key);
	if (!policy || policy->generation == 0 || !parse_packet(skb, &packet))
		return TC_ACT_UNSPEC;
	ingress_ifindex = skb->ingress_ifindex;
	bridge = bpf_map_lookup_elem(&bridge_ifindexes, &ingress_ifindex);
	if (bridge) {
		matched = 1;
	} else {
		socket = skb->sk;
		if (socket)
			socket = bpf_sk_fullsock(socket);
		if (socket) {
			tag = bpf_sk_storage_get(&socket_tags, socket, 0, 0);
			if (tag)
				matched = 1;
		}
	}
	if (!matched) {
		flow_from_packet(&flow, &packet, 0);
		generation = bpf_map_lookup_elem(&flows, &flow);
		if (generation && *generation == policy->generation)
			matched = 1;
	}
	if (!matched)
		return TC_ACT_UNSPEC;
	if (peer_is_private(&packet, 0)) {
		account_local_upload(skb);
		return TC_ACT_UNSPEC;
	}
	flow_from_packet(&flow, &packet, 0);
	bpf_map_update_elem(&flows, &flow, &policy->generation, BPF_ANY);
	// Also retain an address/protocol wildcard for non-initial return
	// fragments, which do not carry transport ports even when the original
	// egress packet was not fragmented.
	if (flow.local_port != 0 || flow.remote_port != 0) {
		flow.local_port = 0;
		flow.remote_port = 0;
		bpf_map_update_elem(&flows, &flow, &policy->generation, BPF_ANY);
	}
	if (policy->upload_mark == 0 || policy->mark_mask == 0)
		return TC_ACT_OK;
	skb->mark = (skb->mark & ~policy->mark_mask) | policy->upload_mark;
	return TC_ACT_UNSPEC;
}

char LICENSE[] SEC("license") = "GPL";
