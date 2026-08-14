package smtpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/kiliant/go-smtp"
)

func TestPipeliningSecuritySynchronizationGroups(t *testing.T) {
	t.Run("DATA followed immediately by content", func(t *testing.T) {
		baseline := stableGoroutineBaseline()
		backend := newCommandTestBackend(ModeSMTP)
		harness := newRawTestServer(t, ModeSMTP, backend.backend(), nil)
		writeTestCommand(t, harness.conn,
			"EHLO client.example\r\n"+
				"MAIL FROM:<sender@example.test>\r\n"+
				"RCPT TO:<recipient@example.test>\r\n"+
				"DATA\r\n"+
				"body\r\n.\r\n"+
				"NOOP\r\n")
		wantSecurityReplies(t, harness, 250, 250, 250, 354, 250, 250)
		if message := backend.message(); !strings.HasSuffix(message, "body\r\n") {
			t.Fatalf("backend message = %q, want DATA content suffix", message)
		}
		harness.close()
		assertGoroutinesReturn(t, baseline, 4)
	})

	t.Run("pipeline spanning RSET", func(t *testing.T) {
		baseline := stableGoroutineBaseline()
		backend := newCommandTestBackend(ModeSMTP)
		harness := newRawTestServer(t, ModeSMTP, backend.backend(), nil)
		harness.wantCommand("EHLO client.example", 250)
		writeTestCommand(t, harness.conn,
			"MAIL FROM:<first@example.test>\r\n"+
				"RCPT TO:<discarded@example.test>\r\n"+
				"RSET\r\n"+
				"MAIL FROM:<second@example.test>\r\n"+
				"RCPT TO:<kept@example.test>\r\n"+
				"DATA\r\n"+
				"second\r\n.\r\n")
		wantSecurityReplies(t, harness, 250, 250, 250, 250, 250, 354, 250)
		if reasons := backend.resetReasonSnapshot(); len(reasons) < 2 || reasons[0] != ResetExplicit || reasons[1] != ResetCompleted {
			t.Fatalf("Reset reasons = %#v, want ResetExplicit then ResetCompleted", reasons)
		}
		harness.close()
		assertGoroutinesReturn(t, baseline, 4)
	})

	t.Run("pipeline spanning rejected RCPT", func(t *testing.T) {
		baseline := stableGoroutineBaseline()
		backend := newCommandTestBackend(ModeSMTP)
		wrapped := backend.backend()
		newSession := wrapped.NewSession
		wrapped.NewSession = func(ctx context.Context, info *ConnInfo, opts *NewSessionOptions) (*Session, error) {
			session, err := newSession(ctx, info, opts)
			if err != nil {
				return nil, err
			}
			rcpt := session.Rcpt
			session.Rcpt = func(ctx context.Context, recipient string, params *smtp.RcptOptions, opts *RcptOptions) error {
				if strings.Contains(recipient, "rejected") {
					return &smtp.Error{Code: 550, Enhanced: smtp.ParseEnhancedCode("5.1.1"), Text: "No such user"}
				}
				return rcpt(ctx, recipient, params, opts)
			}
			return session, nil
		}
		harness := newRawTestServer(t, ModeSMTP, wrapped, nil)
		harness.wantCommand("EHLO client.example", 250)
		writeTestCommand(t, harness.conn,
			"MAIL FROM:<sender@example.test>\r\n"+
				"RCPT TO:<accepted@example.test>\r\n"+
				"RCPT TO:<rejected@example.test>\r\n"+
				"DATA\r\n"+
				"body\r\n.\r\n"+
				"NOOP\r\n")
		wantSecurityReplies(t, harness, 250, 250, 550, 354, 250, 250)
		if calls := backend.dataCallCount(); calls != 1 {
			t.Fatalf("Session.Data calls = %d, want 1", calls)
		}
		harness.close()
		assertGoroutinesReturn(t, baseline, 4)
	})
}

func wantSecurityReplies(t *testing.T, harness *rawTestServer, want ...int) {
	t.Helper()
	for i, expected := range want {
		if code, _ := readTestReplyDetails(t, harness.reader); code != expected {
			t.Fatalf("reply %d = %d, want %d", i, code, expected)
		}
	}
}
