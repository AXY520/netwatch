package probe

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"netwatch/internal/lzcsdk"
)

const (
	networkMutationVerifyAttempts = 4
	networkMutationVerifyInterval = 500 * time.Millisecond
)

func (s *Service) verifyNetworkMutation(ctx context.Context, id string) NetworkMutationVerification {
	started := time.Now()
	report := NetworkMutationVerification{StartedAt: started.Format(time.RFC3339), Status: "failed"}
	mutation, err := s.getNetworkMutation(id, s.networkMutationKind(id))
	if err != nil {
		report.Steps = append(report.Steps, verificationStep("mutation", true, started, "", err))
		report.DurationMS = time.Since(started).Milliseconds()
		return report
	}

	var required []NetworkMutationVerificationStep
	for attempt := 0; attempt < networkMutationVerifyAttempts; attempt++ {
		required = verifyRequiredMutationState(ctx, mutation)
		if requiredStepsPassed(required) {
			break
		}
		if attempt+1 < networkMutationVerifyAttempts {
			select {
			case <-ctx.Done():
				attempt = networkMutationVerifyAttempts
			case <-time.After(networkMutationVerifyInterval):
			}
		}
	}
	report.Steps = append(report.Steps, required...)
	if !requiredStepsPassed(required) {
		report.Status = "failed"
		report.DurationMS = time.Since(started).Milliseconds()
		s.recordNetworkMutationVerification(id, report)
		return report
	}

	optionalCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	optional := s.verifyOptionalMutationConnectivity(optionalCtx, mutation)
	cancel()
	report.Steps = append(report.Steps, optional...)
	report.Status = "passed"
	for _, step := range optional {
		if !step.OK {
			report.Status = "warning"
			break
		}
	}
	report.DurationMS = time.Since(started).Milliseconds()
	s.recordNetworkMutationVerification(id, report)
	return report
}

func (s *Service) networkMutationKind(id string) networkMutationKind {
	s.network.mu.Lock()
	defer s.network.mu.Unlock()
	if s.network.active != nil && s.network.active.ID == id {
		return s.network.active.Kind
	}
	return ""
}

func verifyRequiredMutationState(ctx context.Context, mutation *networkMutation) []NetworkMutationVerificationStep {
	if mutation == nil {
		return []NetworkMutationVerificationStep{verificationStep("mutation", true, time.Now(), "", fmt.Errorf("network mutation is nil"))}
	}
	device := mutation.Target
	if mutation.Bridge != nil {
		device = mutation.Bridge.Bridge
	}
	started := time.Now()
	iface, err := net.InterfaceByName(device)
	steps := []NetworkMutationVerificationStep{verificationStep("interface_exists", true, started, device, err)}
	if err != nil {
		return steps
	}
	up := iface.Flags&net.FlagUp != 0
	var upErr error
	if !up {
		upErr = fmt.Errorf("interface %s is not up", device)
	}
	steps = append(steps, verificationStep("link_up", true, started, iface.Flags.String(), upErr))
	if upErr != nil {
		return steps
	}

	runtime, runtimeErr := readRuntimeConfigForVerification(ctx, device, iface)
	steps = append(steps, verifyRuntimeMutationConfig(mutation, runtime, runtimeErr)...)
	return steps
}

func readRuntimeConfigForVerification(ctx context.Context, device string, iface *net.Interface) (networkDeviceRuntimeConfig, error) {
	addrs, err := iface.Addrs()
	if err != nil {
		return networkDeviceRuntimeConfig{}, err
	}
	var runtime networkDeviceRuntimeConfig
	for _, addr := range addrs {
		if ip, _, err := net.ParseCIDR(addr.String()); err == nil && ip.To4() != nil {
			runtime.IPv4 = addr.String()
			break
		}
	}
	route := readDefaultIPv4Route()
	if route.Interface == device {
		runtime.Gateway = route.Gateway
	}
	runtime.DNS = readResolvDNS()

	// Host-network mode exposes the kernel's effective address and route to this
	// process. Those are authoritative for an IP mutation. Lazycat's NmcliCall
	// may legitimately return an empty device-show payload, which must not turn
	// a valid kernel configuration into an empty runtime observation.
	if nmcliTransportAvailable() {
		if nmRuntime, err := readNetworkDeviceRuntimeConfig(ctx, device); err == nil && nmRuntime.DNS != "" {
			runtime.DNS = nmRuntime.DNS
		}
	}
	return runtime, nil
}

