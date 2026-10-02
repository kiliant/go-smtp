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

func TestFetchInboxTreatsUnaddressableExistsAsNotYetVisible(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	done := make(chan error, 1)
	go func() {
		r := bufio.NewReader(server)
		for _, reply := range []string{
			"* 1 EXISTS\r\na1 OK [READ-WRITE] SELECT completed.\r\n",
			"a2 BAD FETCH failed. Invalid messageset.\r\n",
		} {
			if _, err := r.ReadString('\n'); err != nil {
				done <- err
				return
			}
			if _, err := fmt.Fprint(server, reply); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	c := &imapConn{conn: client, r: bufio.NewReader(client)}
	msgs, err := c.fetchInbox(interopRecipient)
	if err != nil {
		t.Fatalf("fetchInbox: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("server: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("messages = %d, want 0 so the caller polls again", len(msgs))
	}
}

func TestFetchInboxReportsOtherBadCompletion(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	go func() {
		r := bufio.NewReader(server)
		_, _ = r.ReadString('\n')
		_, _ = fmt.Fprint(server, "* 1 EXISTS\r\na1 OK SELECT completed.\r\n")
		_, _ = r.ReadString('\n')
		_, _ = fmt.Fprint(server, "a2 BAD Unknown command\r\n")
	}()

	c := &imapConn{conn: client, r: bufio.NewReader(client)}
	if _, err := c.fetchInbox(interopRecipient); err == nil {
		t.Fatal("fetchInbox succeeded; want an error for an unrelated BAD")
	}
}
