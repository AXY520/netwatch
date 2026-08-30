package probe

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"netwatch/internal/logger"
)

const (
	appProxyDialTimeout          = 10 * time.Second
	appProxyUDPIdle              = 2 * time.Minute
	appProxyUDPConntrackAttempts = 4
	appProxyUDPConntrackDelay    = 3 * time.Millisecond
)

var errUDPConntrackFlowNotFound = errors.New("nf_conntrack has no redirected UDP flow")

type appProxyAdapter struct {
	mu         sync.RWMutex
	config     AppProxySettings
	listenPort int
	tcp4       net.Listener
	tcp6       net.Listener
	udp4       *net.UDPConn
	udp6       *net.UDPConn

	sessionsMu sync.Mutex
	sessions   map[string]*socksUDPAssociation
}

func newAppProxyAdapter() *appProxyAdapter {
	return &appProxyAdapter{sessions: make(map[string]*socksUDPAssociation)}
}

func (a *appProxyAdapter) ensureStarted(config AppProxySettings) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tcp4 != nil {
		if a.config != config {
			return errors.New("application proxy adapter configuration is immutable")
		}
		return nil
	}

	ipv6Enabled := hostIPv6Enabled()
	var lastErr error
	for attempts := 0; attempts < 16; attempts++ {
		tcp4, err := net.Listen("tcp4", net.JoinHostPort("0.0.0.0", "0"))
		if err != nil {
			return fmt.Errorf("listen transparent TCP proxy: %w", err)
		}
		port := tcp4.Addr().(*net.TCPAddr).Port
		var tcp6 net.Listener
		var udp4, udp6 *net.UDPConn
		closeAttempt := func() {
			_ = tcp4.Close()
			if tcp6 != nil {
				_ = tcp6.Close()
			}
			if udp4 != nil {
				_ = udp4.Close()
			}
			if udp6 != nil {
				_ = udp6.Close()
			}
		}
		if ipv6Enabled {
			tcp6, err = net.Listen("tcp6", net.JoinHostPort("::", strconv.Itoa(port)))
			if err != nil {
				lastErr = fmt.Errorf("listen transparent IPv6 TCP proxy: %w", err)
				closeAttempt()
				continue
			}
		}
		if config.Protocol == "socks5" {
			udp4, err = listenOriginalDestinationUDP("udp4", net.JoinHostPort("0.0.0.0", strconv.Itoa(port)))
			if err != nil {
				lastErr = fmt.Errorf("listen transparent UDP proxy: %w", err)
				closeAttempt()
				continue
			}
			if ipv6Enabled {
				udp6, err = listenOriginalDestinationUDP("udp6", net.JoinHostPort("::", strconv.Itoa(port)))
				if err != nil {
					lastErr = fmt.Errorf("listen transparent IPv6 UDP proxy: %w", err)
					closeAttempt()
					continue
				}
			}
		}
		a.config, a.listenPort = config, port
		a.tcp4, a.tcp6, a.udp4, a.udp6 = tcp4, tcp6, udp4, udp6
		go a.acceptTCP(tcp4, false)
		if tcp6 != nil {
			go a.acceptTCP(tcp6, true)
		}
		if udp4 != nil {
			go a.readUDP(udp4)
		}
		if udp6 != nil {
			go a.readUDP(udp6)
		}
		return nil
	}
	return lastErr
}

func (a *appProxyAdapter) ready(config AppProxySettings) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.tcp4 != nil && a.listenPort > 0 && a.config == config
}

func (a *appProxyAdapter) port() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.listenPort
}

func (a *appProxyAdapter) close() {
	a.mu.Lock()
	if a.tcp4 != nil {
		_ = a.tcp4.Close()
	}
	if a.tcp6 != nil {
		_ = a.tcp6.Close()
	}
	if a.udp4 != nil {
		_ = a.udp4.Close()
	}
	if a.udp6 != nil {
		_ = a.udp6.Close()
	}
	a.tcp4, a.tcp6, a.udp4, a.udp6 = nil, nil, nil, nil
	a.listenPort = 0
	a.mu.Unlock()
	a.closeUDPSessions()
}

func (a *appProxyAdapter) currentConfig() AppProxySettings {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.config
}

