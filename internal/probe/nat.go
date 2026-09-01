package probe

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"
)

const (
	stunBindingRequest  = 0x0001
	stunMappedAddress   = 0x0001
	stunXORMappedAdress = 0x0020
	stunMagicCookie     = 0x2112A442

	NAT1       = "NAT1"
	NAT2       = "NAT2"
	NAT3       = "NAT3"
	NAT23      = "NAT2_OR_NAT3"
	NAT4       = "NAT4"
	NATUnknown = "unknown"
)

type natClassification struct {
	Type              string
	Confidence        string
	MappingBehavior   string
	FilteringBehavior string
	Diagnostic        string
}

func (s *Service) ProbeNAT(ctx context.Context) NATInfo {
	results := NATInfo{
		GeneratedAt: localTimestamp(),
		Type:        NATUnknown,
		Note:        "使用同一 UDP socket 向多个 STUN 服务器观测映射；结果反映微服出口，不代表浏览器所在网络。",
	}

	observations := parallelSTUNObservations(ctx, s.cfg.STUNServers, s.cfg.NATTimeout)
	results.Observations = successfulNATObservations(observations)
	results.Reachable = false
	for _, observation := range observations {
		if observation.ExternalAddr != "" {
			results.Reachable = true
			if results.ExternalAddr == "" {
				results.ExternalAddr = observation.ExternalAddr
			}
			break
		}
	}

	classification := classifyNATDetailed(observations, results.Reachable)
	results.Type = classification.Type
	results.Confidence = classification.Confidence
	results.MappingBehavior = classification.MappingBehavior
	results.FilteringBehavior = classification.FilteringBehavior
	results.Diagnostic = classification.Diagnostic
	return results
}

func successfulNATObservations(observations []NATObservation) []NATObservation {
	out := make([]NATObservation, 0, len(observations))
	for _, observation := range observations {
		if observation.ExternalAddr != "" {
			out = append(out, observation)
		}
	}
	return out
}

func parallelSTUNObservations(ctx context.Context, servers []string, timeout time.Duration) []NATObservation {
	return sharedSocketSTUNObservations(ctx, servers, timeout)
}

func classifyNAT(observations []NATObservation, reachable bool) (string, string) {
	classification := classifyNATDetailed(observations, reachable)
	return classification.Type, classification.Confidence
}

func classifyNATDetailed(observations []NATObservation, reachable bool) natClassification {
	if !reachable {
		return natClassification{
			Type:            NATUnknown,
			Confidence:      "low",
			MappingBehavior: "unknown",
			Diagnostic:      "UDP STUN 不可达；这可能是防火墙、代理、路由或服务器超时，不能据此判定为 NAT4",
		}
	}

	externalAddresses := map[string]struct{}{}
	successful := 0
	direct := true
	for _, observation := range observations {
		if observation.ExternalAddr == "" {
			continue
		}
		successful++
		externalAddresses[observation.ExternalAddr] = struct{}{}
		if observation.LocalAddr == "" || observation.LocalAddr != observation.ExternalAddr {
			direct = false
		}
	}

	if successful == 0 {
		return natClassification{Type: NATUnknown, Confidence: "low", MappingBehavior: "unknown", Diagnostic: "没有可用的 STUN 映射观测"}
	}
	if direct {
		return natClassification{
			Type:              NAT1,
			Confidence:        confidenceForSTUN(successful),
			MappingBehavior:   "public",
			FilteringBehavior: "not_applicable",
			Diagnostic:        "本地 UDP 地址与 STUN 映射地址一致，未观察到地址转换",
		}
	}
	if successful < 2 {
		return natClassification{
			Type:            NATUnknown,
			Confidence:      "low",
			MappingBehavior: "unknown",
			Diagnostic:      "只有一个 STUN 服务器返回结果，无法比较不同目标下的端口映射",
		}
	}
	if len(externalAddresses) == 1 {
		return natClassification{
			Type:              NAT23,
			Confidence:        confidenceForSTUN(successful),
			MappingBehavior:   "endpoint_independent",
			FilteringBehavior: "unknown",
			Diagnostic:        "不同 STUN 目标得到相同映射；普通 Binding 响应无法继续区分 NAT2 与 NAT3",
		}
	}
	return natClassification{
		Type:              NAT4,
		Confidence:        confidenceForSTUN(successful),
		MappingBehavior:   "endpoint_dependent",
		FilteringBehavior: "unknown",
		Diagnostic:        "同一 UDP socket 访问不同 STUN 目标时映射发生变化，符合对称型/目标相关映射特征",
	}
}

