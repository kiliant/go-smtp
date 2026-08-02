package smtpclient

import (
	"context"
	"errors"
	"strings"
	"testing"

	smtp "github.com/kiliant/go-smtp"
)

func TestTransactionDataReturnsPerRecipientResults(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 sender ok\r\n")},
		{command: "RCPT TO:<one@example.test>", replies: fakeReplies("250 one ok\r\n")},
		{command: "RCPT TO:<two@example.test>", replies: fakeReplies("251 two forwarded\r\n")},
		{command: "DATA", replies: fakeReplies("354 send it\r\n")},
		{command: ".", replies: fakeReplies("250 queued\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt(context.Background(), "one@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt(context.Background(), "two@example.test", nil); err != nil {
		t.Fatal(err)
	}
	got, err := c.Data(context.Background(), strings.NewReader(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got.AllAccepted() {
		t.Fatalf("Data result = %#v", got)
	}
	for _, result := range got {
		if result.Command != "DATA" || result.Code != 250 || result.Text != "queued" {
			t.Errorf("recipient result = %#v", result)
		}
	}
}

func TestDataRejectionLeavesTransactionRecoverable(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 sender ok\r\n")},
		{command: "RCPT TO:<one@example.test>", replies: fakeReplies("250 one ok\r\n")},
		{command: "DATA", replies: fakeReplies("554 no content\r\n")},
		{command: "RSET", replies: fakeReplies("250 reset\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt(context.Background(), "one@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Data(context.Background(), strings.NewReader("body"), nil); err == nil {
		t.Fatal("DATA rejection returned nil")
	}
	if err := c.Reset(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestDataRequires250FinalReply(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 sender ok\r\n")},
		{command: "RCPT TO:<one@example.test>", replies: fakeReplies("250 one ok\r\n")},
		{command: "DATA", replies: fakeReplies("354 send it\r\n")},
		{command: ".", replies: fakeReplies("251 non-standard completion\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt(context.Background(), "one@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Data(context.Background(), strings.NewReader(""), nil); err == nil {
		t.Fatal("DATA 251 completion returned nil")
	} else {
		var smtpErr *smtp.Error
		if !errors.As(err, &smtpErr) || smtpErr.Code != 251 {
			t.Fatalf("DATA error = %v, want *smtp.Error code 251", err)
		}
	}
	c.conn.mu.Lock()
	defer c.conn.mu.Unlock()
	if c.conn.state == stateTransaction {
		t.Fatal("DATA 251 left transaction open")
	}
}

func TestMailExtraValidatesAdvertisedExtensionBeforeWrite(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{Extra: []smtp.Param{{Keyword: "SIZE", Value: "1"}}})
	if err == nil || !strings.Contains(err.Error(), "SIZE") {
		t.Fatalf("Mail error = %v, want missing SIZE extension", err)
	}
}
