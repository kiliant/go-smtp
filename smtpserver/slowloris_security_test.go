package smtpserver

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

// TestSlowLorisEveryServerReadDeadline models a peer sending one byte and then
// waiting longer than the configured stage deadline. Millisecond test
// deadlines stand in for the attack's minute-scale inter-byte delay.
func TestSlowLorisEveryServerReadDeadline(t *testing.T) {
	t.Run("command line", func(t *testing.T) {
		runSlowLorisCase(t, slowLorisCase{
			configure: func(opts *ServerOptions) { opts.CommandTimeout = 30 * time.Millisecond },
			start:     func(t *testing.T, harness *rawTestServer) { writeTestCommand(t, harness.conn, "N") },
		})
	})

	t.Run("DATA content", func(t *testing.T) {
		runSlowLorisCase(t, slowLorisCase{
			configure: func(opts *ServerOptions) { opts.DataTimeout = 30 * time.Millisecond },
			start: func(t *testing.T, harness *rawTestServer) {
				beginDataSecurityTransaction(t, harness)
				harness.wantCommand("DATA", 354)
				writeTestCommand(t, harness.conn, "x")
			},
			wantReset: true,
		})
	})

	t.Run("BDAT content", func(t *testing.T) {
		runSlowLorisCase(t, slowLorisCase{
			configure: func(opts *ServerOptions) {
				enableTestChunking(opts)
				opts.SpoolDir = t.TempDir()
				opts.DataTimeout = 30 * time.Millisecond
			},
			start: func(t *testing.T, harness *rawTestServer) {
				beginDataSecurityTransaction(t, harness)
				writeTestCommand(t, harness.conn, "BDAT 5\r\nx")
			},
			wantReset: true,
		})
	})

	t.Run("SASL response", func(t *testing.T) {
		runSlowLorisCase(t, slowLorisCase{
			configure: func(opts *ServerOptions) {
				opts.CommandTimeout = 30 * time.Millisecond
				opts.AuthMechanismsBeforeTLS = []string{"PLAIN"}
			},
			start: func(t *testing.T, harness *rawTestServer) {
				harness.wantCommand("EHLO client.example", 250)
				harness.wantCommand("AUTH PLAIN", 334)
				writeTestCommand(t, harness.conn, "A")
			},
			authenticate: true,
		})
	})

	t.Run("STARTTLS handshake", func(t *testing.T) {
		serverTLS, _ := testTLSConfigs(t)
		runSlowLorisCase(t, slowLorisCase{
			configure: func(opts *ServerOptions) {
				opts.CommandTimeout = 30 * time.Millisecond
				opts.TLSConfig = serverTLS
			},
			start: func(t *testing.T, harness *rawTestServer) {
				harness.wantCommand("EHLO client.example", 250)
				harness.wantCommand("STARTTLS", 220)
				writeTestCommand(t, harness.conn, "\x16")
			},
		})
	})
}

type slowLorisCase struct {
	configure    func(*ServerOptions)
	start        func(*testing.T, *rawTestServer)
	wantReset    bool
	authenticate bool
}

func runSlowLorisCase(t *testing.T, test slowLorisCase) {
	t.Helper()
	baseline := stableGoroutineBaseline()
	state := newCommandTestBackend(ModeSMTP)
	var backend *Backend
	if test.authenticate {
		recorder := &recordingBackend{mode: ModeSMTP, authenticate: true}
		backend = recorder.backend()
	} else {
		backend = state.backend()
	}
	backend, closed := backendWithGenericCloseSignal(backend)
	logged := make(chan error, 8)
	harness := newRawTestServer(t, ModeSMTP, backend, func(opts *ServerOptions) {
		test.configure(opts)
		opts.ErrorLog = func(event ErrorEvent) { logged <- event.Err }
	})
	test.start(t, harness)
	if err := harness.conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.reader.ReadString('\n'); err == nil {
		t.Fatal("server replied instead of terminating the slow peer")
	} else {
		var timeout net.Error
		if errors.As(err, &timeout) && timeout.Timeout() {
			t.Fatalf("client guard deadline fired before server deadline: %v", err)
		}
	}
	waitForSessionClose(t, closed)
	if test.wantReset {
		waitForResetReason(t, state, ResetSessionEnd)
	}
	waitForLoggedTimeout(t, logged)
	harness.close()
	assertGoroutinesReturn(t, baseline, 4)
}

func backendWithGenericCloseSignal(backend *Backend) (*Backend, <-chan struct{}) {
	newSession := backend.NewSession
	closed := make(chan struct{})
	backend.NewSession = func(ctx context.Context, info *ConnInfo, opts *NewSessionOptions) (*Session, error) {
		session, err := newSession(ctx, info, opts)
		if err != nil {
			return nil, err
		}
		closeSession := session.Close
		var once sync.Once
		session.Close = func(ctx context.Context, opts *CloseOptions) {
			closeSession(ctx, opts)
			once.Do(func() { close(closed) })
		}
		return session, nil
	}
	return backend, closed
}

func waitForLoggedTimeout(t *testing.T, logged <-chan error) {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case err := <-logged:
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				return
			}
		case <-deadline:
			t.Fatal("server deadline timeout was not logged")
		}
	}
}
