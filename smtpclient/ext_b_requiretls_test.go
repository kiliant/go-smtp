package smtpclient

import (
	"context"
	"strings"
	"testing"

	smtp "github.com/kiliant/go-smtp"
)

// TestRequireTLSRejectedOverCleartextSession covers the audit finding that
// REQUIRETLS was written to the wire unconditionally, with no local check
// that RFC 8689 §2's own precondition holds: "This option MUST only be
// specified in the context of an SMTP session meeting the security
// requirements of REQUIRETLS: ... The session itself MUST employ TLS
// transmission." Even though the fake server here advertises REQUIRETLS
// (so the generic "extension not advertised" gate would not catch this),
// requesting it over a plain cleartext session must fail locally before
// MAIL ever reaches the wire — the script below has no MAIL step, so
// writing the command anyway would fail the fake server's command match.
func TestRequireTLSRejectedOverCleartextSession(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 REQUIRETLS\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{
		Delivery: &smtp.DeliveryOptions{RequireTLS: true},
	}, nil)

	if err == nil {
		t.Fatal("Mail with RequireTLS succeeded over a cleartext session")
	}
	if !strings.Contains(err.Error(), "TLS-protected session") {
		t.Fatalf("Mail error = %v, want a TLS-protected-session error citing RFC 8689 §2", err)
	}
}

// TestRequireTLSPermittedAfterSTARTTLS is the permitted-path counterpart:
// once the session is actually TLS-protected, REQUIRETLS must still be sent
// on MAIL FROM as before.
func TestRequireTLSPermittedAfterSTARTTLS(t *testing.T) {
	serverTLS, clientTLS := fakeTLSConfig(t)
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 STARTTLS\r\n")},
		{command: "STARTTLS", replies: fakeReplies("220 ready\r\n"), startTLS: true},
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 REQUIRETLS\r\n")},
		{command: "MAIL FROM:<sender@example.test> REQUIRETLS", replies: fakeReplies("250 ok\r\n")},
	}, serverTLS)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test", TLSConfig: clientTLS})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.StartTLS(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{
		Delivery: &smtp.DeliveryOptions{RequireTLS: true},
	}, nil); err != nil {
		t.Fatal(err)
	}
}

// TestRequireTLSPermittedOverImplicitTLS covers the ClientOptions.ImplicitTLS
// path (RFC 8314 submission convention) as an independent way of satisfying
// RFC 8689 §2's "session itself MUST employ TLS transmission" precondition,
// so the two paths to a TLS-protected session are not conflated in the
// implementation.
func TestRequireTLSPermittedOverImplicitTLS(t *testing.T) {
	serverTLS, clientTLS := fakeTLSConfig(t)
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 REQUIRETLS\r\n"), implicitTLS: true},
		{command: "MAIL FROM:<sender@example.test> REQUIRETLS", replies: fakeReplies("250 ok\r\n")},
	}, serverTLS)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{
		Identity: "client.test", ImplicitTLS: true, TLSConfig: clientTLS,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{
		Delivery: &smtp.DeliveryOptions{RequireTLS: true},
	}, nil); err != nil {
		t.Fatal(err)
	}
}
