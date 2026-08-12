package smtpclient

import (
	"context"
	"strings"
	"testing"

	smtp "github.com/kiliant/go-smtp"
)

// TestRcptBatchAppliesSendOptionsPerRecipient is what makes T16's item-2 design
// claim load-bearing. AllowUnadvertisedParameters moved to Recipient.Send rather
// than to RcptBatchOptions specifically so that policy can differ between two
// recipients of one batch — and nothing else in the suite exercises that,
// because every other path reaches it through single-recipient Rcpt. Without
// this test, refactoring the flag to a batch-level field would keep the whole
// suite green while quietly removing the granularity the split was chosen for.
func TestRcptBatchAppliesSendOptionsPerRecipient(t *testing.T) {
	notify := &smtp.RcptOptions{Extra: []smtp.Param{{Keyword: "NOTIFY", Value: "NEVER"}}}

	// The opted-out recipient's parameter reaches the wire; the recipient
	// without the opt-out is not dragged along by it and needs no parameter of
	// its own. DSN is deliberately unadvertised by the fake EHLO.
	t.Run("opt-out applies only where set", func(t *testing.T) {
		raw, done := startFakeServer(t, []fakeStep{
			{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
			{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 ok\r\n")},
			{command: "RCPT TO:<opted@example.test> NOTIFY=NEVER", replies: fakeReplies("250 ok\r\n")},
			{command: "RCPT TO:<plain@example.test>", replies: fakeReplies("250 ok\r\n")},
		}, nil)
		defer done()
		c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Mail(context.Background(), "sender@example.test", nil, nil); err != nil {
			t.Fatal(err)
		}
		result, err := c.RcptBatch(context.Background(), []Recipient{
			{Address: "opted@example.test", Options: notify, Send: &RcptSendOptions{AllowUnadvertisedParameters: true}},
			{Address: "plain@example.test"},
		}, nil)
		if err != nil {
			t.Fatalf("RcptBatch = %v, want the opted-out recipient's unadvertised parameter accepted", err)
		}
		if !result.AllAccepted() {
			t.Fatalf("RcptBatch result = %#v, want both recipients accepted", result)
		}
	})

	// The mirror image, and the half that actually discriminates: one recipient
	// opting out must not license the same parameter on a recipient that did
	// not. The fake server scripts no RCPT step, so a command written anyway
	// fails the match rather than passing silently.
	t.Run("one recipient's opt-out does not license another's", func(t *testing.T) {
		raw, done := startFakeServer(t, []fakeStep{
			{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
			{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 ok\r\n")},
		}, nil)
		defer done()
		c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
		if err != nil {
			t.Fatal(err)
		}
		if err := c.Mail(context.Background(), "sender@example.test", nil, nil); err != nil {
			t.Fatal(err)
		}
		_, err = c.RcptBatch(context.Background(), []Recipient{
			{Address: "opted@example.test", Options: notify, Send: &RcptSendOptions{AllowUnadvertisedParameters: true}},
			{Address: "strict@example.test", Options: notify},
		}, nil)
		if err == nil {
			t.Fatal("RcptBatch = nil, want a local validation error for the recipient without the opt-out")
		}
		if !strings.Contains(err.Error(), "did not advertise extension") || !strings.Contains(err.Error(), "recipient 1") {
			t.Fatalf("RcptBatch error = %v, want it to name recipient 1 and the unadvertised extension", err)
		}
	})
}

// TestMailSendOptionsAreIndependentOfRcpt pins the other half of the split: the
// MAIL-level opt-out does not leak into RCPT validation, and vice versa. The two
// structs exist separately for exactly this reason, and a single shared flag
// would pass every other test in the suite.
func TestMailSendOptionsAreIndependentOfRcpt(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "MAIL FROM:<sender@example.test> SIZE=1", replies: fakeReplies("250 ok\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	// MAIL opts out and its unadvertised SIZE= is written.
	if err := c.Mail(context.Background(), "sender@example.test",
		&smtp.MailOptions{Extra: []smtp.Param{{Keyword: "SIZE", Value: "1"}}},
		&MailSendOptions{AllowUnadvertisedParameters: true}); err != nil {
		t.Fatalf("Mail with the opt-out = %v, want the parameter sent unvalidated", err)
	}
	// The transaction's MAIL opt-out must not carry over to RCPT: no RCPT step
	// is scripted, so a written command would also fail the fake server match.
	err = c.Rcpt(context.Background(), "rcpt@example.test",
		&smtp.RcptOptions{Extra: []smtp.Param{{Keyword: "NOTIFY", Value: "NEVER"}}}, nil)
	if err == nil {
		t.Fatal("Rcpt = nil, want a local validation error: the MAIL opt-out is not a session-wide policy")
	}
	if !strings.Contains(err.Error(), "did not advertise extension") {
		t.Fatalf("Rcpt error = %v, want the unadvertised-extension validation error", err)
	}
}
