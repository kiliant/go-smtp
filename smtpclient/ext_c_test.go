package smtpclient

import (
	"context"
	"errors"
	"strings"
	"testing"

	smtp "github.com/kiliant/go-smtp"
)

func TestETRN(t *testing.T) {
	for _, code := range []int{250, 251, 252, 253} {
		t.Run(string(rune(code)), func(t *testing.T) {
			raw, done := startFakeServer(t, []fakeStep{
				{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 ETRN\r\n")},
				{command: "ETRN @example.test", replies: fakeReplies(stringReply(code, "queue status"))},
			}, nil)
			defer done()
			c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
			if err != nil {
				t.Fatal(err)
			}
			text, err := c.ETRN(context.Background(), "@example.test", nil)
			if err != nil || text != "queue status" {
				t.Fatalf("ETRN = (%q, %v)", text, err)
			}
		})
	}
}

func TestETRNLocalValidationAndRejection(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ETRN(context.Background(), "bad node", nil); err == nil || !strings.Contains(err.Error(), "ETRN") {
		t.Fatalf("invalid ETRN error = %v", err)
	}
	if _, err := c.ETRN(context.Background(), "example.test", nil); err == nil || !strings.Contains(err.Error(), "ETRN") {
		t.Fatalf("missing ETRN error = %v", err)
	}

	raw, done = startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 ETRN\r\n")},
		{command: "ETRN example.test", replies: fakeReplies("459 denied\r\n")},
	}, nil)
	defer done()
	c, err = NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.ETRN(context.Background(), "example.test", nil)
	var smtpErr *smtp.Error
	if !errors.As(err, &smtpErr) || smtpErr.Code != 459 {
		t.Fatalf("ETRN rejection = %v, want *smtp.Error 459", err)
	}
}

func TestATRNClosesAfterAcceptedRoleReversal(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 ATRN\r\n")},
		{command: "ATRN example.test,example.net", replies: fakeReplies("250 reversing\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	c.conn.mu.Lock()
	c.conn.state = stateAuthenticated
	c.conn.mu.Unlock()
	if err := c.ATRN(context.Background(), []string{"example.test", "example.net"}, nil); !errors.Is(err, ErrATRNRoleReversal) {
		t.Fatalf("ATRN error = %v, want ErrATRNRoleReversal", err)
	}
	if !c.conn.closed() {
		t.Fatal("ATRN success left the unrecoverable role-reversed connection open")
	}
}

func TestLegacyMailAndRcptParameters(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250-NO-SOLICITING\r\n", "250-MTRK\r\n", "250-SUBMITTER\r\n", "250-CONPERM\r\n", "250 CONNEG\r\n")},
		{command: "MAIL FROM:<sender@example.test> SOLICIT=org.example:ADV TRANSID=abc123 SUBMITTER=alice@example.test CONPERM", replies: fakeReplies("250 sender ok\r\n")},
		{command: "RCPT TO:<recipient@example.test> CONNEG", replies: fakeReplies("250 recipient ok\r\n")},
		{command: "RSET", replies: fakeReplies("250 reset\r\n")},
		{command: "MAIL FROM:<second@example.test>", replies: fakeReplies("250 sender ok\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{Legacy: &smtp.LegacyOptions{
		Solicit: "org.example:ADV", TransitID: "abc123", Submitter: "alice@example.test", ConPerm: true,
	}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt(context.Background(), "recipient@example.test", &smtp.RcptOptions{Legacy: &smtp.RecipientLegacyOptions{ConNeg: true}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Reset(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "second@example.test", nil); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyLocalValidation(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{Legacy: &smtp.LegacyOptions{Solicit: "bad value"}}); err == nil {
		t.Fatal("invalid SOLICIT was sent")
	}
	if err := c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{Legacy: &smtp.LegacyOptions{ConPerm: true}}); err == nil || !strings.Contains(err.Error(), "CONPERM") {
		t.Fatalf("missing CONPERM error = %v", err)
	}
}

func TestDeferredGroupCExtensionsRemainObservable(t *testing.T) {
	const extensions = "ETRN ATRN NO-SOLICITING MTRK SUBMITTER CONPERM CONNEG CHECKPOINT VERB ONEX SEND SOML SAML TURN"
	words := strings.Fields(extensions)
	replies := make([]string, 0, len(words)+1)
	replies = append(replies, "250-fake.test\r\n")
	for _, ext := range words[:len(words)-1] {
		replies = append(replies, "250-"+ext+" raw parameter\r\n")
	}
	replies = append(replies, "250 "+words[len(words)-1]+" raw parameter\r\n")
	raw, done := startFakeServer(t, []fakeStep{{command: "EHLO client.test", replies: replies}}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ext := range words {
		if params, ok := c.Extension(smtp.Extension(strings.ToLower(ext))); !ok || params != "raw parameter" {
			t.Errorf("Extension(%q) = (%q, %v), want raw parameter", ext, params, ok)
		}
	}
}

func stringReply(code int, text string) string {
	return string(rune('0'+code/100)) + string(rune('0'+code/10%10)) + string(rune('0'+code%10)) + " " + text + "\r\n"
}
