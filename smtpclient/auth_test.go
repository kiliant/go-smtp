package smtpclient

import (
	"context"
	"strings"
	"testing"
)

func TestAuthPlainInitialResponseAndReEHLO(t *testing.T) {
	server, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 AUTH PLAIN LOGIN\r\n")},
		{command: "AUTH PLAIN AHVzZXIAcGFzcw==", replies: fakeReplies("235 accepted\r\n")},
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
	}, nil)
	defer done()
	client, err := NewClient(context.Background(), server, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Auth(context.Background(), &AuthOptions{Username: "user", Password: "pass", AllowInsecureAuth: true}); err != nil {
		t.Fatal(err)
	}
}

func TestAuthRefusesCleartextCredentials(t *testing.T) {
	server, done := startFakeServer(t, []fakeStep{{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 AUTH PLAIN\r\n")}}, nil)
	defer done()
	client, err := NewClient(context.Background(), server, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	err = client.Auth(context.Background(), &AuthOptions{Username: "user", Password: "pass"})
	if err == nil || !strings.Contains(err.Error(), "unencrypted") {
		t.Fatalf("Auth error = %v, want cleartext credential refusal", err)
	}
}

func TestSelectMechanismPrefersSCRAMPlus(t *testing.T) {
	got, err := selectMechanism(nil, map[string]bool{
		"SCRAM-SHA-256-PLUS": true,
		"SCRAM-SHA-256":      true,
	})
	if err != nil || got != "SCRAM-SHA-256-PLUS" {
		t.Fatalf("selectMechanism = %q, %v", got, err)
	}
}

// TestAuthCancelsWithAsteriskOnMalformedChallenge covers the audit finding
// that RFC 4954 §4 cancellation was unimplemented: when the client cannot
// make sense of a challenge, it must write a lone "*" rather than dropping
// the connection outright, then read the server's reply to it. Here the
// server plays along with the required 501, so the connection must remain
// usable afterwards.
func TestAuthCancelsWithAsteriskOnMalformedChallenge(t *testing.T) {
	server, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 AUTH CRAM-MD5\r\n")},
		{command: "AUTH CRAM-MD5", replies: fakeReplies("334 not-valid-base64!!!\r\n")},
		{command: "*", replies: fakeReplies("501 5.5.4 cancelled\r\n")},
		{command: "NOOP", replies: fakeReplies("250 still here\r\n")},
	}, nil)
	defer done()
	client, err := NewClient(context.Background(), server, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	err = client.Auth(context.Background(), &AuthOptions{Username: "user", Password: "pass", AllowInsecureAuth: true, Mechanisms: []string{"CRAM-MD5"}})
	if err == nil {
		t.Fatal("Auth with a malformed challenge returned a nil error")
	}
	if client.conn.closed() {
		t.Fatal("AUTH cancellation poisoned the connection despite a clean RFC 4954 501 response")
	}
	if err := client.Noop(context.Background(), nil); err != nil {
		t.Fatalf("Noop after AUTH cancellation = %v, want success: the session must remain usable", err)
	}
}

// TestAuthPoisonsConnectionWhenCancelNotAcknowledged is cancelAuth's other
// branch: if the server does not honour the RFC 4954 §4 contract by
// answering "*" with 501, the connection can no longer be trusted and must
// be poisoned.
func TestAuthPoisonsConnectionWhenCancelNotAcknowledged(t *testing.T) {
	server, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 AUTH CRAM-MD5\r\n")},
		{command: "AUTH CRAM-MD5", replies: fakeReplies("334 not-valid-base64!!!\r\n")},
		{command: "*", replies: fakeReplies("500 unexpected\r\n")},
	}, nil)
	defer done()
	client, err := NewClient(context.Background(), server, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	err = client.Auth(context.Background(), &AuthOptions{Username: "user", Password: "pass", AllowInsecureAuth: true, Mechanisms: []string{"CRAM-MD5"}})
	if err == nil {
		t.Fatal("Auth with a malformed challenge returned a nil error")
	}
	if !client.conn.closed() {
		t.Fatal("expected the connection to be poisoned when the server did not answer AUTH cancellation with 501")
	}
}

func TestAuthAcceptsHistoricalAUTHEqualsAdvertisement(t *testing.T) {
	server, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 AUTH=PLAIN\r\n")},
		{command: "AUTH PLAIN AHVzZXIAcGFzcw==", replies: fakeReplies("235 accepted\r\n")},
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
	}, nil)
	defer done()
	client, err := NewClient(context.Background(), server, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Auth(context.Background(), &AuthOptions{Username: "user", Password: "pass", AllowInsecureAuth: true}); err != nil {
		t.Fatal(err)
	}
}