func verifyRuntimeMutationConfig(m *networkMutation, runtime networkDeviceRuntimeConfig, runtimeErr error) []NetworkMutationVerificationStep {
	started := time.Now()
	if runtimeErr != nil {
		return []NetworkMutationVerificationStep{verificationStep("runtime_config", true, started, "", runtimeErr)}
	}
	var method, address, gateway, dns string
	switch m.Kind {
	case networkMutationIP:
		method, address, gateway, dns = m.IP.Request.Method, m.IP.Request.Address, m.IP.Request.Gateway, m.IP.Request.DNS
	case networkMutationBridge:
		method, address, gateway, dns = m.Bridge.Record.Method, m.Bridge.Record.Address, m.Bridge.Record.Gateway, m.Bridge.Record.DNS
	case networkMutationDNS:
		method, dns = m.DNS.Request.Method, m.DNS.Request.DNS
	}
	var steps []NetworkMutationVerificationStep
	if m.Kind != networkMutationDNS {
		addressOK := runtime.IPv4 != ""
		if method == "manual" && address != "" {
			addressOK = sameCIDRAddress(runtime.IPv4, address)
		}
		var err error
		if !addressOK {
			err = fmt.Errorf("runtime address %q does not match requested %q", runtime.IPv4, address)
		}
		steps = append(steps, verificationStep("ipv4_address", true, started, runtime.IPv4, err))
		if gateway != "" {
			var gwErr error
			if strings.TrimSpace(runtime.Gateway) != strings.TrimSpace(gateway) {
				gwErr = fmt.Errorf("runtime gateway %q does not match requested %q", runtime.Gateway, gateway)
			}
			steps = append(steps, verificationStep("default_gateway", true, started, runtime.Gateway, gwErr))
		}
	}
	if method == "manual" && dns != "" {
		var dnsErr error
		if !dnsContainsAll(runtime.DNS, dns) {
			dnsErr = fmt.Errorf("runtime DNS %q does not contain requested %q", runtime.DNS, dns)
		}
		steps = append(steps, verificationStep("dns_config", true, started, runtime.DNS, dnsErr))
	} else if m.Kind == networkMutationDNS && method == "auto" {
		var dnsErr error
		if strings.TrimSpace(runtime.DNS) == "" {
			dnsErr = fmt.Errorf("automatic DNS did not provide any runtime nameserver")
		}
		steps = append(steps, verificationStep("dns_config", true, started, runtime.DNS, dnsErr))
	}
	if len(steps) == 0 {
		steps = append(steps, verificationStep("runtime_config", true, started, "configuration applied", nil))
	}
	return steps
}

func (s *Service) verifyOptionalMutationConnectivity(ctx context.Context, mutation *networkMutation) []NetworkMutationVerificationStep {
	type result struct {
		index int
		step  NetworkMutationVerificationStep
	}
	checks := []func(context.Context) NetworkMutationVerificationStep{
		func(ctx context.Context) NetworkMutationVerificationStep {
			return verifyGatewayReachability(ctx, mutation)
		},
		s.verifyDNSResolution,
		func(ctx context.Context) NetworkMutationVerificationStep {
			return verifySiteGroup(ctx, "domestic_connectivity", s.cfg.DomesticSites)
		},
		func(ctx context.Context) NetworkMutationVerificationStep {
			return verifySiteGroup(ctx, "global_connectivity", s.cfg.GlobalSites)
		},
		verifyLazycatSystemAPI,
	}
	results := make(chan result, len(checks))
	var wg sync.WaitGroup
	for i, check := range checks {
		wg.Add(1)
		go func(index int, fn func(context.Context) NetworkMutationVerificationStep) {
			defer wg.Done()
			results <- result{index: index, step: fn(ctx)}
		}(i, check)
	}
	wg.Wait()
	close(results)
	steps := make([]NetworkMutationVerificationStep, len(checks))
	for result := range results {
		steps[result.index] = result.step
	}
	return steps
}

func verifyGatewayReachability(ctx context.Context, mutation *networkMutation) NetworkMutationVerificationStep {
	started := time.Now()
	gateway := ""
	if mutation != nil {
		switch mutation.Kind {
		case networkMutationIP:
			gateway = mutation.IP.Request.Gateway
		case networkMutationBridge:
			gateway = mutation.Bridge.Record.Gateway
		}
	}
	if strings.TrimSpace(gateway) == "" {
		return verificationStep("gateway_reachable", false, started, "no explicit gateway", nil)
	}
	bin, err := exec.LookPath("ping")
	if err != nil {
		return verificationStep("gateway_reachable", false, started, "ping unavailable", nil)
	}
	out, err := exec.CommandContext(ctx, bin, "-c", "1", "-W", "2", gateway).CombinedOutput()
	return verificationStep("gateway_reachable", false, started, strings.TrimSpace(string(out)), err)
}