func (a *appProxyAdapter) acceptTCP(listener net.Listener, ipv6 bool) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			tcp, ok := conn.(*net.TCPConn)
			if !ok {
				return
			}
			destination, err := originalTCPDestination(tcp, ipv6)
			if err != nil {
				logger.Warn("application proxy TCP original destination: %v", err)
				return
			}
			if destination.Port == a.port() && isLocalAddress(destination.IP) {
				return
			}
			upstream, err := dialAppProxyTCP(a.currentConfig(), destination)
			if err != nil {
				logger.Warn("application proxy TCP %s: %v", destination, err)
				return
			}
			defer upstream.Close()
			proxyBidirectional(tcp, upstream)
		}()
	}
}

func dialAppProxyTCP(config AppProxySettings, destination *net.TCPAddr) (net.Conn, error) {
	conn, _, err := dialAppProxyUpstream(config)
	if err != nil {
		return nil, err
	}
	var proxied net.Conn
	switch config.Protocol {
	case "http":
		proxied, err = httpConnect(conn, destination.String())
	case "socks5":
		proxied, err = socksConnect(conn, destination)
	default:
		err = fmt.Errorf("unsupported proxy protocol %q", config.Protocol)
	}
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return proxied, nil
}

func dialAppProxyUpstream(config AppProxySettings) (net.Conn, string, error) {
	var lastErr error
	for _, host := range appProxyDialHosts(config.Host) {
		endpoint := net.JoinHostPort(host, strconv.Itoa(config.Port))
		conn, err := net.DialTimeout("tcp", endpoint, appProxyDialTimeout)
		if err == nil {
			return conn, host, nil
		}
		lastErr = err
	}
	return nil, "", lastErr
}

func appProxyDialHosts(host string) []string {
	return appProxyDialHostsAt(host, isLocalAddress)
}

func appProxyDialHostsAt(host string, local func(net.IP) bool) []string {
	host = strings.TrimSpace(host)
	ip := net.ParseIP(host)
	if ip == nil || ip.IsLoopback() || local == nil || !local(ip) {
		return []string{host}
	}
	loopback := "127.0.0.1"
	if ip.To4() == nil {
		loopback = "::1"
	}
	return []string{loopback, host}
}

func httpConnect(conn net.Conn, destination string) (net.Conn, error) {
	_ = conn.SetDeadline(time.Now().Add(appProxyDialTimeout))
	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Connection: Keep-Alive\r\n\r\n", destination, destination); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodConnect})
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()
		return nil, fmt.Errorf("HTTP proxy returned %s", response.Status)
	}
	_ = conn.SetDeadline(time.Time{})
	if reader.Buffered() > 0 {
		return &bufferedProxyConn{Conn: conn, reader: reader}, nil
	}
	return conn, nil
}

type bufferedProxyConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedProxyConn) Read(body []byte) (int, error) { return c.reader.Read(body) }

func socksConnect(conn net.Conn, destination *net.TCPAddr) (net.Conn, error) {
	reader, err := socksHandshake(conn)
	if err != nil {
		return nil, err
	}
	request, err := socksRequest(1, destination.IP, destination.Port)
	if err != nil {
		return nil, err
	}
	if _, err := conn.Write(request); err != nil {
		return nil, err
	}
	if _, err := readSocksReply(reader); err != nil {
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	if reader.Buffered() > 0 {
		return &bufferedProxyConn{Conn: conn, reader: reader}, nil
	}
	return conn, nil
}

func socksHandshake(conn net.Conn) (*bufio.Reader, error) {
	_ = conn.SetDeadline(time.Now().Add(appProxyDialTimeout))
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		return nil, err
	}
	reader := bufio.NewReader(conn)
	reply := make([]byte, 2)
	if _, err := io.ReadFull(reader, reply); err != nil {
		return nil, err
	}
	if reply[0] != 5 || reply[1] != 0 {
		return nil, fmt.Errorf("SOCKS5 proxy rejected authentication method")
	}
	return reader, nil
}

func socksRequest(command byte, ip net.IP, port int) ([]byte, error) {
	address, err := encodeSocksAddress(ip, port)
	if err != nil {
		return nil, err
	}
	return append([]byte{5, command, 0}, address...), nil
}

