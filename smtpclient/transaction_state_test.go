package smtpclient

import (
	"context"
	"strings"
	"testing"

	smtp "github.com/kiliant/go-smtp"
)

// The tests in this file cover behaviour an audit found to be correct in the
// code but asserted nowhere: the transaction state machine's ordering rules
// and the AllowUnadvertisedParameters escape hatch. Each of these would have
// regressed silently.

// TestRcptBeforeMailRejected pins that the state machine refuses a recipient
// with no envelope open. Reaching the wire would leave the session
// desynchronised against a server that answers 503.
func TestRcptBeforeMailRejected(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	// The fake server scripts no RCPT: if the client wrote one, the script
	// mismatch fails the test in addition to the assertion below.
	err = c.Rcpt(context.Background(), "rcpt@example.test", nil)
	if err == nil || !strings.Contains(err.Error(), "RCPT") {
		t.Fatalf("Rcpt before Mail = %v, want a local state error naming RCPT", err)
	}
}

// TestDataBeforeRcptRejected pins the companion rule: RFC 5321 §3.3 requires
// at least one accepted recipient before DATA.
func TestDataBeforeRcptRejected(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 ok\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil); err != nil {
		t.Fatal(err)
	}
	_, err = c.Data(context.Background(), strings.NewReader("body\r\n"), nil)
	if err == nil || !strings.Contains(err.Error(), "recipient") {
		t.Fatalf("Data with no recipient = %v, want a local error naming the missing recipient", err)
	}
}

// TestDataBeforeMailRejected covers the outer ordering case: no transaction
// at all.
func TestDataBeforeMailRejected(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Data(context.Background(), strings.NewReader("body\r\n"), nil)
	if err == nil || !strings.Contains(err.Error(), "DATA") {
		t.Fatalf("Data before Mail = %v, want a local state error naming DATA", err)
	}
}

// TestMailAllowUnadvertisedParametersBypassesValidation is the positive half
// of the escape hatch required by docs/API-STABILITY.md §1b. The negative
// half — rejection when the extension is unadvertised — is covered by
// TestMailExtraValidatesAdvertisedExtensionBeforeWrite. Without this test a
// regression that ignored the opt-out would keep that test green while
// permanently blocking a caller from sending a parameter a future RFC allows.
//
// The fake server scripts the exact wire form, so the assertion is that the
// parameter really reached the wire, not merely that no error came back.
func TestMailAllowUnadvertisedParametersBypassesValidation(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "MAIL FROM:<sender@example.test> SIZE=1", replies: fakeReplies("250 ok\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{
		Extra:                       []smtp.Param{{Keyword: "SIZE", Value: "1"}},
		AllowUnadvertisedParameters: true,
	})
	if err != nil {
		t.Fatalf("Mail with AllowUnadvertisedParameters = %v, want the parameter sent unvalidated", err)
	}
}

// TestRcptAllowUnadvertisedParametersBypassesValidation is the recipient-side
// counterpart. It is separate because the two options are separate fields on
// separate structs, and a fix applied to only one of them would otherwise go
// unnoticed.
func TestRcptAllowUnadvertisedParametersBypassesValidation(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 ok\r\n")},
		{command: "RCPT TO:<rcpt@example.test> NOTIFY=NEVER", replies: fakeReplies("250 ok\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil); err != nil {
		t.Fatal(err)
	}
	err = c.Rcpt(context.Background(), "rcpt@example.test", &smtp.RcptOptions{
		Extra:                       []smtp.Param{{Keyword: "NOTIFY", Value: "NEVER"}},
		AllowUnadvertisedParameters: true,
	})
	if err != nil {
		t.Fatalf("Rcpt with AllowUnadvertisedParameters = %v, want the parameter sent unvalidated", err)
	}
}

// TestRcptRejectsUnadvertisedParameterByDefault is the negative control for
// the test above: same parameter, same unadvertising server, opt-out off.
// Without it, the pair could both pass because validation was removed
// entirely rather than because the opt-out works.
func TestRcptRejectsUnadvertisedParameterByDefault(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 ok\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil); err != nil {
		t.Fatal(err)
	}
	err = c.Rcpt(context.Background(), "rcpt@example.test", &smtp.RcptOptions{
		Extra: []smtp.Param{{Keyword: "NOTIFY", Value: "NEVER"}},
	})
	if err == nil || !strings.Contains(err.Error(), "DSN") {
		t.Fatalf("Rcpt error = %v, want a local error naming the unadvertised DSN extension", err)
	}
}
