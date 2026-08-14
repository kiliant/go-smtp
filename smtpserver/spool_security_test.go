package smtpserver

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/kiliant/go-smtp"
)

func TestAggregateSpoolExhaustionConsumesChunkAndReturns452(t *testing.T) {
	baseline := stableGoroutineBaseline()
	spoolDir := t.TempDir()
	listener := newQueuedListener()
	server, err := NewServer(&ServerOptions{
		Listener:                 listener,
		Backend:                  newCommandTestBackend(ModeSMTP).backend(),
		GreetingIdentity:         "server.example",
		EnableCHUNKING:           true,
		MaxSpoolBytes:            8,
		MaxSpoolMemoryBytes:      1,
		MaxTotalSpoolBytes:       3,
		MaxTotalSpoolMemoryBytes: 2,
		MaxConcurrentSpools:      2,
		MaxMessageBytes:          8,
		SpoolDir:                 spoolDir,
		CommandTimeout:           time.Second,
		DataTimeout:              time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, nil) }()

	first, firstReader := beginQueuedChunkTransaction(t, listener)
	writeTestCommand(t, first, "BDAT 2\r\nab")
	if code, _ := readTestReplyDetails(t, firstReader); code != 250 {
		t.Fatalf("first BDAT = %d, want 250", code)
	}
	second, secondReader := beginQueuedChunkTransaction(t, listener)
	writeTestCommand(t, second, "BDAT 2\r\ncdNOOP\r\n")
	if code, text := readTestReplyDetails(t, secondReader); code != 452 || !strings.Contains(text, "4.3.1") {
		t.Fatalf("aggregate exhaustion reply = %d %q, want 452 4.3.1", code, text)
	}
	wantQueuedReplies(t, secondReader, 250)

	_ = first.Close()
	_ = second.Close()
	shutdownCtx, stop := context.WithTimeout(context.Background(), time.Second)
	if err := server.Shutdown(shutdownCtx, nil); err != nil {
		t.Fatal(err)
	}
	stop()
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not stop")
	}
	assertNoSpoolFiles(t, spoolDir)
	assertGoroutinesReturn(t, baseline, 4)
}

func TestBackendPanicReleasesSpoolFileAndSession(t *testing.T) {
	baseline := stableGoroutineBaseline()
	spoolDir := t.TempDir()
	state := newCommandTestBackend(ModeSMTP)
	backend, closed := backendWithCloseSignal(state)
	newSession := backend.NewSession
	backend.NewSession = func(ctx context.Context, info *ConnInfo, opts *NewSessionOptions) (*Session, error) {
		session, err := newSession(ctx, info, opts)
		if err != nil {
			return nil, err
		}
		session.Data = func(context.Context, io.Reader, *DataOptions) (smtp.DataResult, error) {
			panic("T22 backend panic")
		}
		return session, nil
	}
	logged := make(chan error, 4)
	harness := newRawTestServer(t, ModeSMTP, backend, func(opts *ServerOptions) {
		enableTestChunking(opts)
		opts.SpoolDir = spoolDir
		opts.MaxSpoolMemoryBytes = 1
		opts.ErrorLog = func(event ErrorEvent) { logged <- event.Err }
	})
	beginDataSecurityTransaction(t, harness)
	writeTestCommand(t, harness.conn, "BDAT 2 LAST\r\nab")
	if err := harness.conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.reader.ReadString('\n'); err == nil {
		t.Fatal("server replied after backend panic")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatalf("server hung after backend panic: %v", err)
	}
	waitForSessionClose(t, closed)
	waitForResetReason(t, state, ResetSessionEnd)
	select {
	case err := <-logged:
		if !strings.Contains(err.Error(), "backend or command panic: T22 backend panic") {
			t.Fatalf("logged error = %v, want backend panic", err)
		}
	case <-time.After(time.Second):
		t.Fatal("backend panic was not logged")
	}
	assertNoSpoolFiles(t, spoolDir)
	harness.close()
	assertGoroutinesReturn(t, baseline, 4)
}

func beginQueuedChunkTransaction(t *testing.T, listener *queuedListener) (net.Conn, *bufio.Reader) {
	t.Helper()
	conn := listener.dial(t)
	reader := bufio.NewReader(conn)
	wantQueuedReplies(t, reader, 220)
	writeTestCommand(t, conn,
		"EHLO client.example\r\n"+
			"MAIL FROM:<sender@example.test>\r\n"+
			"RCPT TO:<recipient@example.test>\r\n")
	wantQueuedReplies(t, reader, 250, 250, 250)
	return conn, reader
}

func wantQueuedReplies(t *testing.T, reader *bufio.Reader, want ...int) {
	t.Helper()
	for i, expected := range want {
		if code, _ := readTestReplyDetails(t, reader); code != expected {
			t.Fatalf("reply %d = %d, want %d", i, code, expected)
		}
	}
}
