package smtpserver

import (
	"bufio"
	"context"
	"net"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestConnectionSourceIdentity(t *testing.T) {
	tests := []struct {
		name  string
		left  net.Addr
		right net.Addr
		equal bool
	}{
		{
			name:  "same IPv4 different ports",
			left:  &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 25},
			right: &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 587},
			equal: true,
		},
		{
			name:  "IPv4-mapped collides with IPv4",
			left:  &net.TCPAddr{IP: net.ParseIP("::ffff:192.0.2.1"), Port: 25},
			right: &net.TCPAddr{IP: net.ParseIP("192.0.2.1").To4(), Port: 587},
			equal: true,
		},
		{
			name:  "IPv6 canonical spelling",
			left:  fixedAddr{network: "tcp", address: "[2001:0db8:0:0::1]:25"},
			right: fixedAddr{network: "tcp", address: "[2001:db8::1]:587"},
			equal: true,
		},
		{
			name:  "IPv6 zone preserved",
			left:  &net.TCPAddr{IP: net.ParseIP("fe80::1"), Zone: "en0", Port: 25},
			right: &net.TCPAddr{IP: net.ParseIP("fe80::1"), Zone: "en1", Port: 25},
			equal: false,
		},
		{
			name:  "distinct IP",
			left:  &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 25},
			right: &net.TCPAddr{IP: net.ParseIP("192.0.2.2"), Port: 25},
			equal: false,
		},
		{
			name:  "generic host port drops port",
			left:  fixedAddr{network: "testnet", address: "peer.example:25"},
			right: fixedAddr{network: "testnet", address: "peer.example:587"},
			equal: true,
		},
		{
			name:  "generic host port retains network",
			left:  fixedAddr{network: "first", address: "peer.example:25"},
			right: fixedAddr{network: "second", address: "peer.example:25"},
			equal: false,
		},
		{
			name:  "non host port uses network and string",
			left:  fixedAddr{network: "pipe", address: "peer"},
			right: fixedAddr{network: "pipe", address: "peer"},
			equal: true,
		},
		{
			name:  "non host port retains network",
			left:  fixedAddr{network: "first", address: "peer"},
			right: fixedAddr{network: "second", address: "peer"},
			equal: false,
		},
		{
			name:  "nil and unusable share sentinel",
			left:  nil,
			right: fixedAddr{},
			equal: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			left := connectionSourceForAddr(test.left)
			right := connectionSourceForAddr(test.right)
			if got := left == right; got != test.equal {
				t.Fatalf("source equality = %v, want %v: left=%+v right=%+v", got, test.equal, left, right)
			}
		})
	}
}

func TestConnectionRegistryEnforcesIndependentBounds(t *testing.T) {
	t.Run("per source", func(t *testing.T) {
		registry := newConnectionRegistry(10, 2)
		source := connectionSourceForAddr(&net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 25})
		other := connectionSourceForAddr(&net.TCPAddr{IP: net.ParseIP("192.0.2.2"), Port: 25})
		first := registerTestConnection(t, registry, source)
		second := registerTestConnection(t, registry, source)
		server, client := net.Pipe()
		t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
		if registry.register(server, func() {}, source) {
			t.Fatal("third connection from one source exceeded its cap")
		}
		third := registerTestConnection(t, registry, other)
		registry.unregister(first)
		registry.unregister(second)
		registry.unregister(third)
	})

	t.Run("global still wins", func(t *testing.T) {
		registry := newConnectionRegistry(2, 10)
		first := registerTestConnection(t, registry, connectionSource{kind: connectionSourceAddr, value: "first"})
		second := registerTestConnection(t, registry, connectionSource{kind: connectionSourceAddr, value: "second"})
		server, client := net.Pipe()
		t.Cleanup(func() { _ = server.Close(); _ = client.Close() })
		if registry.register(server, func() {}, connectionSource{kind: connectionSourceAddr, value: "third"}) {
			t.Fatal("third connection exceeded the global cap")
		}
		registry.unregister(first)
		registry.unregister(second)
	})
}

