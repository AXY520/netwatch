package probe

import (
	"context"
	"encoding/binary"
	"net"
	"strconv"
	"testing"
	"time"
)

func TestClassifyNAT1WhenLocalAndExternalMatch(t *testing.T) {
	got, confidence := classifyNAT([]NATObservation{
		{LocalAddr: "203.0.113.10:50000", ExternalAddr: "203.0.113.10:50000"},
		{LocalAddr: "203.0.113.10:50000", ExternalAddr: "203.0.113.10:50000"},
	}, true)
	if got != NAT1 || confidence != "high" {
		t.Fatalf("classifyNAT = %s/%s, want NAT1/high", got, confidence)
	}
}

func TestClassifyNAT4WhenMappingChangesAcrossServers(t *testing.T) {
	got, confidence := classifyNAT([]NATObservation{
		{LocalAddr: "192.168.1.10:50000", ExternalAddr: "198.51.100.20:41000"},
		{LocalAddr: "192.168.1.10:50000", ExternalAddr: "198.51.100.20:42000"},
	}, true)
	if got != NAT4 || confidence != "high" {
		t.Fatalf("classifyNAT = %s/%s, want NAT4/high", got, confidence)
	}
}

func TestClassifyNATUnknownWhenSTUNUnreachable(t *testing.T) {
	got, confidence := classifyNAT(nil, false)
	if got != NATUnknown || confidence != "low" {
		t.Fatalf("classifyNAT = %s/%s, want unknown/low", got, confidence)
	}
}

func TestClassifyNAT23WhenMappingIsEndpointIndependent(t *testing.T) {
	classification := classifyNATDetailed([]NATObservation{
		{Server: "stun-a:3478", LocalAddr: "192.168.1.10:50000", ExternalAddr: "198.51.100.20:41000"},
		{Server: "stun-b:3478", LocalAddr: "192.168.1.10:50000", ExternalAddr: "198.51.100.20:41000"},
	}, true)
	if classification.Type != NAT23 || classification.Confidence != "high" {
		t.Fatalf("classification = %+v, want NAT2_OR_NAT3/high", classification)
	}
	if classification.MappingBehavior != "endpoint_independent" || classification.FilteringBehavior != "unknown" {
		t.Fatalf("classification behavior = %+v", classification)
	}
}

func TestClassifyNATUnknownWithOnlyOneObservation(t *testing.T) {
	classification := classifyNATDetailed([]NATObservation{
		{Server: "stun-a:3478", LocalAddr: "192.168.1.10:50000", ExternalAddr: "198.51.100.20:41000"},
	}, true)
	if classification.Type != NATUnknown || classification.Confidence != "low" {
		t.Fatalf("classification = %+v, want unknown/low", classification)
	}
}

func TestSharedSocketSTUNObservationsReuseSourcePort(t *testing.T) {
	firstServer, firstSourcePort := startSTUNTestServer(t, 41000)
	secondServer, secondSourcePort := startSTUNTestServer(t, 41000)

	observations := sharedSocketSTUNObservations(context.Background(), []string{firstServer, secondServer}, time.Second)
	if len(observations) != 2 {
		t.Fatalf("observations = %d, want 2", len(observations))
	}
	for index, observation := range observations {
		if observation.Error != "" {
			t.Fatalf("observation %d error = %q", index, observation.Error)
		}
		if observation.ExternalAddr != "203.0.113.10:41000" {
			t.Fatalf("observation %d external address = %q", index, observation.ExternalAddr)
		}
	}

	_, firstLocalPort, err := net.SplitHostPort(observations[0].LocalAddr)
	if err != nil {
		t.Fatalf("first local address = %q: %v", observations[0].LocalAddr, err)
	}
	_, secondLocalPort, err := net.SplitHostPort(observations[1].LocalAddr)
	if err != nil {
		t.Fatalf("second local address = %q: %v", observations[1].LocalAddr, err)
	}
	if firstLocalPort != secondLocalPort {
		t.Fatalf("local source ports differ: %s != %s", firstLocalPort, secondLocalPort)
	}
	wantPort, _ := strconv.Atoi(firstLocalPort)
	if got := <-firstSourcePort; got != wantPort {
		t.Fatalf("first server observed source port %d, want %d", got, wantPort)
	}
	if got := <-secondSourcePort; got != wantPort {
		t.Fatalf("second server observed source port %d, want %d", got, wantPort)
	}
}

func startSTUNTestServer(t *testing.T, mappedPort uint16) (string, <-chan int) {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	sourcePort := make(chan int, 1)
	go func() {
		request := make([]byte, 512)
		n, remote, readErr := conn.ReadFromUDP(request)
		if readErr != nil || n < 20 {
			sourcePort <- -1
			return
		}
		response := make([]byte, 32)
		binary.BigEndian.PutUint16(response[0:2], 0x0101)
		binary.BigEndian.PutUint16(response[2:4], 12)
		binary.BigEndian.PutUint32(response[4:8], stunMagicCookie)
		copy(response[8:20], request[8:20])
		binary.BigEndian.PutUint16(response[20:22], stunMappedAddress)
		binary.BigEndian.PutUint16(response[22:24], 8)
		response[25] = 0x01
		binary.BigEndian.PutUint16(response[26:28], mappedPort)
		copy(response[28:32], net.IPv4(203, 0, 113, 10).To4())
		if _, writeErr := conn.WriteToUDP(response, remote); writeErr != nil {
			sourcePort <- -1
			return
		}
		sourcePort <- remote.Port
	}()
	return conn.LocalAddr().String(), sourcePort
}