func confidenceForSTUN(successful int) string {
	if successful >= 2 {
		return "high"
	}
	return "medium"
}

func sharedSocketSTUNObservations(ctx context.Context, servers []string, timeout time.Duration) []NATObservation {
	observations := make([]NATObservation, len(servers))
	for i, server := range servers {
		observations[i].Server = server
	}
	if len(servers) == 0 {
		return observations
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		for i := range observations {
			observations[i].Error = err.Error()
		}
		return observations
	}
	defer conn.Close()
	localPort := conn.LocalAddr().(*net.UDPAddr).Port

	type pendingRequest struct {
		index int
		txID  []byte
	}
	type resolvedServer struct {
		index  int
		remote *net.UDPAddr
		err    error
	}
	pending := make(map[string]pendingRequest, len(servers))
	resolved := make(chan resolvedServer, len(servers))
	for index, server := range servers {
		go func(index int, server string) {
			remote, resolveErr := resolveUDP4Addr(probeCtx, server)
			resolved <- resolvedServer{index: index, remote: remote, err: resolveErr}
		}(index, server)
	}

resolveLoop:
	for range servers {
		var target resolvedServer
		select {
		case target = <-resolved:
		case <-probeCtx.Done():
			for index := range observations {
				if observations[index].Error == "" && observations[index].LocalAddr == "" {
					observations[index].Error = probeCtx.Err().Error()
				}
			}
			break resolveLoop
		}
		if target.err != nil {
			observations[target.index].Error = target.err.Error()
			continue
		}
		observations[target.index].LocalAddr = routeLocalAddress(target.remote, localPort)
		txID := make([]byte, 12)
		if _, readErr := rand.Read(txID); readErr != nil {
			observations[target.index].Error = readErr.Error()
			continue
		}
		request := make([]byte, 20)
		binary.BigEndian.PutUint16(request[0:2], stunBindingRequest)
		binary.BigEndian.PutUint32(request[4:8], stunMagicCookie)
		copy(request[8:20], txID)
		if _, writeErr := conn.WriteToUDP(request, target.remote); writeErr != nil {
			observations[target.index].Error = writeErr.Error()
			continue
		}
		pending[string(txID)] = pendingRequest{index: target.index, txID: txID}
	}

	deadline, _ := probeCtx.Deadline()
	_ = conn.SetReadDeadline(deadline)
	response := make([]byte, 2048)
	for len(pending) > 0 {
		n, _, readErr := conn.ReadFromUDP(response)
		if readErr != nil {
			break
		}
		if n < 20 || binary.BigEndian.Uint32(response[4:8]) != stunMagicCookie {
			continue
		}
		key := string(response[8:20])
		request, ok := pending[key]
		if !ok {
			continue
		}
		address, parseErr := parseSTUNResponse(response[:n], request.txID)
		if parseErr != nil {
			observations[request.index].Error = parseErr.Error()
		} else {
			observations[request.index].ExternalAddr = address
		}
		delete(pending, key)
	}
	for _, request := range pending {
		if err := probeCtx.Err(); err != nil {
			observations[request.index].Error = err.Error()
		} else {
			observations[request.index].Error = "stun timeout"
		}
	}
	return observations
}