func encodeSocksAddress(ip net.IP, port int) ([]byte, error) {
	if port < 0 || port > 65535 {
		return nil, errors.New("invalid SOCKS5 port")
	}
	var out []byte
	if v4 := ip.To4(); v4 != nil {
		out = append([]byte{1}, v4...)
	} else if v6 := ip.To16(); v6 != nil {
		out = append([]byte{4}, v6...)
	} else {
		return nil, errors.New("invalid SOCKS5 IP address")
	}
	return binary.BigEndian.AppendUint16(out, uint16(port)), nil
}

func readSocksReply(reader *bufio.Reader) (*net.UDPAddr, error) {
	header := make([]byte, 3)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, err
	}
	if header[0] != 5 || header[1] != 0 || header[2] != 0 {
		return nil, fmt.Errorf("SOCKS5 proxy returned code %d", header[1])
	}
	return readSocksAddress(reader)
}

func readSocksAddress(reader io.Reader) (*net.UDPAddr, error) {
	var atyp [1]byte
	if _, err := io.ReadFull(reader, atyp[:]); err != nil {
		return nil, err
	}
	var host string
	switch atyp[0] {
	case 1:
		body := make([]byte, 4)
		if _, err := io.ReadFull(reader, body); err != nil {
			return nil, err
		}
		host = net.IP(body).String()
	case 4:
		body := make([]byte, 16)
		if _, err := io.ReadFull(reader, body); err != nil {
			return nil, err
		}
		host = net.IP(body).String()
	case 3:
		var size [1]byte
		if _, err := io.ReadFull(reader, size[:]); err != nil {
			return nil, err
		}
		body := make([]byte, int(size[0]))
		if _, err := io.ReadFull(reader, body); err != nil {
			return nil, err
		}
		host = string(body)
	default:
		return nil, fmt.Errorf("unsupported SOCKS5 address type %d", atyp[0])
	}
	var port [2]byte
	if _, err := io.ReadFull(reader, port[:]); err != nil {
		return nil, err
	}
	return net.ResolveUDPAddr("udp", net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(port[:])))))
}

func proxyBidirectional(left, right net.Conn) {
	done := make(chan struct{}, 2)
	copyOneWay := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if closer, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = closer.CloseWrite()
		}
		done <- struct{}{}
	}
	go copyOneWay(left, right)
	go copyOneWay(right, left)
	<-done
	<-done
}

func originalTCPDestination(conn *net.TCPConn, ipv6 bool) (*net.TCPAddr, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil, err
	}
	var destination *net.TCPAddr
	var socketErr error
	err = raw.Control(func(fd uintptr) {
		destination, socketErr = originalTCPDestinationFD(fd, ipv6)
	})
	if err != nil {
		return nil, err
	}
	return destination, socketErr
}

func originalTCPDestinationFD(fd uintptr, ipv6 bool) (*net.TCPAddr, error) {
	if ipv6 {
		var address unix.RawSockaddrInet6
		size := uint32(unsafe.Sizeof(address))
		_, _, errno := unix.Syscall6(unix.SYS_GETSOCKOPT, fd, unix.SOL_IPV6, unix.SO_ORIGINAL_DST, uintptr(unsafe.Pointer(&address)), uintptr(unsafe.Pointer(&size)), 0)
		if errno != 0 {
			return nil, errno
		}
		return &net.TCPAddr{IP: net.IP(address.Addr[:]), Port: networkPort(address.Port), Zone: zoneName(address.Scope_id)}, nil
	}
	var address unix.RawSockaddrInet4
	size := uint32(unsafe.Sizeof(address))
	_, _, errno := unix.Syscall6(unix.SYS_GETSOCKOPT, fd, unix.SOL_IP, unix.SO_ORIGINAL_DST, uintptr(unsafe.Pointer(&address)), uintptr(unsafe.Pointer(&size)), 0)
	if errno != 0 {
		return nil, errno
	}
	return &net.TCPAddr{IP: net.IP(address.Addr[:]), Port: networkPort(address.Port)}, nil
}

func networkPort(port uint16) int { return int(port>>8 | port<<8) }

func zoneName(index uint32) string {
	if index == 0 {
		return ""
	}
	if iface, err := net.InterfaceByIndex(int(index)); err == nil {
		return iface.Name
	}
	return strconv.FormatUint(uint64(index), 10)
}

func isLocalAddress(ip net.IP) bool {
	interfaces, _ := net.Interfaces()
	for _, iface := range interfaces {
		addresses, _ := iface.Addrs()
		for _, address := range addresses {
			candidate, _, err := net.ParseCIDR(address.String())
			if err == nil && candidate.Equal(ip) {
				return true
			}
		}
	}
	return false
}

