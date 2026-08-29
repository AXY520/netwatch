package probe

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/rlimit"
)

// hostLimitPrototype is deliberately not wired into AppNetworkController yet.
// It provides the smallest fail-open cgroup_skb policing unit needed to prove
// the Host-network kernel path before the product advertises that capability.
// One instance is attached to every cgroup path belonging to an application,
// so its Host and Bridge containers consume one upload and one download bucket.
type hostLimitPrototype struct {
	objects     hostlimitObjects
	attachments map[string][]link.Link
	active      bool
	closed      bool
}

type hostLimitPrototypeSnapshot struct {
	Upload   hostlimitLimitState
	Download hostlimitLimitState
}

const hostLimitPolicyBypassPrivate = uint32(1)
const hostLimitPrototypeKernelKey = uint64(1)

func newHostLimitPrototype() (*hostLimitPrototype, error) {
	_ = rlimit.RemoveMemlock()
	prototype := &hostLimitPrototype{
		attachments: make(map[string][]link.Link),
	}
	if err := loadHostlimitObjects(&prototype.objects, nil); err != nil {
		return nil, fmt.Errorf("load experimental Host limit BPF objects: %w", err)
	}
	return prototype, nil
}

func (p *hostLimitPrototype) stage(cgroupPaths []string, limit AppTrafficLimit, generation uint64, bypassPrivate bool) error {
	if p == nil {
		return errors.New("Host limit prototype is unavailable")
	}
	if p.closed {
		return errors.New("Host limit prototype is closed")
	}
	if p.active || len(p.attachments) != 0 {
		return errors.New("Host limit prototype already has a staged policy")
	}
	if generation == 0 {
		return errors.New("Host limit prototype generation must be non-zero")
	}
	paths := uniqueExistingCgroupPaths(cgroupPaths)
	if len(paths) == 0 {
		return errors.New("Host limit prototype has no existing cgroup path")
	}
	policy, err := hostLimitPolicy(limit, generation)
	if err != nil {
		return err
	}
	if bypassPrivate {
		policy.Flags = hostLimitPolicyBypassPrivate
	}

	// Stage attachments while no policy exists. Missing policy means both BPF
	// programs return allow, so every partial failure is fail-open.
	for _, path := range paths {
		ingress, attachErr := link.AttachCgroup(link.CgroupOptions{
			Path: path, Attach: ebpf.AttachCGroupInetIngress, Program: p.objects.HostlimitIngress,
		})
		if attachErr != nil {
			return errors.Join(fmt.Errorf("attach Host limit ingress to %s: %w", path, attachErr), p.close())
		}
		p.attachments[path] = append(p.attachments[path], ingress)
		egress, attachErr := link.AttachCgroup(link.CgroupOptions{
			Path: path, Attach: ebpf.AttachCGroupInetEgress, Program: p.objects.HostlimitEgress,
		})
		if attachErr != nil {
			return errors.Join(fmt.Errorf("attach Host limit egress to %s: %w", path, attachErr), p.close())
		}
		p.attachments[path] = append(p.attachments[path], egress)
	}

	// One map update atomically activates both directions after staging.
	if err := p.objects.Policies.Put(hostLimitPrototypeKernelKey, policy); err != nil {
		return errors.Join(fmt.Errorf("activate Host limit policy: %w", err), p.close())
	}
	p.active = true
	return nil
}

func hostLimitPolicy(limit AppTrafficLimit, generation uint64) (hostlimitLimitPolicy, error) {
	if limit.UploadKbps < 0 || limit.DownloadKbps < 0 || limit.UploadKbps > maxAppTrafficLimitKbps || limit.DownloadKbps > maxAppTrafficLimitKbps {
		return hostlimitLimitPolicy{}, fmt.Errorf("traffic limit must be between 0 and %d Kbit/s", maxAppTrafficLimitKbps)
	}
	return hostlimitLimitPolicy{
		Generation:             generation,
		UploadBytesPerSecond:   uint64(limit.UploadKbps * 1000 / 8),
		UploadBurstBytes:       limitDirectionBurst(limit.UploadKbps),
		DownloadBytesPerSecond: uint64(limit.DownloadKbps * 1000 / 8),
		DownloadBurstBytes:     limitDirectionBurst(limit.DownloadKbps),
	}, nil
}

func limitDirectionBurst(kbps int64) uint64 {
	if kbps <= 0 {
		return 0
	}
	burst := appTrafficBurstBytes(kbps)
	// cgroup_skb runs before GRO packets are necessarily segmented. A burst
	// smaller than one aggregated skb can never admit that skb and collapses a
	// TCP flow instead of merely policing it. Lazycat's current host reports a
	// 64 KiB GSO/GRO ceiling, so retain headroom for headers and future drivers.
	if burst < 128*1024 {
		burst = 128 * 1024
	}
	return uint64(burst)
}

func uniqueExistingCgroupPaths(paths []string) []string {
	unique := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.IsDir() {
			continue
		}
		unique[path] = struct{}{}
	}
	result := make([]string, 0, len(unique))
	for path := range unique {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func (p *hostLimitPrototype) snapshot() (hostLimitPrototypeSnapshot, error) {
	if p == nil || !p.active {
		return hostLimitPrototypeSnapshot{}, errors.New("Host limit prototype policy is not active")
	}
	var snapshot hostLimitPrototypeSnapshot
	uploadErr := p.objects.UploadStates.Lookup(hostLimitPrototypeKernelKey, &snapshot.Upload)
	if errors.Is(uploadErr, ebpf.ErrKeyNotExist) {
		uploadErr = nil
	}
	downloadErr := p.objects.DownloadStates.Lookup(hostLimitPrototypeKernelKey, &snapshot.Download)
	if errors.Is(downloadErr, ebpf.ErrKeyNotExist) {
		downloadErr = nil
	}
	return snapshot, errors.Join(uploadErr, downloadErr)
}

func (p *hostLimitPrototype) close() error {
	if p == nil || p.closed {
		return nil
	}
	p.closed = true
	var cleanupErrors []error
	// Delete the policy first. All attached programs immediately become
	// fail-open even if a later detach or state cleanup reports an error.
	if p.objects.Policies != nil {
		if err := p.objects.Policies.Delete(hostLimitPrototypeKernelKey); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("disable Host limit policy: %w", err))
		}
	}
	p.active = false

	paths := make([]string, 0, len(p.attachments))
	for path := range p.attachments {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		for _, attached := range p.attachments[path] {
			if err := attached.Close(); err != nil {
				cleanupErrors = append(cleanupErrors, fmt.Errorf("detach Host limit program from %s: %w", path, err))
			}
		}
		delete(p.attachments, path)
	}
	for _, stateMap := range []*ebpf.Map{p.objects.UploadStates, p.objects.DownloadStates} {
		if stateMap == nil {
			continue
		}
		if err := stateMap.Delete(hostLimitPrototypeKernelKey); err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("delete Host limit state: %w", err))
		}
	}
	if err := p.objects.Close(); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("close Host limit BPF objects: %w", err))
	}
	return errors.Join(cleanupErrors...)
}
