package smtpserver

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"net"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestMaximumRecipientsRejectsNextAndKeepsTransactionSynchronized(t *testing.T) {
	baseline := stableGoroutineBaseline()
	backend := &recordingBackend{mode: ModeSMTP}
	harness := newRawTestServer(t, ModeSMTP, backend.backend(), func(opts *ServerOptions) {
		opts.MaxRecipients = 100
	})
	harness.wantCommand("EHLO client.example", 250)
	harness.wantCommand("MAIL FROM:<sender@example.test>", 250)
	for i := 0; i < 100; i++ {
		harness.wantCommand("RCPT TO:<recipient@example.test>", 250)
	}
	harness.wantCommand("RCPT TO:<overflow@example.test>", 452)
	if recipients := backend.recipientSnapshot(); len(recipients) != 100 {
		t.Fatalf("accepted recipients = %d, want 100", len(recipients))
	}
	harness.wantCommand("DATA", 354)
	writeTestCommand(t, harness.conn, "body\r\n.\r\n")
	if code, _ := readTestReplyDetails(t, harness.reader); code != 250 {
		t.Fatalf("DATA after recipient limit = %d, want 250", code)
	}
	harness.close()
	assertGoroutinesReturn(t, baseline, 4)
}

func TestAUTHStatefulSecuritySequences(t *testing.T) {
	t.Run("repeated failure then success and AUTH after AUTH", func(t *testing.T) {
		baseline := stableGoroutineBaseline()
		backend := &recordingBackend{mode: ModeSMTP, authenticate: true}
		harness := newRawTestServer(t, ModeSMTP, backend.backend(), func(opts *ServerOptions) {
			opts.AuthMechanismsBeforeTLS = []string{"PLAIN"}
		})
		harness.wantCommand("EHLO client.example", 250)
		bad := plainInitialResponse("user", "wrong")
		for i := 0; i < 3; i++ {
			harness.wantCommand("AUTH PLAIN "+bad, 535)
		}
		harness.wantCommand("AUTH PLAIN "+plainInitialResponse("user", "secret"), 235)
		harness.wantCommand("AUTH PLAIN "+plainInitialResponse("user", "secret"), 503)
		backend.mu.Lock()
		commits := backend.authCommits
		backend.mu.Unlock()
		if commits != 1 {
			t.Fatalf("CommitAuth calls = %d, want 1", commits)
		}
		harness.close()
		assertGoroutinesReturn(t, baseline, 4)
	})

	t.Run("AUTH during open transaction", func(t *testing.T) {
		baseline := stableGoroutineBaseline()
		backend := &recordingBackend{mode: ModeSMTP, authenticate: true}
		harness := newRawTestServer(t, ModeSMTP, backend.backend(), func(opts *ServerOptions) {
			opts.AuthMechanismsBeforeTLS = []string{"PLAIN"}
		})
		harness.wantCommand("EHLO client.example", 250)
		harness.wantCommand("MAIL FROM:<sender@example.test>", 250)
		harness.wantCommand("AUTH PLAIN "+plainInitialResponse("user", "secret"), 503)
		harness.wantCommand("RCPT TO:<recipient@example.test>", 250)
		backend.mu.Lock()
		commits := backend.authCommits
		backend.mu.Unlock()
		if commits != 0 {
			t.Fatalf("CommitAuth calls = %d, want 0", commits)
		}
		harness.close()
		assertGoroutinesReturn(t, baseline, 4)
	})
}

func plainInitialResponse(authenticationID, password string) string {
	return base64.StdEncoding.EncodeToString([]byte("\x00" + authenticationID + "\x00" + password))
}

