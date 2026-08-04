//go:build interop

package harness

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	smtp "github.com/kiliant/go-smtp"
)

func TestAssertProfileClaimedExtension(t *testing.T) {
	for _, tc := range []struct {
		name    string
		claimed smtp.Extension
		want    Outcome
	}{
		{name: "advertised", claimed: smtp.ExtSize, want: OutcomeOK},
		{name: "missing", claimed: smtp.ExtStartTLS, want: OutcomeProfileViolation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			addr, stop := startEHLOServer(t, []string{"SIZE 1000", "8BITMIME"})
			defer stop()

			_, portText, err := net.SplitHostPort(addr)
			if err != nil {
				t.Fatal(err)
			}
			var port int
			if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
				t.Fatal(err)
			}
			result := AssertProfile(context.Background(), Config{
				HealthTimeout:  time.Second,
				CommandTimeout: time.Second,
			}, &Handle{hostPorts: map[int]int{25: port}}, Profile{
				Name:               "fake",
				Ports:              []Port{{Container: 25, Kind: "smtp"}},
				ExpectedExtensions: []smtp.Extension{tc.claimed},
			})
			if result.Outcome != tc.want {
				t.Fatalf("outcome = %v, want %v (err: %v; transcript: %s)", result.Outcome, tc.want, result.Err, result.Transcript)
			}
		})
	}
}

func startEHLOServer(t *testing.T, extensions []string) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("220 fake ESMTP ready\r\n"))
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO "):
				_, _ = conn.Write([]byte("250-fake.test ready\r\n"))
				for _, ext := range extensions[:len(extensions)-1] {
					_, _ = fmt.Fprintf(conn, "250-%s\r\n", ext)
				}
				_, _ = fmt.Fprintf(conn, "250 %s\r\n", extensions[len(extensions)-1])
			case strings.HasPrefix(line, "QUIT"):
				_, _ = conn.Write([]byte("221 closing\r\n"))
				return
			}
		}
	}()
	return listener.Addr().String(), func() {
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("fake SMTP server did not stop")
		}
	}
}