func (s *Service) verifyDNSResolution(ctx context.Context) NetworkMutationVerificationStep {
	started := time.Now()
	host := firstVerificationHost(s.cfg.DomesticSites, s.cfg.GlobalSites)
	if host == "" {
		return verificationStep("dns_resolution", false, started, "no configured target", nil)
	}
	addresses, err := net.DefaultResolver.LookupHost(ctx, host)
	return verificationStep("dns_resolution", false, started, strings.Join(addresses, ","), err)
}

func verifySiteGroup(ctx context.Context, name string, sites []SiteTarget) NetworkMutationVerificationStep {
	started := time.Now()
	if len(sites) == 0 {
		return verificationStep(name, false, started, "no configured target", nil)
	}
	result := probeHTTPTarget(ctx, sites[0], 3*time.Second)
	var err error
	if result.Status != StatusOK {
		err = fmt.Errorf("%s: %s", sites[0].Name, result.Error)
	}
	return verificationStep(name, false, started, fmt.Sprintf("%s %dms", sites[0].Name, result.LatencyMS), err)
}

func verifyLazycatSystemAPI(ctx context.Context) NetworkMutationVerificationStep {
	started := time.Now()
	if !lzcsdk.Available() {
		return verificationStep("lazycat_system_api", false, started, "not running with Lazycat SDK", nil)
	}
	status, err := lzcsdk.FetchNetworkStatus(ctx)
	detail := fmt.Sprintf("connectivity=%s internet=%v", status.Connectivity, status.HasInternet)
	if err == nil && !status.HasInternet {
		err = fmt.Errorf("Lazycat reports no internet connectivity")
	}
	return verificationStep("lazycat_system_api", false, started, detail, err)
}

func verificationStep(name string, required bool, started time.Time, detail string, err error) NetworkMutationVerificationStep {
	step := NetworkMutationVerificationStep{Name: name, Required: required, OK: err == nil, DurationMS: time.Since(started).Milliseconds(), Detail: detail}
	if err != nil {
		step.Error = err.Error()
	}
	return step
}

func requiredStepsPassed(steps []NetworkMutationVerificationStep) bool {
	for _, step := range steps {
		if step.Required && !step.OK {
			return false
		}
	}
	return true
}

func sameCIDRAddress(runtime, requested string) bool {
	runtimeIP, runtimeNet, runtimeErr := net.ParseCIDR(strings.TrimSpace(runtime))
	requestedIP, requestedNet, requestedErr := net.ParseCIDR(strings.TrimSpace(requested))
	if runtimeErr != nil || requestedErr != nil || runtimeIP == nil || requestedIP == nil {
		return false
	}
	runtimeOnes, _ := runtimeNet.Mask.Size()
	requestedOnes, _ := requestedNet.Mask.Size()
	return runtimeIP.Equal(requestedIP) && runtimeOnes == requestedOnes
}

func dnsContainsAll(runtime, requested string) bool {
	have := map[string]struct{}{}
	for _, value := range strings.FieldsFunc(runtime, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		have[strings.TrimSpace(value)] = struct{}{}
	}
	for _, value := range strings.FieldsFunc(requested, func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if _, ok := have[strings.TrimSpace(value)]; !ok {
			return false
		}
	}
	return true
}

func firstVerificationHost(groups ...[]SiteTarget) string {
	for _, group := range groups {
		for _, target := range group {
			parsed, err := url.Parse(target.URL)
			if err == nil && parsed.Hostname() != "" {
				return parsed.Hostname()
			}
		}
	}
	return ""
}

func (s *Service) recordNetworkMutationVerification(id string, report NetworkMutationVerification) {
	s.network.mu.Lock()
	active := s.network.active
	if active == nil || active.ID != id {
		s.network.mu.Unlock()
		return
	}
	active.Verification = &report
	_ = s.persistNetworkMutationLocked()
	snapshot := *active
	s.network.mu.Unlock()
	s.auditNetworkMutation(NetworkMutationAuditEvent{Action: "verify", Verification: &report}, &snapshot)
}
