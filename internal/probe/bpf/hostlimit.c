// SPDX-License-Identifier: GPL-2.0
// Experimental cgroup_skb application meter used to validate Host-network
// policing on Lazycat. Userspace keeps this fail-open until every attachment
// has been staged successfully.

#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <bpf/bpf_endian.h>
#include <bpf/bpf_helpers.h>

struct limit_policy {
	__u64 generation;
	__u64 upload_bytes_per_second;
	__u64 upload_burst_bytes;
	__u64 download_bytes_per_second;
	__u64 download_burst_bytes;
	__u32 flags;
	__u32 reserved;
};

#define LIMIT_POLICY_BYPASS_PRIVATE (1U << 0)

struct limit_state {
	struct bpf_spin_lock lock;
	__u32 reserved;
	__u64 generation;
	// One byte is represented by 1e9 units. This preserves sub-byte refill
	// time between small packets instead of rounding it away on every hook.
	__u64 token_nanobytes;
	__u64 last_refill_ns;
	__u64 passed_bytes;
	__u64 passed_packets;
	__u64 dropped_bytes;
	__u64 dropped_packets;
};

struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 1);
	__type(key, __u64);
	__type(value, struct limit_policy);
} policies SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 1);
	__type(key, __u64);
	__type(value, struct limit_state);
} upload_states SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 1);
	__type(key, __u64);
	__type(value, struct limit_state);
} download_states SEC(".maps");

static __always_inline int ipv4_is_private(__u32 address)
{
	address = bpf_ntohl(address);
	if ((address & 0xff000000U) == 0x0a000000U)
		return 1;
	if ((address & 0xfff00000U) == 0xac100000U)
		return 1;
	if ((address & 0xffff0000U) == 0xc0a80000U)
		return 1;
	if ((address & 0xff000000U) == 0x7f000000U)
		return 1;
	if ((address & 0xffff0000U) == 0xa9fe0000U)
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

static __always_inline int is_private_peer(struct __sk_buff *skb, int ingress)
{
	if (skb->protocol == bpf_htons(ETH_P_IP)) {
		__u32 address = 0;
		__u32 offset = ingress ? 12 : 16;
		if (bpf_skb_load_bytes_relative(skb, offset, &address,
						sizeof(address), BPF_HDR_START_NET) < 0)
			return 0;
		return ipv4_is_private(address);
	}
	if (skb->protocol == bpf_htons(ETH_P_IPV6)) {
		__u8 address[16] = {};
		__u32 offset = ingress ? 8 : 24;
		if (bpf_skb_load_bytes_relative(skb, offset, address,
						sizeof(address), BPF_HDR_START_NET) < 0)
			return 0;
		return ipv6_is_private(address);
	}
	// Only an explicitly decoded private IP peer bypasses the public meter.
	// Host-network skbs can occasionally carry an unset protocol field; treating
	// that as local would create a topology-dependent public traffic bypass.
	return 0;
}

static __always_inline int police_packet(struct __sk_buff *skb, int ingress)
{
	// Each userspace prototype owns its maps and program instances. Every path
	// attached to this instance therefore belongs to one application and uses
	// the same fixed key. This avoids relying on skb socket-cgroup metadata,
	// which is incomplete on some Host-network ingress paths.
	__u64 app_key = 1;
	struct limit_policy *policy;
	struct limit_state *state;
	struct limit_state initial = {};
	void *states;
	__u64 rate;
	__u64 burst;
	__u64 now;
	__u64 elapsed;
	__u64 burst_nanobytes;
	__u64 refill_nanobytes;
	__u64 packet_nanobytes;
	__u64 packet_bytes = skb->len;
	int allow = 1;

	policy = bpf_map_lookup_elem(&policies, &app_key);
	if (!policy)
		return 1;
	if ((policy->flags & LIMIT_POLICY_BYPASS_PRIVATE) && is_private_peer(skb, ingress))
		return 1;

	if (ingress) {
		rate = policy->download_bytes_per_second;
		burst = policy->download_burst_bytes;
		states = &download_states;
	} else {
		rate = policy->upload_bytes_per_second;
		burst = policy->upload_burst_bytes;
		states = &upload_states;
	}
	if (rate == 0 || burst == 0)
		return 1;

	state = bpf_map_lookup_elem(states, &app_key);
	if (!state) {
		initial.generation = policy->generation;
		initial.token_nanobytes = burst * 1000000000ULL;
		initial.last_refill_ns = bpf_ktime_get_ns();
		bpf_map_update_elem(states, &app_key, &initial, BPF_NOEXIST);
		state = bpf_map_lookup_elem(states, &app_key);
		if (!state)
			return 1;
	}

	now = bpf_ktime_get_ns();
	burst_nanobytes = burst * 1000000000ULL;
	packet_nanobytes = packet_bytes * 1000000000ULL;
	bpf_spin_lock(&state->lock);
	if (state->generation != policy->generation) {
		state->generation = policy->generation;
		state->token_nanobytes = burst_nanobytes;
		state->last_refill_ns = now;
	}
	if (now > state->last_refill_ns && state->token_nanobytes < burst_nanobytes) {
		elapsed = now - state->last_refill_ns;
		if (elapsed >= (burst_nanobytes - state->token_nanobytes) / rate) {
			state->token_nanobytes = burst_nanobytes;
		} else {
			refill_nanobytes = elapsed * rate;
			if (refill_nanobytes >= burst_nanobytes - state->token_nanobytes)
				state->token_nanobytes = burst_nanobytes;
			else
				state->token_nanobytes += refill_nanobytes;
		}
		state->last_refill_ns = now;
	}
	if (packet_nanobytes <= state->token_nanobytes) {
		state->token_nanobytes -= packet_nanobytes;
		state->passed_bytes += packet_bytes;
		state->passed_packets++;
	} else {
		state->dropped_bytes += packet_bytes;
		state->dropped_packets++;
		allow = 0;
	}
	bpf_spin_unlock(&state->lock);
	return allow;
}

SEC("cgroup_skb/ingress")
int hostlimit_ingress(struct __sk_buff *skb)
{
	return police_packet(skb, 1);
}

SEC("cgroup_skb/egress")
int hostlimit_egress(struct __sk_buff *skb)
{
	return police_packet(skb, 0);
}

char LICENSE[] SEC("license") = "GPL";