func TestPublicServerConnectionLimitAndRecovery(t *testing.T) {
	baseline := stableGoroutineBaseline()
	listener := newQueuedListener()
	server, err := NewServer(&ServerOptions{
		Listener:         listener,
		Backend:          newCommandTestBackend(ModeSMTP).backend(),
		GreetingIdentity: "server.example",
		MaxConnections:   1,
		CommandTimeout:   time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx, nil) }()

	first := listener.dial(t)
	if code, _ := readTestReplyDetails(t, bufio.NewReader(first)); code != 220 {
		t.Fatalf("first greeting = %d, want 220", code)
	}
	overflow := listener.dial(t)
	if code, _ := readTestReplyDetails(t, bufio.NewReader(overflow)); code != 421 {
		t.Fatalf("overflow greeting = %d, want 421", code)
	}
	_ = overflow.Close()
	_ = first.Close()

	var recovered net.Conn
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		candidate := listener.dial(t)
		code, _ := readTestReplyDetails(t, bufio.NewReader(candidate))
		if code == 220 {
			recovered = candidate
			break
		}
		if code != 421 {
			t.Fatalf("recovery greeting = %d, want 220 or transient 421", code)
		}
		_ = candidate.Close()
		time.Sleep(time.Millisecond)
	}
	if recovered == nil {
		t.Fatal("connection capacity did not recover after first peer closed")
	}
	_ = recovered.Close()
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not stop")
	}
	assertGoroutinesReturn(t, baseline, 4)
}

func TestSpillTransactionsDoNotLeakFileDescriptors(t *testing.T) {
	goroutineBaseline := stableGoroutineBaseline()
	baseline, ok := fileDescriptorCount()
	if !ok {
		t.Skip("platform does not expose a readable /dev/fd")
	}
	spoolDir := t.TempDir()
	backend := newCommandTestBackend(ModeSMTP)
	harness := newRawTestServer(t, ModeSMTP, backend.backend(), func(opts *ServerOptions) {
		enableTestChunking(opts)
		opts.SpoolDir = spoolDir
		opts.MaxSpoolMemoryBytes = 1
	})
	harness.wantCommand("EHLO client.example", 250)
	for i := 0; i < 16; i++ {
		harness.wantCommand("MAIL FROM:<sender@example.test>", 250)
		harness.wantCommand("RCPT TO:<recipient@example.test>", 250)
		writeTestCommand(t, harness.conn, "BDAT 2 LAST\r\nab")
		if code, _ := readTestReplyDetails(t, harness.reader); code != 250 {
			t.Fatalf("transaction %d BDAT = %d, want 250", i, code)
		}
		assertNoSpoolFiles(t, spoolDir)
	}
	harness.close()
	assertGoroutinesReturn(t, goroutineBaseline, 4)

	// Account for one descriptor of runtime/test harness noise while still
	// making a leaked spool descriptor visible after a repeated spill loop.
	const tolerance = 1
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		runtime.GC()
		if got, ok := fileDescriptorCount(); ok && got <= baseline+tolerance {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	got, _ := fileDescriptorCount()
	t.Fatalf("file descriptors = %d, baseline = %d, tolerance = %d", got, baseline, tolerance)
}

func fileDescriptorCount() (int, bool) {
	entries, err := filepath.Glob("/dev/fd/[0-9]*")
	if err != nil || len(entries) == 0 {
		return 0, false
	}
	return len(entries), true
}

type queuedListener struct {
	incoming chan net.Conn
	closed   chan struct{}
	once     sync.Once
}

func newQueuedListener() *queuedListener {
	return &queuedListener{incoming: make(chan net.Conn, 16), closed: make(chan struct{})}
}

func (l *queuedListener) dial(t *testing.T) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	if err := client.SetDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	select {
	case l.incoming <- server:
		return client
	case <-l.closed:
		_ = client.Close()
		_ = server.Close()
		t.Fatal("dial after listener close")
		return nil
	}
}

func (l *queuedListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.incoming:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *queuedListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *queuedListener) Addr() net.Addr { return testAddr("smtp-security") }

var _ net.Listener = (*queuedListener)(nil)
