package smtpserver

import (
	"bufio"
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/kiliant/go-smtp"
)

func TestConnectionSourceCapacityReleasedOnTeardownPaths(t *testing.T) {
	tests := []struct {
		name      string
		backend   func() *Backend
		configure func(*ServerOptions)
		interact  func(*testing.T, net.Conn, *bufio.Reader, context.CancelFunc, *Server)
		wantPanic bool
	}{
		{
			name:    "normal EOF",
			backend: validBackend,
			interact: func(t *testing.T, client net.Conn, reader *bufio.Reader, _ context.CancelFunc, _ *Server) {
				if code := readTestReply(t, reader); code != 220 {
					t.Fatalf("greeting = %d", code)
				}
				_ = client.Close()
			},
		},
		{
			name:    "QUIT",
			backend: validBackend,
			interact: func(t *testing.T, client net.Conn, reader *bufio.Reader, _ context.CancelFunc, _ *Server) {
				if code := readTestReply(t, reader); code != 220 {
					t.Fatalf("greeting = %d", code)
				}
				writeTestCommand(t, client, "QUIT\r\n")
				if code := readTestReply(t, reader); code != 221 {
					t.Fatalf("QUIT = %d", code)
				}
				_ = client.Close()
			},
		},
		{
			name:    "implicit TLS failure",
			backend: validBackend,
			configure: func(opts *ServerOptions) {
				serverTLS, _ := testTLSConfigs(t)
				opts.TLSConfig = serverTLS
				opts.ImplicitTLS = true
			},
			interact: func(t *testing.T, client net.Conn, _ *bufio.Reader, _ context.CancelFunc, _ *Server) {
				writeTestCommand(t, client, "not TLS\r\n")
				_ = client.Close()
			},
		},
		{
			name: "NewSession error",
			backend: func() *Backend {
				return &Backend{NewSession: func(context.Context, *ConnInfo, *NewSessionOptions) (*Session, error) {
					return nil, errors.New("session setup failed")
				}}
			},
			interact: func(t *testing.T, client net.Conn, reader *bufio.Reader, _ context.CancelFunc, _ *Server) {
				if code := readTestReply(t, reader); code != 421 {
					t.Fatalf("greeting failure = %d", code)
				}
				_ = client.Close()
			},
		},
		{
			name: "NewSession panic",
			backend: func() *Backend {
				return &Backend{NewSession: func(context.Context, *ConnInfo, *NewSessionOptions) (*Session, error) {
					panic("session setup panic")
				}}
			},
			interact:  func(*testing.T, net.Conn, *bufio.Reader, context.CancelFunc, *Server) {},
			wantPanic: true,
		},
		{
			name: "invalid session",
			backend: func() *Backend {
				return &Backend{NewSession: func(context.Context, *ConnInfo, *NewSessionOptions) (*Session, error) {
					return &Session{}, nil
				}}
			},
			interact: func(t *testing.T, client net.Conn, reader *bufio.Reader, _ context.CancelFunc, _ *Server) {
				if code := readTestReply(t, reader); code != 421 {
					t.Fatalf("invalid-session greeting = %d", code)
				}
				_ = client.Close()
			},
		},
		{
			name: "backend command panic",
			backend: func() *Backend {
				backend := validBackend()
				newSession := backend.NewSession
				backend.NewSession = func(ctx context.Context, info *ConnInfo, opts *NewSessionOptions) (*Session, error) {
					session, err := newSession(ctx, info, opts)
					session.Mail = func(context.Context, string, *smtp.MailOptions, *MailOptions) error {
						panic("mail panic")
					}
					return session, err
				}
				return backend
			},
			interact: func(t *testing.T, client net.Conn, reader *bufio.Reader, _ context.CancelFunc, _ *Server) {
				if code := readTestReply(t, reader); code != 220 {
					t.Fatalf("greeting = %d", code)
				}
				writeTestCommand(t, client, "EHLO client.example\r\n")
				if code := readTestReply(t, reader); code != 250 {
					t.Fatalf("EHLO = %d", code)
				}
				writeTestCommand(t, client, "MAIL FROM:<sender@example.test>\r\n")
			},
		},
		{
			name:    "context cancellation",
			backend: validBackend,
			interact: func(t *testing.T, client net.Conn, reader *bufio.Reader, cancel context.CancelFunc, _ *Server) {
				if code := readTestReply(t, reader); code != 220 {
					t.Fatalf("greeting = %d", code)
				}
				cancel()
				_ = client.Close()
			},
		},
		{
			name:    "forced transport close",
			backend: validBackend,
			interact: func(t *testing.T, _ net.Conn, reader *bufio.Reader, _ context.CancelFunc, server *Server) {
				if code := readTestReply(t, reader); code != 220 {
					t.Fatalf("greeting = %d", code)
				}
				server.connections.closeAll()
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts := &ServerOptions{
				Listener:                &stubListener{addr: testAddr("smtp")},
				Backend:                 test.backend(),
				GreetingIdentity:        "server.example",
				MaxConnections:          10,
				MaxConnectionsPerSource: 1,
			}
			if test.configure != nil {
				test.configure(opts)
			}
			server, err := NewServer(opts)
			if err != nil {
				t.Fatal(err)
			}

			client, raw := net.Pipe()
			if err := client.SetDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			remote := &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 25001}
			conn := &remoteAddrConn{Conn: raw, remote: remote}
			source := connectionSourceForAddr(conn.RemoteAddr())
			ctx, cancel := context.WithCancel(context.Background())
			if !server.connections.register(conn, cancel, source) {
				t.Fatal("initial registration refused")
			}
			done := make(chan any, 1)
			go func() {
				var recovered any
				func() {
					defer func() { recovered = recover() }()
					server.serveConnection(ctx, cancel, conn)
				}()
				done <- recovered
			}()

			test.interact(t, client, bufio.NewReader(client), cancel, server)
			select {
			case recovered := <-done:
				if (recovered != nil) != test.wantPanic {
					t.Fatalf("panic = %v, want panic %v", recovered, test.wantPanic)
				}
			case <-time.After(time.Second):
				t.Fatal("connection handler did not exit")
			}
			cancel()
			_ = client.Close()

			server.connections.mu.Lock()
			active, sources := len(server.connections.active), len(server.connections.bySource)
			server.connections.mu.Unlock()
			if active != 0 || sources != 0 {
				t.Fatalf("released registry = active %d sources %d", active, sources)
			}

			readmitServer, readmitClient := net.Pipe()
			if !server.connections.register(readmitServer, func() {}, source) {
				t.Fatal("source was not readmitted after teardown")
			}
			server.connections.unregister(readmitServer)
			_ = readmitServer.Close()
			_ = readmitClient.Close()
		})
	}
}
