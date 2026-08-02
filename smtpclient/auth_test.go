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
