package smtpclient

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	smtp "github.com/kiliant/go-smtp"
)

// FuzzHostileGreeting exercises the public client boundary. A peer may send
// arbitrary bytes before EHLO; malformed protocol input must surface as the
// library's single protocol error type and must never leave the pipe blocked.
func FuzzHostileGreeting(f *testing.F) {
	f.Add("220 fuzz.test ready\r\n")
	f.Add("2x0 bad code\r\n")
	f.Add("220-first\r\n221 mismatch\r\n")
	f.Add("220 ready\x00\r\n")
	f.Add("220 bare LF\n")
	f.Add("")

	f.Fuzz(func(t *testing.T, reply string) {
		if len(reply) > 64<<10 {
			t.Skip()
		}
		client, server := net.Pipe()
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer server.Close()
			_ = server.SetDeadline(time.Now().Add(100 * time.Millisecond))
			_, _ = server.Write([]byte(reply))
			buf := make([]byte, 4096)
			_, _ = server.Read(buf) // EHLO only when greeting happened to parse.
			_, _ = server.Write([]byte(reply))
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		c, err := NewClient(ctx, client, &ClientOptions{Identity: "fuzz.test", GreetingTimeout: 50 * time.Millisecond, MailTimeout: 50 * time.Millisecond})
		if err == nil {
			_ = c.Close()
		} else if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
			var protocol *smtp.Error
			if !errors.As(err, &protocol) {
				t.Fatalf("hostile reply crossed public boundary as %T: %v", err, err)
			}
		}
		select {
		case <-done:
		case <-time.After(250 * time.Millisecond):
			t.Fatal("hostile peer remained blocked")
		}
	})
}

func FuzzAuthChallenge(f *testing.F) {
	f.Add("")
	f.Add("=")
	f.Add("dXNlcg==")
	f.Add("%%%%")
	f.Add("AAAA")

	f.Fuzz(func(t *testing.T, encoded string) {
		challenge, err := decodeChallenge(encoded)
		if err != nil {
			return
		}
		if len(challenge) > maxAuthChallenge {
			t.Fatalf("accepted AUTH challenge larger than cap: %d", len(challenge))
		}
	})
}
