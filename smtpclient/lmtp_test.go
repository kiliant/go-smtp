package smtpclient

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	smtp "github.com/kiliant/go-smtp"
)

func TestLMTPUsesLHLOAndReturnsPerRecipientReplies(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "LHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250-PIPELINING\r\n", "250 ENHANCEDSTATUSCODES\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 sender ok\r\n")},
		{command: "RCPT TO:<one@example.test>", replies: fakeReplies("250 one ok\r\n")},
		{command: "RCPT TO:<two@example.test>", replies: fakeReplies("250 two ok\r\n")},
		{command: "DATA", replies: fakeReplies("354 send it\r\n")},
		{command: ".", replies: fakeReplies("250 2.1.5 one delivered\r\n", "550 5.1.1 two unknown\r\n")},
		{command: "NOOP", replies: fakeReplies("250 reusable\r\n")},
	}, nil)
	defer done()

	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test", LMTP: true})
	if err != nil {
		t.Fatal(err)
	}
	if params, ok := c.Extension(smtp.ExtPipelining); !ok || params != "" {
		t.Fatalf("PIPELINING = (%q, %v), want (empty, true)", params, ok)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, recipient := range []string{"one@example.test", "two@example.test"} {
		if err := c.Rcpt(context.Background(), recipient, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	got, err := c.Data(context.Background(), strings.NewReader(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Recipient != "one@example.test" || got[1].Recipient != "two@example.test" {
		t.Fatalf("DATA recipients = %#v", got)
	}
	if !got[0].Accepted() || got[0].Enhanced.Raw != "2.1.5" || got[0].Text != "one delivered" {
		t.Errorf("first DATA result = %#v", got[0])
	}
	if got[1].Accepted() || got[1].Enhanced.Raw != "5.1.1" || got[1].Text != "two unknown" {
		t.Errorf("second DATA result = %#v", got[1])
	}
	if err := c.Noop(context.Background(), nil); err != nil {
		t.Fatalf("NOOP after valid LMTP DATA: %v", err)
	}
}

func TestLMTPDoesNotFallBackToHELO(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "LHLO client.test", replies: fakeReplies("502 SMTP only\r\n")},
	}, nil)
	defer done()

	_, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test", LMTP: true})
	var smtpErr *smtp.Error
	if !errors.As(err, &smtpErr) || smtpErr.Code != 502 {
		t.Fatalf("NewClient LMTP error = %v, want *smtp.Error code 502", err)
	}
}

func TestLMTPAuthRenegotiatesWithLHLO(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "LHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 AUTH PLAIN\r\n")},
		{command: "AUTH PLAIN AHVzZXIAcGFzcw==", replies: fakeReplies("235 accepted\r\n")},
		{command: "LHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
	}, nil)
	defer done()

	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test", LMTP: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Auth(context.Background(), &AuthOptions{Username: "user", Password: "pass", AllowInsecureAuth: true}); err != nil {
		t.Fatal(err)
	}
}

func TestLMTPDataIncludesOnlyRecipientsAcceptedByRCPT(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "LHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 sender ok\r\n")},
		{command: "RCPT TO:<one@example.test>", replies: fakeReplies("250 one ok\r\n")},
		{command: "RCPT TO:<rejected@example.test>", replies: fakeReplies("550 no such user\r\n")},
		{command: "RCPT TO:<three@example.test>", replies: fakeReplies("250 three ok\r\n")},
		{command: "DATA", replies: fakeReplies("354 send it\r\n")},
		{command: ".", replies: fakeReplies("250 one delivered\r\n", "250 three delivered\r\n")},
		{command: "NOOP", replies: fakeReplies("250 reusable\r\n")},
	}, nil)
	defer done()

	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test", LMTP: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt(context.Background(), "one@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt(context.Background(), "rejected@example.test", nil, nil); err == nil {
		t.Fatal("rejected RCPT succeeded")
	}
	if err := c.Rcpt(context.Background(), "three@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	got, err := c.Data(context.Background(), strings.NewReader(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Recipient != "one@example.test" || got[1].Recipient != "three@example.test" {
		t.Fatalf("DATA recipients after rejected RCPT = %#v", got)
	}
	if err := c.Noop(context.Background(), nil); err != nil {
		t.Fatalf("NOOP after accepted-recipient LMTP DATA: %v", err)
	}
}

func TestLMTPTooFewFinalRepliesPoisonsConnection(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "LHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 sender ok\r\n")},
		{command: "RCPT TO:<one@example.test>", replies: fakeReplies("250 one ok\r\n")},
		{command: "RCPT TO:<two@example.test>", replies: fakeReplies("250 two ok\r\n")},
		{command: "DATA", replies: fakeReplies("354 send it\r\n")},
		{command: ".", replies: fakeReplies("250 one delivered\r\n")},
	}, nil)
	defer done()

	c := newLMTPTransactionClient(t, raw)
	if _, err := c.Data(context.Background(), strings.NewReader(""), nil); err == nil {
		t.Fatal("DATA with too few LMTP replies succeeded")
	}
	if err := c.Noop(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("NOOP after truncated LMTP replies = %v, want closed connection", err)
	}
}

func TestLMTPExtraFinalReplyPoisonsConnection(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "LHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 sender ok\r\n")},
		{command: "RCPT TO:<one@example.test>", replies: fakeReplies("250 one ok\r\n")},
		{command: "DATA", replies: fakeReplies("354 send it\r\n")},
		{command: ".", replies: fakeReplies("250 one delivered\r\n", "250 hostile extra\r\n")},
	}, nil)
	defer done()

	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test", LMTP: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt(context.Background(), "one@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Data(context.Background(), strings.NewReader(""), nil); err == nil {
		t.Fatal("DATA with extra LMTP reply succeeded")
	}
	if err := c.Noop(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("NOOP after extra LMTP reply = %v, want closed connection", err)
	}
}

func TestLMTPZeroRecipientsHasNoFinalReplies(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "LHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 sender ok\r\n")},
		{command: "DATA", replies: fakeReplies("354 send it\r\n")},
		{command: "."},
		{command: "NOOP", replies: fakeReplies("250 reusable\r\n")},
	}, nil)
	defer done()

	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test", LMTP: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	got, err := c.Data(context.Background(), strings.NewReader(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("zero-recipient DATA result = %#v", got)
	}
	if err := c.Noop(context.Background(), nil); err != nil {
		t.Fatalf("NOOP after zero-recipient LMTP DATA: %v", err)
	}
}

func TestLMTPMalformedFinalReplyPoisonsConnection(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "LHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 sender ok\r\n")},
		{command: "RCPT TO:<one@example.test>", replies: fakeReplies("250 one ok\r\n")},
		{command: "RCPT TO:<two@example.test>", replies: fakeReplies("250 two ok\r\n")},
		{command: "DATA", replies: fakeReplies("354 send it\r\n")},
		{command: ".", replies: fakeReplies("250 one delivered\r\n", "malformed\r\n")},
	}, nil)
	defer done()

	c := newLMTPTransactionClient(t, raw)
	if _, err := c.Data(context.Background(), strings.NewReader(""), nil); err == nil {
		t.Fatal("DATA with malformed LMTP reply succeeded")
	}
	if err := c.Noop(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("NOOP after malformed LMTP reply = %v, want closed connection", err)
	}
}

func newLMTPTransactionClient(t *testing.T, raw net.Conn) *Client {
	t.Helper()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test", LMTP: true})
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
	return c
}