func listenOriginalDestinationUDP(network, address string) (*net.UDPConn, error) {
	listenConfig := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var socketErr error
		if err := raw.Control(func(fd uintptr) {
			level, option := unix.SOL_IP, unix.IP_RECVORIGDSTADDR
			if network == "udp6" {
				level, option = unix.SOL_IPV6, unix.IPV6_RECVORIGDSTADDR
			}
			socketErr = unix.SetsockoptInt(int(fd), level, option, 1)
		}); err != nil {
			return err
		}
		return socketErr
	}}
	packet, err := listenConfig.ListenPacket(context.Background(), network, address)
	if err != nil {
		return nil, err
	}
	return packet.(*net.UDPConn), nil
}

func (a *appProxyAdapter) readUDP(listener *net.UDPConn) {
	packet := make([]byte, 65535)
	oob := make([]byte, 256)
	for {
		n, oobn, _, client, err := listener.ReadMsgUDP(packet, oob)
		if err != nil {
			return
		}
		destination, err := originalUDPDestination(oob[:oobn])
		if err != nil {
			logger.Warn("application proxy UDP original destination: %v", err)
			continue
		}
		association, err := a.udpAssociation(listener, client)
		if err != nil {
			logger.Warn("application proxy UDP association: %v", err)
			continue
		}
		if destination.Port == a.port() && isLocalAddress(destination.IP) {
			conntrackDestination, err := association.redirectedDestination(a.port())
			if err != nil {
				logger.Warn("application proxy UDP conntrack destination: %v", err)
				continue
			}
			destination = conntrackDestination
		}
		if err := association.send(destination, packet[:n]); err != nil {
			association.close()
			logger.Warn("application proxy UDP %s: %v", destination, err)
		}
	}
}

func originalUDPDestination(oob []byte) (*net.UDPAddr, error) {
	messages, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, err
	}
	for index := range messages {
		address, err := unix.ParseOrigDstAddr(&messages[index])
		if err != nil {
			continue
		}
		switch value := address.(type) {
		case *unix.SockaddrInet4:
			return &net.UDPAddr{IP: net.IP(value.Addr[:]), Port: value.Port}, nil
		case *unix.SockaddrInet6:
			return &net.UDPAddr{IP: net.IP(value.Addr[:]), Port: value.Port, Zone: zoneName(value.ZoneId)}, nil
		}
	}
	return nil, errors.New("original UDP destination is unavailable")
}

func originalUDPConntrackDestination(client *net.UDPAddr, listenerPort int) (*net.UDPAddr, net.IP, error) {
	path := firstReadableFile(
		"/proc/net/nf_conntrack",
		filepath.Join(hostProcRoot(), "net", "nf_conntrack"),
	)
	if path == "" {
		return nil, nil, errors.New("nf_conntrack is unavailable")
	}
	return lookupUDPConntrackDestination(client, listenerPort, func() (io.ReadCloser, error) {
		return os.Open(path)
	}, time.Sleep)
}

func lookupUDPConntrackDestination(client *net.UDPAddr, listenerPort int, open func() (io.ReadCloser, error), sleep func(time.Duration)) (*net.UDPAddr, net.IP, error) {
	var lastErr error
	for attempt := 0; attempt < appProxyUDPConntrackAttempts; attempt++ {
		file, err := open()
		if err != nil {
			return nil, nil, err
		}
		destination, replySource, err := parseUDPConntrackDestination(file, client, listenerPort)
		_ = file.Close()
		if err == nil {
			return destination, replySource, nil
		}
		if !errors.Is(err, errUDPConntrackFlowNotFound) {
			return nil, nil, err
		}
		lastErr = err
		if attempt+1 < appProxyUDPConntrackAttempts {
			sleep(appProxyUDPConntrackDelay)
		}
	}
	return nil, nil, lastErr
}

