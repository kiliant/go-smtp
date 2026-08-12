package smtpserver

import (
	"context"
	"net"
	"sync"
)

type activeConnection struct {
	conn   net.Conn
	cancel context.CancelFunc
}

// connectionRegistry is owned by one Server instance. It coordinates graceful
// shutdown without creating a process-wide connection or resource budget.
type connectionRegistry struct {
	mu        sync.Mutex
	accepting bool
	active    map[net.Conn]activeConnection
	wait      sync.WaitGroup
}

func newConnectionRegistry() *connectionRegistry {
	return &connectionRegistry{accepting: true, active: make(map[net.Conn]activeConnection)}
}

func (r *connectionRegistry) register(conn net.Conn, cancel context.CancelFunc) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.accepting {
		return false
	}
	r.active[conn] = activeConnection{conn: conn, cancel: cancel}
	r.wait.Add(1)
	return true
}

func (r *connectionRegistry) unregister(conn net.Conn) {
	r.mu.Lock()
	if _, ok := r.active[conn]; !ok {
		r.mu.Unlock()
		return
	}
	delete(r.active, conn)
	r.mu.Unlock()
	r.wait.Done()
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