func TestConnectionRegistryReleaseDeletesSourceAndReadmits(t *testing.T) {
	registry := newConnectionRegistry(10, 1)
	source := connectionSourceForAddr(&net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 25})
	first := registerTestConnection(t, registry, source)
	registry.unregister(first)
	registry.unregister(first)

	registry.mu.Lock()
	if len(registry.active) != 0 || len(registry.bySource) != 0 {
		t.Fatalf("registry retained released membership: active=%d sources=%#v", len(registry.active), registry.bySource)
	}
	registry.mu.Unlock()

	second := registerTestConnection(t, registry, source)
	registry.unregister(second)
}

func TestConnectionRegistryConcurrentBounds(t *testing.T) {
	for _, test := range []struct {
		name       string
		global     int
		perSource  int
		sourceFor  func(int) connectionSource
		wantAccept int
	}{
		{
			name:       "per source",
			global:     100,
			perSource:  7,
			sourceFor:  func(int) connectionSource { return connectionSource{kind: connectionSourceAddr, value: "same"} },
			wantAccept: 7,
		},
		{
			name:      "global",
			global:    11,
			perSource: 100,
			sourceFor: func(index int) connectionSource {
				return connectionSource{kind: connectionSourceAddr, value: string(rune(index + 1))}
			},
			wantAccept: 11,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			registry := newConnectionRegistry(test.global, test.perSource)
			const attempts = 128
			servers := make([]net.Conn, attempts)
			clients := make([]net.Conn, attempts)
			accepted := make(chan net.Conn, attempts)
			var wait sync.WaitGroup
			for index := range attempts {
				servers[index], clients[index] = net.Pipe()
				wait.Add(1)
				go func(index int) {
					defer wait.Done()
					if registry.register(servers[index], func() {}, test.sourceFor(index)) {
						accepted <- servers[index]
					}
				}(index)
			}
			wait.Wait()
			close(accepted)
			var registered []net.Conn
			for conn := range accepted {
				registered = append(registered, conn)
			}
			if len(registered) != test.wantAccept {
				t.Fatalf("accepted = %d, want %d", len(registered), test.wantAccept)
			}
			for _, conn := range registered {
				registry.unregister(conn)
			}
			for index := range attempts {
				_ = servers[index].Close()
				_ = clients[index].Close()
			}
			registry.mu.Lock()
			defer registry.mu.Unlock()
			if len(registry.active) != 0 || len(registry.bySource) != 0 {
				t.Fatalf("registry retained memberships: active=%d sources=%d", len(registry.active), len(registry.bySource))
			}
		})
	}
}

