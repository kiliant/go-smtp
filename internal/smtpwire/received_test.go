package smtpwire

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestReceivedProtocol(t *testing.T) {
	tests := []struct {
		name string
		opts ReceivedOptions
		want string
	}{
		{name: "SMTP", want: "SMTP"},
		{name: "ESMTP", opts: ReceivedOptions{Extended: true}, want: "ESMTP"},
		{name: "ESMTPA", opts: ReceivedOptions{Extended: true, Authenticated: true}, want: "ESMTPA"},
		{name: "ESMTPS", opts: ReceivedOptions{Extended: true, TLS: true}, want: "ESMTPS"},
		{name: "ESMTPSA", opts: ReceivedOptions{Extended: true, TLS: true, Authenticated: true}, want: "ESMTPSA"},
		{name: "LMTP", opts: ReceivedOptions{LMTP: true}, want: "LMTP"},
		{name: "LMTPA", opts: ReceivedOptions{LMTP: true, Authenticated: true}, want: "LMTPA"},
		{name: "LMTPS", opts: ReceivedOptions{LMTP: true, TLS: true}, want: "LMTPS"},
		{name: "LMTPSA", opts: ReceivedOptions{LMTP: true, TLS: true, Authenticated: true}, want: "LMTPSA"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := receivedProtocol(tt.opts)
			if err != nil {
				t.Fatalf("receivedProtocol: %v", err)
			}
			if got != tt.want {
				t.Fatalf("receivedProtocol = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatReceived(t *testing.T) {
	timestamp := time.Date(2026, time.August, 12, 14, 30, 15, 0, time.FixedZone("CEST", 2*60*60))
	got, err := FormatReceived(ReceivedOptions{
		From:           "client.example",
		By:             "mx.example",
		Extended:       true,
		TLS:            true,
		Authenticated:  true,
		ID:             "abc123",
		For:            "<one@example.test>",
		RecipientCount: 1,
		Timestamp:      timestamp,
	})
	if err != nil {
		t.Fatalf("FormatReceived: %v", err)
	}
	want := "Received: from client.example by mx.example with ESMTPSA id abc123 for <one@example.test>; Wed, 12 Aug 2026 14:30:15 +0200\r\n"
	if got != want {
		t.Fatalf("header = %q, want %q", got, want)
	}
}

func TestFormatReceivedOmitsForWithMultipleRecipients(t *testing.T) {
	got, err := FormatReceived(ReceivedOptions{
		From: "client.example", By: "mx.example", Extended: true,
		For: "<private@example.test>", RecipientCount: 2,
		Timestamp: time.Unix(0, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("FormatReceived: %v", err)
	}
	if strings.Contains(got, " for ") || strings.Contains(got, "private") {
		t.Fatalf("multi-recipient header leaked FOR clause: %q", got)
	}
}

func TestFormatReceivedRejectsInvalidStateAndInjection(t *testing.T) {
	base := ReceivedOptions{From: "client", By: "mx", Timestamp: time.Unix(0, 0).UTC()}
	badState := base
	badState.TLS = true
	if _, err := FormatReceived(badState); !errors.Is(err, ErrReceivedState) {
		t.Fatalf("HELO plus TLS error = %v, want ErrReceivedState", err)
	}
	injected := base
	injected.From = "client\r\nBcc: victim"
	if _, err := FormatReceived(injected); !errors.Is(err, ErrReceivedField) {
		t.Fatalf("injection error = %v, want ErrReceivedField", err)
	}
}
