package smtpserver

import (
	"context"
	"net"
	"net/netip"
	"sync"
)

type activeConnection struct {
	conn   net.Conn
	cancel context.CancelFunc
	source connectionSource
}

type connectionSource struct {
	kind    uint8
	network string
	value   string
}

const (
	connectionSourceUnknown uint8 = iota
	connectionSourceIP
	connectionSourceHostPort
	connectionSourceAddr
)

var unknownConnectionSource = connectionSource{kind: connectionSourceUnknown}

// connectionRegistry is owned by one Server instance. It coordinates graceful
// shutdown without creating a process-wide connection or resource budget.
type connectionRegistry struct {
	mu        sync.Mutex
	accepting bool
	max       int
	maxSource int
	active    map[net.Conn]activeConnection
	bySource  map[connectionSource]int
	wait      sync.WaitGroup
}

func newConnectionRegistry(maximum, maximumPerSource int) *connectionRegistry {
	return &connectionRegistry{
		accepting: true,
		max:       maximum,
		maxSource: maximumPerSource,
		active:    make(map[net.Conn]activeConnection),
		bySource:  make(map[connectionSource]int),
	}
}

func (r *connectionRegistry) register(conn net.Conn, cancel context.CancelFunc, source connectionSource) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.accepting || (r.max > 0 && len(r.active) >= r.max) || r.bySource[source] >= r.maxSource {
		return false
	}
	if _, exists := r.active[conn]; exists {
		return false
	}
	r.active[conn] = activeConnection{conn: conn, cancel: cancel, source: source}
	r.bySource[source]++
	r.wait.Add(1)
	return true
}

func (r *connectionRegistry) unregister(conn net.Conn) {
	r.mu.Lock()
	connection, ok := r.active[conn]
	if !ok {
		r.mu.Unlock()
		return
	}
	delete(r.active, conn)
	if remaining := r.bySource[connection.source] - 1; remaining == 0 {
		delete(r.bySource, connection.source)
	} else {
		r.bySource[connection.source] = remaining
	}
	r.mu.Unlock()
	r.wait.Done()
}

func connectionSourceForAddr(addr net.Addr) connectionSource {
	if addr == nil {
		return unknownConnectionSource
	}
	switch value := addr.(type) {
	case *net.TCPAddr:
		return connectionSourceForIP(value.IP, value.Zone)
	case *net.UDPAddr:
		return connectionSourceForIP(value.IP, value.Zone)
	case *net.IPAddr:
		return connectionSourceForIP(value.IP, value.Zone)
	}

	network, address := addr.Network(), addr.String()
	if network == "" || address == "" {
		return unknownConnectionSource
	}
	host, _, err := net.SplitHostPort(address)
	if err == nil {
		if host == "" {
			return unknownConnectionSource
		}
		if ip, parseErr := netip.ParseAddr(host); parseErr == nil {
			return canonicalIPConnectionSource(ip)
		}
		return connectionSource{kind: connectionSourceHostPort, network: network, value: host}
	}
	return connectionSource{kind: connectionSourceAddr, network: network, value: address}
}

func connectionSourceForIP(ip net.IP, zone string) connectionSource {
	address, ok := netip.AddrFromSlice(ip)
	if !ok {
		return unknownConnectionSource
	}
	address = address.Unmap()
	if zone != "" && address.Is6() {
		address = address.WithZone(zone)
	}
	return canonicalIPConnectionSource(address)
}

func canonicalIPConnectionSource(address netip.Addr) connectionSource {
	address = address.Unmap()
	if !address.IsValid() {
		return unknownConnectionSource
	}
	return connectionSource{kind: connectionSourceIP, value: address.String()}
}

// shutdown prevents later registration and cancels every connection context
// immediately. It then waits for handlers to reach a protocol-legal exit until
// ctx expires, at which point it force-closes the remaining transports.
func (r *connectionRegistry) shutdown(ctx context.Context) error {
	r.mu.Lock()
	r.accepting = false
	connections := make([]activeConnection, 0, len(r.active))
	for _, connection := range r.active {
		connections = append(connections, connection)
	}
	r.mu.Unlock()
	for _, connection := range connections {
		connection.cancel()
	}

	done := make(chan struct{})
	go func() {
		r.wait.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		r.closeAll()
		return ctx.Err()
	}
}

func (r *connectionRegistry) closeAll() {
	r.mu.Lock()
	connections := make([]net.Conn, 0, len(r.active))
	for conn := range r.active {
		connections = append(connections, conn)
	}
	r.mu.Unlock()
	for _, conn := range connections {
		_ = conn.Close()
	}
}