func resolveUDP4Addr(ctx context.Context, address string) (*net.UDPAddr, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid stun port %q", portText)
	}
	if parsed := net.ParseIP(host); parsed != nil {
		if ipv4 := parsed.To4(); ipv4 != nil {
			return &net.UDPAddr{IP: ipv4, Port: port}, nil
		}
		return nil, fmt.Errorf("stun server %q has no IPv4 address", host)
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if ipv4 := ip.To4(); ipv4 != nil {
			return &net.UDPAddr{IP: ipv4, Port: port}, nil
		}
	}
	return nil, fmt.Errorf("stun server %q has no IPv4 address", host)
}

func routeLocalAddress(remote *net.UDPAddr, port int) string {
	conn, err := net.DialUDP("udp4", nil, remote)
	if err != nil {
		return net.JoinHostPort(net.IPv4zero.String(), strconv.Itoa(port))
	}
	defer conn.Close()
	local, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		return net.JoinHostPort(net.IPv4zero.String(), strconv.Itoa(port))
	}
	return net.JoinHostPort(local.IP.String(), strconv.Itoa(port))
}

func parseSTUNResponse(message, txID []byte) (string, error) {
	if len(message) < 20 {
		return "", errors.New("short stun response")
	}
	if binary.BigEndian.Uint32(message[4:8]) != stunMagicCookie {
		return "", errors.New("invalid stun cookie")
	}
	if string(message[8:20]) != string(txID) {
		return "", errors.New("stun transaction mismatch")
	}

	offset := 20
	for offset+4 <= len(message) {
		attrType := binary.BigEndian.Uint16(message[offset : offset+2])
		attrLen := int(binary.BigEndian.Uint16(message[offset+2 : offset+4]))
		offset += 4
		if offset+attrLen > len(message) {
			return "", errors.New("invalid stun attribute length")
		}
		value := message[offset : offset+attrLen]
		switch attrType {
		case stunXORMappedAdress:
			return decodeXORMappedAddress(value, txID)
		case stunMappedAddress:
			return decodeMappedAddress(value)
		}
		offset += attrLen
		if remainder := attrLen % 4; remainder != 0 {
			offset += 4 - remainder
		}
	}
	return "", errors.New("no mapped address in stun response")
}

func decodeMappedAddress(value []byte) (string, error) {
	if len(value) < 8 {
		return "", errors.New("mapped address too short")
	}
	family := value[1]
	port := binary.BigEndian.Uint16(value[2:4])
	switch family {
	case 0x01:
		return net.JoinHostPort(net.IP(value[4:8]).String(), strconv.Itoa(int(port))), nil
	case 0x02:
		if len(value) < 20 {
			return "", errors.New("ipv6 mapped address too short")
		}
		return net.JoinHostPort(net.IP(value[4:20]).String(), strconv.Itoa(int(port))), nil
	default:
		return "", fmt.Errorf("unknown address family %d", family)
	}
}

func decodeXORMappedAddress(value, txID []byte) (string, error) {
	if len(value) < 8 {
		return "", errors.New("xor mapped address too short")
	}
	family := value[1]
	xorPort := binary.BigEndian.Uint16(value[2:4])
	port := xorPort ^ uint16(stunMagicCookie>>16)

	switch family {
	case 0x01:
		cookie := make([]byte, 4)
		binary.BigEndian.PutUint32(cookie, stunMagicCookie)
		ip := make(net.IP, 4)
		for i := range ip {
			ip[i] = value[4+i] ^ cookie[i]
		}
		return net.JoinHostPort(ip.String(), strconv.Itoa(int(port))), nil
	case 0x02:
		if len(value) < 20 {
			return "", errors.New("ipv6 xor mapped address too short")
		}
		mask := make([]byte, 16)
		binary.BigEndian.PutUint32(mask[0:4], stunMagicCookie)
		copy(mask[4:], txID)
		ip := make(net.IP, 16)
		for i := range ip {
			ip[i] = value[4+i] ^ mask[i]
		}
		return net.JoinHostPort(ip.String(), strconv.Itoa(int(port))), nil
	default:
		return "", fmt.Errorf("unknown address family %d", family)
	}
}
