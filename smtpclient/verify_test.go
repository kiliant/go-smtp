package smtpclient

import (
	"context"
	"errors"
	"testing"

	smtp "github.com/kiliant/go-smtp"
)

// verify.go had no test coverage at all (confirmed by grep before this
// change). These tests cover Client.Verify, Client.Expand and Client.Help
// each for success, a multiline reply, and rejection surfacing as
// *smtp.Error, as required by docs/tasks/T05-mail-transaction.md.

func TestVerify(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		raw, done := startFakeServer(t, []fakeStep{
			{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
			{command: "VRFY someone", replies: fakeReplies("250 Someone <someone@example.test>\r\n")},
		}, nil)
		defer done()
		c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.Verify(context.Background(), "someone", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != "Someone <someone@example.test>" {
			t.Fatalf("Verify = %q", got)
		}
	})

	t.Run("multiline", func(t *testing.T) {
		raw, done := startFakeServer(t, []fakeStep{
			{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
			{command: "VRFY ambiguous", replies: fakeReplies("250-First Choice <first@example.test>\r\n", "250 Second Choice <second@example.test>\r\n")},
		}, nil)
		defer done()
		c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.Verify(context.Background(), "ambiguous", nil)
		if err != nil {
			t.Fatal(err)
		}
		want := "First Choice <first@example.test>\nSecond Choice <second@example.test>"
		if got != want {
			t.Fatalf("Verify multiline = %q, want %q", got, want)
		}
	})

	t.Run("rejection", func(t *testing.T) {
		raw, done := startFakeServer(t, []fakeStep{
			{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
			{command: "VRFY nobody", replies: fakeReplies("550 No such user here\r\n")},
		}, nil)
		defer done()
		c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Verify(context.Background(), "nobody", nil)
		var smtpErr *smtp.Error
		if !errors.As(err, &smtpErr) || smtpErr.Code != 550 {
			t.Fatalf("Verify rejection error = %v, want *smtp.Error code 550", err)
		}
	})

	t.Run("requires address", func(t *testing.T) {
		raw, done := startFakeServer(t, []fakeStep{{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")}}, nil)
		defer done()
		c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Verify(context.Background(), "", nil); err == nil {
			t.Fatal("Verify with an empty address returned nil error")
		}
	})
}

func TestExpand(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		raw, done := startFakeServer(t, []fakeStep{
			{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
			{command: "EXPN staff", replies: fakeReplies("250 Someone <someone@example.test>\r\n")},
		}, nil)
		defer done()
		c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.Expand(context.Background(), "staff", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != "Someone <someone@example.test>" {
			t.Fatalf("Expand = %q", got)
		}
	})

	t.Run("multiline", func(t *testing.T) {
		raw, done := startFakeServer(t, []fakeStep{
			{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
			{command: "EXPN staff", replies: fakeReplies("250-Alice <alice@example.test>\r\n", "250 Bob <bob@example.test>\r\n")},
		}, nil)
		defer done()
		c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.Expand(context.Background(), "staff", nil)
		if err != nil {
			t.Fatal(err)
		}
		want := "Alice <alice@example.test>\nBob <bob@example.test>"
		if got != want {
			t.Fatalf("Expand multiline = %q, want %q", got, want)
		}
	})

	t.Run("rejection", func(t *testing.T) {
		raw, done := startFakeServer(t, []fakeStep{
			{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
			{command: "EXPN staff", replies: fakeReplies("550 Access denied\r\n")},
		}, nil)
		defer done()
		c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Expand(context.Background(), "staff", nil)
		var smtpErr *smtp.Error
		if !errors.As(err, &smtpErr) || smtpErr.Code != 550 {
			t.Fatalf("Expand rejection error = %v, want *smtp.Error code 550", err)
		}
	})

	t.Run("requires list", func(t *testing.T) {
		raw, done := startFakeServer(t, []fakeStep{{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")}}, nil)
		defer done()
		c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Expand(context.Background(), "", nil); err == nil {
			t.Fatal("Expand with an empty list returned nil error")
		}
	})
}

func TestHelp(t *testing.T) {
	t.Run("success no topic", func(t *testing.T) {
		raw, done := startFakeServer(t, []fakeStep{
			{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
			{command: "HELP", replies: fakeReplies("214 See RFC 5321\r\n")},
		}, nil)
		defer done()
		c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.Help(context.Background(), "", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != "See RFC 5321" {
			t.Fatalf("Help = %q", got)
		}
	})

	t.Run("success with topic", func(t *testing.T) {
		raw, done := startFakeServer(t, []fakeStep{
			{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
			{command: "HELP MAIL", replies: fakeReplies("214 MAIL FROM:<reverse-path>\r\n")},
		}, nil)
		defer done()
		c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.Help(context.Background(), "MAIL", nil)
		if err != nil {
			t.Fatal(err)
		}
		if got != "MAIL FROM:<reverse-path>" {
			t.Fatalf("Help = %q", got)
		}
	})

	t.Run("multiline", func(t *testing.T) {
		raw, done := startFakeServer(t, []fakeStep{
			{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
			{command: "HELP", replies: fakeReplies("214-Commands:\r\n", "214 MAIL RCPT DATA\r\n")},
		}, nil)
		defer done()
		c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.Help(context.Background(), "", nil)
		if err != nil {
			t.Fatal(err)
		}
		want := "Commands:\nMAIL RCPT DATA"
		if got != want {
			t.Fatalf("Help multiline = %q, want %q", got, want)
		}
	})

	t.Run("rejection", func(t *testing.T) {
		raw, done := startFakeServer(t, []fakeStep{
			{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
			{command: "HELP", replies: fakeReplies("502 Command not implemented\r\n")},
		}, nil)
		defer done()
		c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = c.Help(context.Background(), "", nil)
		var smtpErr *smtp.Error
		if !errors.As(err, &smtpErr) || smtpErr.Code != 502 {
			t.Fatalf("Help rejection error = %v, want *smtp.Error code 502", err)
		}
	})
}
