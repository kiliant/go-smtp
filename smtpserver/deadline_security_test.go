package smtpserver

import (
	"context"
	"errors"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestContentDisconnectsAndDeadlineTerminateWithoutGoroutineLeak(t *testing.T) {
	t.Run("disconnect after 354 without content", func(t *testing.T) {
		baseline := stableGoroutineBaseline()
		backend := newCommandTestBackend(ModeSMTP)
		wrapped, closed := backendWithCloseSignal(backend)
		harness := newRawTestServer(t, ModeSMTP, wrapped, nil)
		beginDataSecurityTransaction(t, harness)
		harness.wantCommand("DATA", 354)
		if err := harness.conn.Close(); err != nil {
			t.Fatal(err)
		}
		waitForSessionClose(t, closed)
		waitForResetReason(t, backend, ResetSessionEnd)
		harness.close()
		assertGoroutinesReturn(t, baseline, 4)
	})

	t.Run("disconnect mid-DATA", func(t *testing.T) {
		baseline := stableGoroutineBaseline()
		backend := newCommandTestBackend(ModeSMTP)
		wrapped, closed := backendWithCloseSignal(backend)
		harness := newRawTestServer(t, ModeSMTP, wrapped, func(opts *ServerOptions) {
			opts.DataTimeout = 100 * time.Millisecond
		})
		beginDataSecurityTransaction(t, harness)
		harness.wantCommand("DATA", 354)
		writeTestCommand(t, harness.conn, "partial content")
		if err := harness.conn.Close(); err != nil {
			t.Fatal(err)
		}
		waitForSessionClose(t, closed)
		waitForResetReason(t, backend, ResetSessionEnd)
		harness.close()
		assertGoroutinesReturn(t, baseline, 4)
	})

	t.Run("disconnect mid-BDAT", func(t *testing.T) {
		baseline := stableGoroutineBaseline()
		backend := newCommandTestBackend(ModeSMTP)
		wrapped, closed := backendWithCloseSignal(backend)
		harness := newRawTestServer(t, ModeSMTP, wrapped, func(opts *ServerOptions) {
			enableTestChunking(opts)
			opts.SpoolDir = t.TempDir()
			opts.DataTimeout = 100 * time.Millisecond
		})
		beginDataSecurityTransaction(t, harness)
		writeTestCommand(t, harness.conn, "BDAT 5\r\nab")
		if err := harness.conn.Close(); err != nil {
			t.Fatal(err)
		}
		waitForSessionClose(t, closed)
		waitForResetReason(t, backend, ResetSessionEnd)
		harness.close()
		assertGoroutinesReturn(t, baseline, 4)
	})

	t.Run("silence after 354", func(t *testing.T) {
		baseline := stableGoroutineBaseline()
		backend := newCommandTestBackend(ModeSMTP)
		wrapped, closed := backendWithCloseSignal(backend)
		logged := make(chan error, 4)
		harness := newRawTestServer(t, ModeSMTP, wrapped, func(opts *ServerOptions) {
			opts.DataTimeout = 30 * time.Millisecond
			opts.ErrorLog = func(event ErrorEvent) { logged <- event.Err }
		})
		beginDataSecurityTransaction(t, harness)
		harness.wantCommand("DATA", 354)
		if err := harness.readUntilClose(); err == nil {
			t.Fatal("server replied instead of terminating after DATA timeout")
		}
		waitForSessionClose(t, closed)
		waitForResetReason(t, backend, ResetSessionEnd)
		select {
		case err := <-logged:
			var timeout net.Error
			if !errors.As(err, &timeout) || !timeout.Timeout() {
				t.Fatalf("logged error = %v, want data timeout", err)
			}
		case <-time.After(time.Second):
			t.Fatal("DATA timeout was not logged")
		}
		harness.close()
		assertGoroutinesReturn(t, baseline, 4)
	})
}

func beginDataSecurityTransaction(t *testing.T, harness *rawTestServer) {
	t.Helper()
	harness.wantCommand("EHLO client.example", 250)
	harness.wantCommand("MAIL FROM:<sender@example.test>", 250)
	harness.wantCommand("RCPT TO:<recipient@example.test>", 250)
}

func backendWithCloseSignal(backend *commandTestBackend) (*Backend, <-chan struct{}) {
	wrapped := backend.backend()
	newSession := wrapped.NewSession
	closed := make(chan struct{})
	var once sync.Once
	wrapped.NewSession = func(ctx context.Context, info *ConnInfo, opts *NewSessionOptions) (*Session, error) {
		session, err := newSession(ctx, info, opts)
		if err != nil {
			return nil, err
		}
		closeSession := session.Close
		session.Close = func(ctx context.Context, opts *CloseOptions) {
			closeSession(ctx, opts)
			once.Do(func() { close(closed) })
		}
		return session, nil
	}
	return wrapped, closed
}

func waitForSessionClose(t *testing.T, closed <-chan struct{}) {
	t.Helper()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("backend session did not close")
	}
}

func stableGoroutineBaseline() int {
	runtime.GC()
	runtime.Gosched()
	return runtime.NumGoroutine()
}

func assertGoroutinesReturn(t *testing.T, baseline, tolerance int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if got := runtime.NumGoroutine(); got <= baseline+tolerance {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("goroutines = %d, baseline = %d, tolerance = %d", runtime.NumGoroutine(), baseline, tolerance)
}
