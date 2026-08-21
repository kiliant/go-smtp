package smtpserver

import (
	"testing"
)

func TestBDATInvalidNextCommandPoisonsTransactionAndPreservesFraming(t *testing.T) {
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

	writeTestCommand(t, harness.conn, "BDAT 3\r\nabc!BAD\r\n")
	wantSecurityReplies(t, harness, 250, 500)
	waitForResetReason(t, backend, ResetFailed)
	if reasons := backend.resetReasonSnapshot(); len(reasons) != 1 || reasons[0] != ResetFailed {
		t.Fatalf("Reset reasons = %#v, want ResetFailed", reasons)
	}
	assertNoSpoolFiles(t, spoolDir)

	writeTestCommand(t, harness.conn,
		"BDAT 2\r\nxy"+
			"BDAT 1 LAST\r\nz"+
			"RSET\r\n"+
			"MAIL FROM:<next@example.test>\r\n"+
			"RCPT TO:<next-recipient@example.test>\r\n"+
			"BDAT 0 LAST\r\n"+
			"NOOP\r\n")
	wantSecurityReplies(t, harness, 503, 503, 250, 250, 250, 250, 250)
	if calls := backend.dataCallCount(); calls != 1 {
		t.Fatalf("Session.Data calls = %d, want 1 after the post-RSET transaction", calls)
	}
	if reasons := backend.resetReasonSnapshot(); len(reasons) != 2 || reasons[0] != ResetFailed || reasons[1] != ResetCompleted {
		t.Fatalf("Reset reasons = %#v, want ResetFailed then ResetCompleted", reasons)
	}
}

func TestMalformedCommandWithoutActiveBDATKeepsConnectionFailureBehavior(t *testing.T) {
	harness := newRawTestServer(t, ModeSMTP, newCommandTestBackend(ModeSMTP).backend(), enableTestChunking)
	harness.wantCommand("EHLO client.example", 250)
	writeTestCommand(t, harness.conn, "!BAD\r\n")
	if err := harness.readUntilClose(); err == nil {
		t.Fatal("malformed command outside BDAT received a reply instead of closing the connection")
	}
}
