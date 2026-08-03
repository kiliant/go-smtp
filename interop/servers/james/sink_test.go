package james

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"testing"
)

func TestIMAPCommandPreservesLiteralAndTracksExists(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	want := []byte("Subject: binary\r\n\r\nbody\x00with\r\nlines")
	done := make(chan error, 1)
	go func() {
		r := bufio.NewReader(server)
		line, err := r.ReadString('\n')
		if err != nil {
			done <- err
			return
		}
		if line != "a1 FETCH 1:* BODY.PEEK[]\r\n" {
			done <- fmt.Errorf("command = %q", line)
			return
		}
		_, err = fmt.Fprintf(server, "* 2 EXISTS\r\n* 1 FETCH (BODY[] {%d}\r\n%s)\r\na1 OK FETCH completed\r\n", len(want), want)
		done <- err
	}()

	c := &imapConn{conn: client, r: bufio.NewReader(client)}
	status, literals, err := c.command("FETCH 1:* BODY.PEEK[]")
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("server: %v", err)
	}
	if status != "OK" {
		t.Fatalf("status = %q, want OK", status)
	}
	if c.exists != 2 {
		t.Fatalf("exists = %d, want 2", c.exists)
	}
	if len(literals) != 1 || !bytes.Equal(literals[0], want) {
		t.Fatalf("literals = %q, want [%q]", literals, want)
	}
}

func TestIMAPCommandReportsBadCompletion(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() {
		r := bufio.NewReader(server)
		_, _ = r.ReadString('\n')
		_, _ = fmt.Fprint(server, "a1 BAD invalid sequence set\r\n")
	}()

	c := &imapConn{conn: client, r: bufio.NewReader(client)}
	status, _, err := c.command("FETCH 1:* BODY.PEEK[]")
	if status != "BAD" || err == nil {
		t.Fatalf("status, error = %q, %v; want BAD and diagnostic error", status, err)
	}
}
