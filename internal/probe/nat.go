package probe

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"
)

const (
	stunBindingRequest  = 0x0001
	stunMappedAddress   = 0x0001
	stunXORMappedAdress = 0x0020
	stunMagicCookie     = 0x2112A442

	NATFullCone          = "full_cone"
	NATRestrictedCone    = "restricted_cone"
	NATPortRestrictedCone = "port_restricted_cone"
	NATSymmetric         = "symmetric"
	NATUnknown           = "unknown"
)

func (s *Service) ProbeNAT(ctx context.Context) NATInfo {
	results := NATInfo{
		GeneratedAt: localTimestamp(),
		Type:        NATUnknown,
		Note:        "NAT 类型基于 STUN 多点观测推断，目前只输出全锥形、受限锥形、端口受限锥形、对称型。",
	}

	observations := parallelSTUNObservations(ctx, s.cfg.STUNServers, s.cfg.NATTimeout)
	results.Reachable = false
	for _, observation := range observations {
		if observation.ExternalAddr != "" {
			results.Reachable = true
			break
		}
	}

	results.Type = classifyNAT(observations, results.Reachable)
	return results
}

func parallelSTUNObservations(ctx context.Context, servers []string, timeout time.Duration) []NATObservation {
	observations := make([]NATObservation, len(servers))
	var wg sync.WaitGroup
	wg.Add(len(servers))

	for i, server := range servers {
		go func(index int, server string) {
			defer wg.Done()
			observations[index] = stunBindingObservation(ctx, server, timeout)
		}(i, server)
	}

	wg.Wait()
	return observations
}

func classifyNAT(observations []NATObservation, reachable bool) string {
	if !reachable {
		return NATUnknown
	}

	externalHosts := map[string]struct{}{}
	externalPorts := map[string]struct{}{}
	for _, observation := range observations {
		if observation.ExternalAddr == "" {
			continue
		}
		host, port, err := net.SplitHostPort(observation.ExternalAddr)
		if err != nil {
			continue
		}
		externalHosts[host] = struct{}{}
		externalPorts[port] = struct{}{}
	}

	if len(externalHosts) == 0 {
		return NATUnknown
	}

	if len(externalHosts) > 1 || len(externalPorts) > 1 {
		return NATSymmetric
	}

	// RFC 3489 style exact cone subtyping needs CHANGE-REQUEST support and filtering tests.
	// Without reliable filtering tests from all servers, default to restricted vs port-restricted heuristics.
	if len(observations) >= 3 {
		return NATRestrictedCone
	}
	return NATPortRestrictedCone
}

func stunBindingObservation(ctx context.Context, server string, timeout time.Duration) NATObservation {
	observation := NATObservation{Server: server}

	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "udp4", server)
	if err != nil {
		observation.Error = err.Error()
		return observation
	}
	defer conn.Close()

	observation.LocalAddr = conn.LocalAddr().String()

	txID := make([]byte, 12)
	if _, err := rand.Read(txID); err != nil {
		observation.Error = err.Error()
		return observation
	}

	request := make([]byte, 20)
	binary.BigEndian.PutUint16(request[0:2], stunBindingRequest)
	binary.BigEndian.PutUint16(request[2:4], 0)
	binary.BigEndian.PutUint32(request[4:8], stunMagicCookie)
	copy(request[8:20], txID)

	_ = conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write(request); err != nil {
		observation.Error = err.Error()
		return observation
	}

	response := make([]byte, 1024)
	n, err := conn.Read(response)
	if err != nil {
		observation.Error = err.Error()
		return observation
	}

	address, err := parseSTUNResponse(response[:n], txID)
	if err != nil {
		observation.Error = err.Error()
		return observation
	}
	observation.ExternalAddr = address
	return observation
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