func TestNewServerPerSourceConnectionOptions(t *testing.T) {
	makeOptions := func() *ServerOptions {
		return &ServerOptions{Listener: &stubListener{addr: testAddr("smtp")}, Backend: validBackend()}
	}
	t.Run("default", func(t *testing.T) {
		server, err := NewServer(makeOptions())
		if err != nil {
			t.Fatal(err)
		}
		if server.connections.maxSource != defaultMaxConnectionsPerSource {
			t.Fatalf("per-source default = %d, want %d", server.connections.maxSource, defaultMaxConnectionsPerSource)
		}
	})
	t.Run("negative", func(t *testing.T) {
		opts := makeOptions()
		opts.MaxConnectionsPerSource = -1
		_, err := NewServer(opts)
		if err == nil || !strings.Contains(err.Error(), "MaxConnectionsPerSource") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("explicit", func(t *testing.T) {
		opts := makeOptions()
		opts.MaxConnectionsPerSource = 7
		server, err := NewServer(opts)
		if err != nil {
			t.Fatal(err)
		}
		if server.connections.maxSource != 7 {
			t.Fatalf("per-source limit = %d", server.connections.maxSource)
		}
	})
	t.Run("above global remains valid", func(t *testing.T) {
		opts := makeOptions()
		opts.MaxConnections = 2
		opts.MaxConnectionsPerSource = 10
		server, err := NewServer(opts)
		if err != nil {
			t.Fatal(err)
		}
		if server.connections.max != 2 || server.connections.maxSource != 10 {
			t.Fatalf("limits = global %d per-source %d", server.connections.max, server.connections.maxSource)
		}
	})
}

func TestServerRejectsPerSourceOverflowBeforeGreeting(t *testing.T) {
	listener := newSourceLimitListener()
	var newSessions atomic.Int32
	backend := validBackend()
	newSession := backend.NewSession
	backend.NewSession = func(ctx context.Context, info *ConnInfo, opts *NewSessionOptions) (*Session, error) {
		newSessions.Add(1)
		return newSession(ctx, info, opts)
	}
	server, err := NewServer(&ServerOptions{
		Listener:                listener,
		Backend:                 backend,
		GreetingIdentity:        "server.example",
		MaxConnections:          10,
		MaxConnectionsPerSource: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, nil) }()
	t.Cleanup(func() {
		cancel()
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("Serve did not stop")
		}
	})

	source := net.ParseIP("192.0.2.1")
	first := listener.dial(t, &net.TCPAddr{IP: source, Port: 25001})
	firstReader := bufio.NewReader(first)
	if code := readTestReply(t, firstReader); code != 220 {
		t.Fatalf("first greeting = %d", code)
	}

	overflow := listener.dial(t, &net.TCPAddr{IP: source, Port: 25002})
	overflowReader := bufio.NewReader(overflow)
	line, err := overflowReader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(line, "421 4.3.2 Too many connections") {
		t.Fatalf("overflow reply = %q", line)
	}
	if _, err := overflowReader.ReadByte(); err == nil {
		t.Fatal("overflow connection remained open")
	}
	_ = overflow.Close()
	if got := newSessions.Load(); got != 1 {
		t.Fatalf("NewSession calls = %d, want 1", got)
	}

	writeTestCommand(t, first, "QUIT\r\n")
	if code := readTestReply(t, firstReader); code != 221 {
		t.Fatalf("QUIT reply = %d", code)
	}
	_ = first.SetReadDeadline(time.Now().Add(time.Second))
	_, _ = firstReader.ReadByte()
	_ = first.Close()
	waitForSourceRelease(t, server.connections, connectionSourceForAddr(&net.TCPAddr{IP: source, Port: 25003}))

	readmitted := listener.dial(t, &net.TCPAddr{IP: source, Port: 25003})
	if code := readTestReply(t, bufio.NewReader(readmitted)); code != 220 {
		t.Fatalf("readmitted greeting = %d", code)
	}
	_ = readmitted.Close()
}

func waitForSourceRelease(t *testing.T, registry *connectionRegistry, source connectionSource) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		registry.mu.Lock()
		count := registry.bySource[source]
		registry.mu.Unlock()
		if count == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("source remained registered with count %d", count)
		}
		runtime.Gosched()
	}
}

func registerTestConnection(t *testing.T, registry *connectionRegistry, source connectionSource) net.Conn {
	t.Helper()
	server, client := net.Pipe()
	t.Cleanup(func() {
		_ = server.Close()
		_ = client.Close()
	})
	if !registry.register(server, func() {}, source) {
		t.Fatal("connection registration refused")
	}
	return server
}

type fixedAddr struct {
	network string
	address string
}

func (a fixedAddr) Network() string { return a.network }
func (a fixedAddr) String() string  { return a.address }

type remoteAddrConn struct {
	net.Conn
	remote net.Addr
}

func (c *remoteAddrConn) RemoteAddr() net.Addr { return c.remote }

type sourceLimitListener struct {
	incoming chan net.Conn
	closed   chan struct{}
	once     sync.Once
}

func newSourceLimitListener() *sourceLimitListener {
	return &sourceLimitListener{incoming: make(chan net.Conn, 8), closed: make(chan struct{})}
}

func (l *sourceLimitListener) dial(t *testing.T, remote net.Addr) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	if err := client.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	select {
	case l.incoming <- &remoteAddrConn{Conn: server, remote: remote}:
		return client
	case <-l.closed:
		_ = client.Close()
		_ = server.Close()
		t.Fatal("dial after listener close")
		return nil
	}
}

func (l *sourceLimitListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.incoming:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *sourceLimitListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *sourceLimitListener) Addr() net.Addr { return fixedAddr{network: "test", address: "listener"} }

var _ net.Listener = (*sourceLimitListener)(nil)
