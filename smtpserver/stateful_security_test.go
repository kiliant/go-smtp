package smtpserver

import (
	"bufio"
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSTARTTLSInjectionIsLoggedDiscardedAndResetsSession(t *testing.T) {
	baseline := stableGoroutineBaseline()
	backend := newCommandTestBackend(ModeSMTP)
	serverTLS, clientTLS := testTLSConfigs(t)
	logged := make(chan error, 4)
	harness := newRawTestServer(t, ModeSMTP, backend.backend(), func(opts *ServerOptions) {
		opts.TLSConfig = serverTLS
		opts.ErrorLog = func(event ErrorEvent) { logged <- event.Err }
	})
	harness.wantCommand("EHLO cleartext.example", 250)

	const injected = "NOOP\r\n"
	writeTestCommand(t, harness.conn, "STARTTLS\r\n"+injected)
	if code, _ := readTestReplyDetails(t, harness.reader); code != 220 {
		t.Fatalf("STARTTLS reply = %d, want 220", code)
	}

	secure := tls.Client(harness.conn, clientTLS)
	if err := secure.Handshake(); err != nil {
		t.Fatal(err)
	}
	secureReader := bufio.NewReader(secure)
	writeTestCommand(t, secure, "MAIL FROM:<sender@example.test>\r\n")
	if code, _ := readTestReplyDetails(t, secureReader); code != 503 {
		t.Fatalf("MAIL before post-TLS EHLO = %d, want 503", code)
	}
	writeTestCommand(t, secure, "EHLO encrypted.example\r\n")
	if code, _ := readTestReplyDetails(t, secureReader); code != 250 {
		t.Fatalf("post-TLS EHLO = %d, want 250", code)
	}

	select {
	case err := <-logged:
		want := "discarded 6 plaintext bytes prefetched after STARTTLS"
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("logged error = %v, want %q", err, want)
		}
	case <-time.After(time.Second):
		t.Fatal("STARTTLS plaintext injection was not logged")
	}
	if reasons := backend.resetReasonSnapshot(); len(reasons) != 1 || reasons[0] != ResetStartTLS {
		t.Fatalf("Reset reasons = %#v, want ResetStartTLS", reasons)
	}
	harness.close()
	assertGoroutinesReturn(t, baseline, 4)
}

func TestFailedBDATPipelinedChunksEachConsumeAndReply(t *testing.T) {
	baseline := stableGoroutineBaseline()
	spoolDir := t.TempDir()
	backend := newCommandTestBackend(ModeSMTP)
	harness := newRawTestServer(t, ModeSMTP, backend.backend(), func(opts *ServerOptions) {
		enableTestChunking(opts)
		opts.SpoolDir = spoolDir
		opts.MaxMessageBytes = 3
		opts.MaxSpoolBytes = 3
		opts.MaxSpoolMemoryBytes = 1
	})
	harness.wantCommand("EHLO client.example", 250)
	harness.wantCommand("MAIL FROM:<sender@example.test>", 250)
	harness.wantCommand("RCPT TO:<recipient@example.test>", 250)

	writeTestCommand(t, harness.conn,
		"BDAT 2\r\nab"+
			"BDAT 2\r\ncd"+
			"BDAT 3\r\nxyz"+
			"BDAT 1 LAST\r\nq"+
			"NOOP\r\n")
	for i, want := range []int{250, 552, 503, 503, 250} {
		code, text := readTestReplyDetails(t, harness.reader)
		if code != want {
			t.Fatalf("reply %d = %d, want %d", i, code, want)
		}
		if i == 1 && !strings.Contains(text, "5.3.4") {
			t.Fatalf("oversize reply = %d %q, want enhanced 5.3.4", code, text)
		}
	}
	if calls := backend.dataCallCount(); calls != 0 {
		t.Fatalf("Session.Data calls = %d, want 0", calls)
	}
	assertNoSpoolFiles(t, spoolDir)
	harness.close()
	assertGoroutinesReturn(t, baseline, 4)
}

func TestSpoolFilesAbsentAfterFailureAndDisconnect(t *testing.T) {
	baseline := stableGoroutineBaseline()
	defer assertGoroutinesReturn(t, baseline, 4)
	t.Run("storage failure", func(t *testing.T) {
		parent := t.TempDir()
		notDirectory := filepath.Join(parent, "not-a-directory")
		if err := os.WriteFile(notDirectory, []byte("marker"), 0o600); err != nil {
			t.Fatal(err)
		}
		backend := newCommandTestBackend(ModeSMTP)
		harness := newRawTestServer(t, ModeSMTP, backend.backend(), func(opts *ServerOptions) {
			enableTestChunking(opts)
			opts.SpoolDir = notDirectory
			opts.MaxSpoolMemoryBytes = 1
		})
		harness.wantCommand("EHLO client.example", 250)
		harness.wantCommand("MAIL FROM:<sender@example.test>", 250)
		harness.wantCommand("RCPT TO:<recipient@example.test>", 250)
		writeTestCommand(t, harness.conn, "BDAT 2\r\nab")
		if code, text := readTestReplyDetails(t, harness.reader); code != 451 || !strings.Contains(text, "4.3.0") {
			t.Fatalf("storage-failure BDAT reply = %d %q, want 451 4.3.0", code, text)
		}
		assertNoSpoolFiles(t, parent)
	})

	t.Run("disconnect", func(t *testing.T) {
		spoolDir := t.TempDir()
		backend := newCommandTestBackend(ModeSMTP)
		harness := newRawTestServer(t, ModeSMTP, backend.backend(), func(opts *ServerOptions) {
			enableTestChunking(opts)
			opts.SpoolDir = spoolDir
			opts.MaxSpoolMemoryBytes = 1
		})
		harness.wantCommand("EHLO client.example", 250)
		harness.wantCommand("MAIL FROM:<sender@example.test>", 250)
		harness.wantCommand("RCPT TO:<recipient@example.test>", 250)
		writeTestCommand(t, harness.conn, "BDAT 2\r\nab")
		if code, _ := readTestReplyDetails(t, harness.reader); code != 250 {
			t.Fatalf("BDAT reply = %d, want 250", code)
		}
		if err := harness.conn.Close(); err != nil {
			t.Fatal(err)
		}
		waitForResetReason(t, backend, ResetSessionEnd)
		assertNoSpoolFiles(t, spoolDir)
	})
}

func waitForResetReason(t *testing.T, backend *commandTestBackend, want ResetReason) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, reason := range backend.resetReasonSnapshot() {
			if reason == want {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("Reset reasons = %#v, want %v", backend.resetReasonSnapshot(), want)
}

func assertNoSpoolFiles(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".go-smtp-spool-") {
			t.Fatalf("spool file remains after cleanup: %s", entry.Name())
		}
	}
}
