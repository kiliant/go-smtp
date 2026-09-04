//go:build interop

package interop

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSeedMessageUsesPerCommandTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	const (
		commandTimeout = 1200 * time.Millisecond
		replyDelay     = 250 * time.Millisecond
	)
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- serveSlowSMTP(listener, replyDelay)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body := []byte("From: interop@example.test\r\nTo: interop@example.test\r\n\r\nslow replies\r\n")
	if err := seedMessage(ctx, commandTimeout, listener.Addr().String(), "interop@example.test", body, false); err != nil {
		t.Fatalf("seedMessage failed even though every command completed within its timeout: %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}

func serveSlowSMTP(listener net.Listener, delay time.Duration) error {
	conn, err := listener.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	reader := bufio.NewReader(conn)

	if err := slowReply(conn, delay, "220 slow.example.test ESMTP ready\r\n"); err != nil {
		return err
	}
	if err := expectCommand(reader, "EHLO "); err != nil {
		return err
	}
	if err := slowReply(conn, delay, "250 slow.example.test\r\n"); err != nil {
		return err
	}
	if err := expectCommand(reader, "MAIL FROM:"); err != nil {
		return err
	}
	if err := slowReply(conn, delay, "250 sender accepted\r\n"); err != nil {
		return err
	}
	if err := expectCommand(reader, "RCPT TO:"); err != nil {
		return err
	}
	if err := slowReply(conn, delay, "250 recipient accepted\r\n"); err != nil {
		return err
	}
	if err := expectCommand(reader, "DATA"); err != nil {
		return err
	}
	if err := slowReply(conn, delay, "354 continue\r\n"); err != nil {
		return err
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if line == ".\r\n" {
			break
		}
	}
	if err := slowReply(conn, delay, "250 queued\r\n"); err != nil {
		return err
	}
	if err := expectCommand(reader, "QUIT"); err != nil {
		return err
	}
	return slowReply(conn, delay, "221 bye\r\n")
}

func slowReply(conn net.Conn, delay time.Duration, reply string) error {
	time.Sleep(delay)
	_, err := fmt.Fprint(conn, reply)
	return err
}

func expectCommand(reader *bufio.Reader, prefix string) error {
	line, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.HasPrefix(line, prefix) {
		return fmt.Errorf("command %q does not begin with %q", strings.TrimSpace(line), prefix)
	}
	return nil
}
