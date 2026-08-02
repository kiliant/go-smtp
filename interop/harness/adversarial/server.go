// Package adversarial provides small, deterministic SMTP peers for testing a
// client against deliberately hostile server behaviour. It is opt-in: nothing
// in the interop matrix starts these listeners automatically.
package adversarial

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// Scenario selects one hostile but bounded peer behaviour.
type Scenario string

const (
	MalformedCode      Scenario = "malformed-code"
	MismatchedReply    Scenario = "mismatched-reply"
	OversizedReplyLine Scenario = "oversized-reply-line"
	UnsolicitedReply   Scenario = "unsolicited-reply"
	ExtraReplies       Scenario = "extra-replies"
	CloseEarly         Scenario = "close-early"
	BareLineEnding     Scenario = "bare-line-ending"
	NULReplyText       Scenario = "nul-reply-text"
	ManyEHLOKeywords   Scenario = "many-ehlo-keywords"
	LongEHLOKeyword    Scenario = "long-ehlo-keyword"
	StallAfter354      Scenario = "stall-after-354"
	BDATShortAccept    Scenario = "bdat-short-accept"
	LMTPTooFew         Scenario = "lmtp-too-few"
	LMTPTooMany        Scenario = "lmtp-too-many"
	Close421           Scenario = "close-421"
)

// Server owns a loopback listener and bounded connection handlers. Close is
// idempotent and waits for handlers, so a test cannot leak a hostile peer.
type Server struct {
	Scenario Scenario
	Listener net.Listener
	done     chan struct{}
	once     sync.Once
	wg       sync.WaitGroup
}

// Pipe returns the client side of an in-memory connection served with the
// selected scenario. It exercises the same protocol handler as Start without
// consuming loopback ports, making it suitable for high-volume unit tests.
// The cleanup function is idempotent and waits for the handler to exit.
func Pipe(ctx context.Context, scenario Scenario) (net.Conn, func()) {
	client, server := net.Pipe()
	done := make(chan struct{})
	finished := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(finished)
		serve(ctx, server, scenario, done)
	}()
	return client, func() {
		once.Do(func() { close(done) })
		_ = client.Close()
		<-finished
	}
}

// Start begins a loopback server for scenario. The listener accepts only local
// test connections; callers should use Addr when constructing a client.
func Start(ctx context.Context, scenario Scenario) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Server{Scenario: scenario, Listener: ln, done: make(chan struct{})}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-s.done:
					return
				default:
					return
				}
			}
			s.wg.Add(1)
			go func() { defer s.wg.Done(); serve(ctx, conn, scenario, s.done) }()
		}
	}()
	return s, nil
}

func (s *Server) Addr() string { return s.Listener.Addr().String() }

func (s *Server) Close() error {
	var err error
	s.once.Do(func() {
		close(s.done)
		err = s.Listener.Close()
		s.wg.Wait()
	})
	return err
}

func serve(ctx context.Context, conn net.Conn, scenario Scenario, done <-chan struct{}) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if scenario == CloseEarly {
		return
	}
	if scenario == MalformedCode {
		_, _ = conn.Write([]byte("2x0 not a reply\r\n"))
		return
	}
	if scenario == MismatchedReply {
		_, _ = conn.Write([]byte("250-first\r\n251 final\r\n"))
		return
	}
	if scenario == OversizedReplyLine {
		_, _ = conn.Write([]byte("250 "))
		writeRepeat(conn, 'x', 10<<20)
		_, _ = conn.Write([]byte("\r\n"))
		return
	}
	if scenario == UnsolicitedReply {
		_, _ = conn.Write([]byte("220 ready\r\n250 unsolicited\r\n"))
		return
	}
	if scenario == ExtraReplies {
		_, _ = conn.Write([]byte("220 ready\r\n"))
		if readCommand(conn) != "" {
			_, _ = conn.Write([]byte("250 ok\r\n550 extra\r\n"))
		}
		return
	}
	if scenario == BareLineEnding {
		_, _ = conn.Write([]byte("220 ready\n"))
		return
	}
	if scenario == NULReplyText {
		_, _ = conn.Write([]byte("220 ready\x00\r\n"))
		return
	}
	if scenario == Close421 {
		_, _ = conn.Write([]byte("421 closing\r\n"))
		return
	}

	_, _ = conn.Write([]byte("220 adversarial.test ready\r\n"))
	command := readCommand(conn)
	if command == "" {
		return
	}
	switch scenario {
	case ManyEHLOKeywords:
		writeEHLO(conn, 10_000, 8)
	case LongEHLOKeyword:
		_, _ = conn.Write([]byte("250-adversarial.test\r\n250 "))
		writeRepeat(conn, 'K', 100<<10)
		_, _ = conn.Write([]byte("\r\n"))
	case StallAfter354:
		_, _ = conn.Write([]byte("250 ok\r\n"))
		if strings.HasPrefix(readCommand(conn), "DATA") {
			_, _ = conn.Write([]byte("354 send content\r\n"))
			select {
			case <-done:
			case <-ctx.Done():
			case <-time.After(1500 * time.Millisecond):
			}
		}
	case BDATShortAccept:
		_, _ = conn.Write([]byte("250 CHUNKING\r\n"))
		if strings.HasPrefix(readCommand(conn), "BDAT") {
			_, _ = conn.Write([]byte("250 accepted prematurely\r\n"))
		}
	case LMTPTooFew, LMTPTooMany:
		_, _ = conn.Write([]byte("250 ok\r\n"))
		for {
			line := readCommand(conn)
			if line == "" {
				return
			}
			switch {
			case strings.HasPrefix(line, "DATA"):
				_, _ = conn.Write([]byte("354 send content\r\n"))
				return
			default:
				_, _ = conn.Write([]byte("250 ok\r\n"))
			}
		}
	default:
		_, _ = conn.Write([]byte("250 ok\r\n"))
	}
}

func readCommand(conn net.Conn) string {
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return ""
	}
	return strings.TrimSpace(line)
}

func writeRepeat(conn net.Conn, b byte, n int) {
	buf := []byte(strings.Repeat(string([]byte{b}), 4096))
	for n > 0 {
		step := len(buf)
		if step > n {
			step = n
		}
		if _, err := conn.Write(buf[:step]); err != nil {
			return
		}
		n -= step
	}
}

func writeEHLO(conn net.Conn, count, width int) {
	_, _ = conn.Write([]byte("250-adversarial.test\r\n"))
	for i := 0; i < count-1; i++ {
		_, _ = fmt.Fprintf(conn, "250-X%0*d\r\n", width, i)
	}
	_, _ = conn.Write([]byte("250 XFINAL\r\n"))
}
