package smtpclient

import (
	"context"
	"errors"
	"strings"
	"testing"

	smtp "github.com/kiliant/go-smtp"
)

func TestBURLLastReturnsResultForEveryAcceptedRecipient(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250-BURL imap\r\n", "250 ENHANCEDSTATUSCODES\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 ok\r\n")},
		{command: "RCPT TO:<one@example.test>", replies: fakeReplies("250 ok\r\n")},
		{command: "RCPT TO:<two@example.test>", replies: fakeReplies("250 ok\r\n")},
		{command: "BURL imap://example.test/inbox/;uid=1 LAST", replies: fakeReplies("250 2.5.0 queued\r\n")},
		{command: "NOOP", replies: fakeReplies("250 reusable\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, recipient := range []string{"one@example.test", "two@example.test"} {
		if err := c.Rcpt(context.Background(), recipient, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	got, err := c.BURL(context.Background(), "imap://example.test/inbox/;uid=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("BURL result length = %d, want 2: %#v", len(got), got)
	}
	for i, recipient := range []string{"one@example.test", "two@example.test"} {
		want := smtp.RecipientResult{Recipient: recipient, Command: "BURL", Code: 250, Enhanced: smtp.ParseEnhancedCode("2.5.0"), Text: "queued"}
		if got[i].Recipient != want.Recipient || got[i].Command != want.Command || got[i].Code != want.Code || got[i].Enhanced != want.Enhanced || got[i].Text != want.Text {
			t.Errorf("BURL result[%d] = %#v, want %#v", i, got[i], want)
		}
	}
	if err := c.Noop(context.Background(), nil); err != nil {
		t.Fatalf("NOOP after BURL LAST: %v", err)
	}
}

func TestBURLWithoutLastReturnsEmptyResultAndKeepsTransactionOpen(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 BURL imap\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 ok\r\n")},
		{command: "RCPT TO:<recipient@example.test>", replies: fakeReplies("250 ok\r\n")},
		{command: "BURL imap://example.test/inbox/;uid=1", replies: fakeReplies("250 waiting for more content\r\n")},
		{command: "BURL imap://example.test/inbox/;uid=2 LAST", replies: fakeReplies("250 queued\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt(context.Background(), "recipient@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	got, err := c.BURL(context.Background(), "imap://example.test/inbox/;uid=1", &BURLOptions{Last: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("non-LAST BURL result = %#v, want empty", got)
	}
	got, err = c.BURL(context.Background(), "imap://example.test/inbox/;uid=2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Recipient != "recipient@example.test" || got[0].Command != "BURL" {
		t.Fatalf("LAST BURL result = %#v", got)
	}
}

func TestBURLLastFailureUsesBURLCommandAndEndsTransaction(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250-BURL imap\r\n", "250 ENHANCEDSTATUSCODES\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 ok\r\n")},
		{command: "RCPT TO:<recipient@example.test>", replies: fakeReplies("250 ok\r\n")},
		{command: "BURL imap://example.test/inbox/;uid=1 LAST", replies: fakeReplies("554 5.6.6 URL resolution failed\r\n")},
		{command: "NOOP", replies: fakeReplies("250 reusable\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt(context.Background(), "recipient@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	got, err := c.BURL(context.Background(), "imap://example.test/inbox/;uid=1", nil)
	if got != nil {
		t.Fatalf("failed BURL result = %#v, want nil", got)
	}
	var smtpErr *smtp.Error
	if !errors.As(err, &smtpErr) || smtpErr.Command != "BURL" || smtpErr.Code != 554 || smtpErr.Enhanced.Raw != "5.6.6" || smtpErr.Text != "URL resolution failed" {
		t.Fatalf("BURL failure = %#v, want BURL 554 5.6.6", err)
	}
	if err := c.Noop(context.Background(), nil); err != nil {
		t.Fatalf("NOOP after failed BURL LAST: %v", err)
	}
}

func TestBURLRejectedLocallyInLMTPMode(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "LHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 BURL imap\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test", LMTP: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.BURL(context.Background(), "imap://example.test/inbox/;uid=1", nil)
	if got != nil || err == nil || !strings.Contains(err.Error(), "not supported in LMTP") {
		t.Fatalf("LMTP BURL = (%#v, %v), want local unsupported error", got, err)
	}
}
