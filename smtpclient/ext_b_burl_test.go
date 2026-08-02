package smtpclient

import (
	"context"
	"testing"
)

func TestBURLLast(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 BURL imap\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 ok\r\n")},
		{command: "RCPT TO:<recipient@example.test>", replies: fakeReplies("250 ok\r\n")},
		{command: "BURL imap://example.test/inbox/;uid=1 LAST", replies: fakeReplies("250 ok\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt(context.Background(), "recipient@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.BURL(context.Background(), "imap://example.test/inbox/;uid=1", nil); err != nil {
		t.Fatal(err)
	}
}
