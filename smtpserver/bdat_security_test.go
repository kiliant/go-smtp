package smtpserver

import (
	"testing"
)

func TestBDATBoundarySecurityCases(t *testing.T) {
	baseline := stableGoroutineBaseline()
	defer assertGoroutinesReturn(t, baseline, 4)
	t.Run("zero LAST without transaction", func(t *testing.T) {
		harness := newRawTestServer(t, ModeSMTP, newCommandTestBackend(ModeSMTP).backend(), enableTestChunking)
		harness.wantCommand("EHLO client.example", 250)
		writeTestCommand(t, harness.conn, "BDAT 0 LAST\r\nNOOP\r\n")
		wantSecurityReplies(t, harness, 503, 250)
	})

	t.Run("before MAIL consumes exact chunk", func(t *testing.T) {
		harness := newRawTestServer(t, ModeSMTP, newCommandTestBackend(ModeSMTP).backend(), enableTestChunking)
		harness.wantCommand("EHLO client.example", 250)
		writeTestCommand(t, harness.conn, "BDAT 3\r\nabcNOOP\r\n")
		wantSecurityReplies(t, harness, 503, 250)
	})

	t.Run("after completed DATA", func(t *testing.T) {
		harness := newRawTestServer(t, ModeSMTP, newCommandTestBackend(ModeSMTP).backend(), enableTestChunking)
		beginDataSecurityTransaction(t, harness)
		harness.wantCommand("DATA", 354)
		writeTestCommand(t, harness.conn, "body\r\n.\r\n")
		if code, _ := readTestReplyDetails(t, harness.reader); code != 250 {
			t.Fatalf("DATA reply = %d, want 250", code)
		}
		writeTestCommand(t, harness.conn, "BDAT 0 LAST\r\nNOOP\r\n")
		wantSecurityReplies(t, harness, 503, 250)
	})

	t.Run("announced size overflow", func(t *testing.T) {
		backend := newCommandTestBackend(ModeSMTP)
		harness := newRawTestServer(t, ModeSMTP, backend.backend(), enableTestChunking)
		beginDataSecurityTransaction(t, harness)
		writeTestCommand(t, harness.conn, "BDAT 18446744073709551615 LAST\r\n")
		if code, _ := readTestReplyDetails(t, harness.reader); code != 501 {
			t.Fatalf("overflow BDAT reply = %d, want 501", code)
		}
		if err := harness.readUntilClose(); err == nil {
			t.Fatal("connection remained open after unframeable BDAT size")
		}
		if calls := backend.dataCallCount(); calls != 0 {
			t.Fatalf("Session.Data calls = %d, want 0", calls)
		}
	})
}