func parseUDPConntrackDestination(reader io.Reader, client *net.UDPAddr, listenerPort int) (*net.UDPAddr, net.IP, error) {
	if client == nil || client.IP == nil || client.Port <= 0 || listenerPort <= 0 {
		return nil, nil, errors.New("invalid UDP conntrack lookup")
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 8 || fields[2] != "udp" {
			continue
		}
		var sources, destinations []net.IP
		var sourcePorts, destinationPorts []int
		for _, field := range fields {
			key, value, ok := strings.Cut(field, "=")
			if !ok {
				continue
			}
			switch key {
			case "src":
				sources = append(sources, net.ParseIP(value))
			case "dst":
				destinations = append(destinations, net.ParseIP(value))
			case "sport":
				if port, err := strconv.Atoi(value); err == nil {
					sourcePorts = append(sourcePorts, port)
				}
			case "dport":
				if port, err := strconv.Atoi(value); err == nil {
					destinationPorts = append(destinationPorts, port)
				}
			}
		}
		if len(sources) < 2 || len(destinations) < 2 || len(sourcePorts) < 2 || len(destinationPorts) < 2 {
			continue
		}
		if !client.IP.Equal(sources[0]) || client.Port != sourcePorts[0] ||
			!client.IP.Equal(destinations[1]) || client.Port != destinationPorts[1] || sourcePorts[1] != listenerPort {
			continue
		}
		if destinations[0] == nil || destinationPorts[0] <= 0 {
			continue
		}
		return &net.UDPAddr{IP: destinations[0], Port: destinationPorts[0]}, append(net.IP(nil), sources[1]...), nil
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	return nil, nil, fmt.Errorf("%w for %s", errUDPConntrackFlowNotFound, client)
}

type socksUDPAssociation struct {
	owner              *appProxyAdapter
	key                string
	client             *net.UDPAddr
	listener           *net.UDPConn
	control            net.Conn
	relay              *net.UDPConn
	once               sync.Once
	mu                 sync.Mutex
	destinationMu      sync.Mutex
	destination        *net.UDPAddr
	replySource        net.IP
	destinationExpires time.Time
}

func (a *appProxyAdapter) udpAssociation(listener *net.UDPConn, client *net.UDPAddr) (*socksUDPAssociation, error) {
	key := listener.LocalAddr().Network() + "|" + client.String()
	a.sessionsMu.Lock()
	current := a.sessions[key]
	a.sessionsMu.Unlock()
	if current != nil {
		return current, nil
	}
	created, err := newSocksUDPAssociation(a, key, listener, client, a.currentConfig())
	if err != nil {
		return nil, err
	}
	a.sessionsMu.Lock()
	if current = a.sessions[key]; current == nil {
		a.sessions[key] = created
		current = created
	}
	a.sessionsMu.Unlock()
	if current != created {
		created.close()
	}
	return current, nil
}

func newSocksUDPAssociation(owner *appProxyAdapter, key string, listener *net.UDPConn, client *net.UDPAddr, config AppProxySettings) (*socksUDPAssociation, error) {
	if config.Protocol != "socks5" {
		return nil, errors.New("SOCKS5 UDP is not configured")
	}
	control, upstreamHost, err := dialAppProxyUpstream(config)
	if err != nil {
		return nil, err
	}
	reader, err := socksHandshake(control)
	if err != nil {
		_ = control.Close()
		return nil, err
	}
	request, _ := socksRequest(3, net.IPv4zero, 0)
	if _, err := control.Write(request); err != nil {
		_ = control.Close()
		return nil, err
	}
	relayAddress, err := readSocksReply(reader)
	if err != nil {
		_ = control.Close()
		return nil, err
	}
	if relayAddress.IP == nil || relayAddress.IP.IsUnspecified() {
		relayAddress.IP = net.ParseIP(upstreamHost)
	} else if hosts := appProxyDialHosts(relayAddress.IP.String()); len(hosts) > 1 {
		relayAddress.IP = net.ParseIP(hosts[0])
	}
	relay, err := net.DialUDP("udp", nil, relayAddress)
	if err != nil {
		_ = control.Close()
		return nil, err
	}
	association := &socksUDPAssociation{owner: owner, key: key, client: cloneUDPAddr(client), listener: listener, control: control, relay: relay}
	_ = relay.SetReadDeadline(time.Now().Add(appProxyUDPIdle))
	go association.readReplies()
	go func() {
		_, _ = io.Copy(io.Discard, control)
		association.close()
	}()
	return association, nil
}

func (s *socksUDPAssociation) redirectedDestination(listenerPort int) (*net.UDPAddr, error) {
	s.destinationMu.Lock()
	if s.destination != nil && time.Now().Before(s.destinationExpires) {
		destination := cloneUDPAddr(s.destination)
		s.destinationMu.Unlock()
		return destination, nil
	}
	s.destinationMu.Unlock()

	destination, replySource, err := originalUDPConntrackDestination(s.client, listenerPort)
	if err != nil {
		return nil, err
	}
	s.destinationMu.Lock()
	s.destination = cloneUDPAddr(destination)
	s.replySource = append(net.IP(nil), replySource...)
	s.destinationExpires = time.Now().Add(time.Second)
	s.destinationMu.Unlock()
	return destination, nil
}

func (s *socksUDPAssociation) writeReply(payload []byte) error {
	s.destinationMu.Lock()
	replySource := append(net.IP(nil), s.replySource...)
	s.destinationMu.Unlock()
	if len(replySource) == 0 {
		_, err := s.listener.WriteToUDP(payload, s.client)
		return err
	}
	if source := replySource.To4(); source != nil {
		var info unix.Inet4Pktinfo
		copy(info.Spec_dst[:], source)
		_, _, err := s.listener.WriteMsgUDP(payload, unix.PktInfo4(&info), s.client)
		return err
	}
	if source := replySource.To16(); source != nil {
		var info unix.Inet6Pktinfo
		copy(info.Addr[:], source)
		_, _, err := s.listener.WriteMsgUDP(payload, unix.PktInfo6(&info), s.client)
		return err
	}
	return errors.New("invalid UDP reply source")
}

func (s *socksUDPAssociation) send(destination *net.UDPAddr, payload []byte) error {
	packet, err := encodeSocksUDPDatagram(destination, payload)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.relay.SetReadDeadline(time.Now().Add(appProxyUDPIdle))
	_, err = s.relay.Write(packet)
	return err
}

func (s *socksUDPAssociation) readReplies() {
	packet := make([]byte, 65535)
	for {
		n, err := s.relay.Read(packet)
		if err != nil {
			s.close()
			return
		}
		payload, err := decodeSocksUDPDatagram(packet[:n])
		if err != nil {
			continue
		}
		if err := s.writeReply(payload); err != nil {
			s.close()
			return
		}
	}
}

func (s *socksUDPAssociation) close() {
	s.once.Do(func() {
		_ = s.relay.Close()
		_ = s.control.Close()
		s.owner.sessionsMu.Lock()
		if s.owner.sessions[s.key] == s {
			delete(s.owner.sessions, s.key)
		}
		s.owner.sessionsMu.Unlock()
	})
}

func (a *appProxyAdapter) closeUDPSessions() {
	a.sessionsMu.Lock()
	sessions := make([]*socksUDPAssociation, 0, len(a.sessions))
	for _, session := range a.sessions {
		sessions = append(sessions, session)
	}
	a.sessionsMu.Unlock()
	for _, session := range sessions {
		session.close()
	}
}

func encodeSocksUDPDatagram(destination *net.UDPAddr, payload []byte) ([]byte, error) {
	address, err := encodeSocksAddress(destination.IP, destination.Port)
	if err != nil {
		return nil, err
	}
	packet := make([]byte, 0, 3+len(address)+len(payload))
	packet = append(packet, 0, 0, 0)
	packet = append(packet, address...)
	return append(packet, payload...), nil
}

func decodeSocksUDPDatagram(packet []byte) ([]byte, error) {
	if len(packet) < 4 || packet[0] != 0 || packet[1] != 0 || packet[2] != 0 {
		return nil, errors.New("invalid or fragmented SOCKS5 UDP datagram")
	}
	offset := 0
	switch packet[3] {
	case 1:
		offset = 4 + 4 + 2
	case 4:
		offset = 4 + 16 + 2
	case 3:
		if len(packet) < 5 {
			return nil, errors.New("invalid SOCKS5 UDP domain")
		}
		offset = 5 + int(packet[4]) + 2
	default:
		return nil, errors.New("invalid SOCKS5 UDP address type")
	}
	if offset > len(packet) {
		return nil, errors.New("short SOCKS5 UDP datagram")
	}
	return packet[offset:], nil
}

func cloneUDPAddr(address *net.UDPAddr) *net.UDPAddr {
	return &net.UDPAddr{IP: append(net.IP(nil), address.IP...), Port: address.Port, Zone: strings.Clone(address.Zone)}
}
