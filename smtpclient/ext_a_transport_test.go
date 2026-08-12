package smtpclient

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	smtp "github.com/kiliant/go-smtp"
)

func TestTransportMailParametersAndSizePreflight(t *testing.T) {
	size := int64(12)
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250-SIZE 12\r\n", "250-8BITMIME\r\n", "250 SMTPUTF8\r\n")},
		{command: "MAIL FROM:<sender@example.test> SIZE=12 BODY=8BITMIME SMTPUTF8", replies: fakeReplies("250 sender ok\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{Transport: &smtp.TransportOptions{Size: &size, Body: smtp.BodyType8BitMIME, SMTPUTF8: true}}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestTransportSizeOverServerMaximumDoesNotWriteMail(t *testing.T) {
	size := int64(13)
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 SIZE 12\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{Transport: &smtp.TransportOptions{Size: &size}}, nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds server maximum") {
		t.Fatalf("Mail error = %v, want local SIZE maximum error", err)
	}
}

func TestBinaryMIMERejectsDataBeforeWriting(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250-CHUNKING\r\n", "250 BINARYMIME\r\n")},
		{command: "MAIL FROM:<sender@example.test> BODY=BINARYMIME", replies: fakeReplies("250 sender ok\r\n")},
		{command: "RCPT TO:<to@example.test>", replies: fakeReplies("250 recipient ok\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{Transport: &smtp.TransportOptions{Body: smtp.BodyTypeBinaryMIME}}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt(context.Background(), "to@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Data(context.Background(), strings.NewReader("body"), nil); err == nil || !strings.Contains(err.Error(), "requires CHUNKING") {
		t.Fatalf("Data error = %v, want BINARYMIME/CHUNKING error", err)
	}
}

// TestBinaryMIMEStateDoesNotPinAbandonedConnection covers the audit finding
// that BODY=BINARYMIME tracking state lived in a process-global
// map[*Client]bool, cleared only by the next Mail() call or a successful
// final BDAT chunk. A transaction abandoned after the mid-sequence BDAT
// failure path (which does not reach either of those) used to keep its
// *Client — and everything reachable from it, including the connection —
// alive for the process lifetime, because a live map entry is itself a GC
// root reference.
//
// The fix moved the flag onto the connection itself, so it is reclaimed
// with the connection instead of being retained by package-global state.
// This test proves that directly: it abandons a transaction with the flag
// still set and confirms the connection becomes collectible once nothing
// else in the test references it.
func TestBinaryMIMEStateDoesNotPinAbandonedConnection(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250-CHUNKING\r\n", "250 BINARYMIME\r\n")},
		{command: "MAIL FROM:<sender@example.test> BODY=BINARYMIME", replies: fakeReplies("250 sender ok\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{Transport: &smtp.TransportOptions{Body: smtp.BodyTypeBinaryMIME}}, nil); err != nil {
		t.Fatal(err)
	}
	if !binaryMailFor(c) {
		t.Fatal("expected BINARYMIME state to be set before abandoning the transaction")
	}

	collected := make(chan struct{})
	runtime.SetFinalizer(c.conn, func(*connection) { close(collected) })
	// Abandon the transaction: never call Data/BDAT, never call Reset. The
	// only remaining local references are dropped here.
	c = nil

	for i := 0; i < 200; i++ {
		runtime.GC()
		select {
		case <-collected:
			return
		case <-time.After(5 * time.Millisecond):
		}
	}
	t.Fatal("connection was never collected: BINARYMIME state pinned it, which is the leak this test guards against")
}

func TestBDATOpaqueChunksAndZeroLengthTerminator(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan error, 1)
	go func() {
		defer server.Close()
		reader := bufio.NewReader(server)
		write := func(s string) error { _, err := server.Write([]byte(s)); return err }
		readLine := func(want string) error {
			line, err := reader.ReadString('\n')
			if err != nil {
				return err
			}
			if line != want+"\r\n" {
				return fmt.Errorf("command = %q, want %q", line, want+"\r\n")
			}
			return nil
		}
		if err := write("220 fake.test ready\r\n"); err != nil {
			done <- err
			return
		}
		if err := readLine("EHLO client.test"); err != nil {
			done <- err
			return
		}
		if err := write("250-fake.test\r\n250 CHUNKING\r\n"); err != nil {
			done <- err
			return
		}
		if err := readLine("MAIL FROM:<sender@example.test>"); err != nil {
			done <- err
			return
		}
		if err := write("250 sender ok\r\n"); err != nil {
			done <- err
			return
		}
		if err := readLine("RCPT TO:<to@example.test>"); err != nil {
			done <- err
			return
		}
		if err := write("250 recipient ok\r\n"); err != nil {
			done <- err
			return
		}
		if err := readLine("BDAT 4"); err != nil {
			done <- err
			return
		}
		payload := make([]byte, 4)
		if _, err := io.ReadFull(reader, payload); err != nil {
			done <- err
			return
		}
		if string(payload) != ".x\r\n" {
			done <- fmt.Errorf("BDAT payload = %q", payload)
			return
		}
		if err := write("250 chunk ok\r\n"); err != nil {
			done <- err
			return
		}
		if err := readLine("BDAT 0 LAST"); err != nil {
			done <- err
			return
		}
		done <- write("250 queued\r\n")
	}()
	c, err := NewClient(context.Background(), client, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt(context.Background(), "to@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	result, err := c.Data(context.Background(), strings.NewReader(".x\r\n"), &DataOptions{UseChunking: true, ChunkSize: 4})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].Command != "BDAT" || result[0].Code != 250 {
		t.Fatalf("BDAT result = %#v", result)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("fake BDAT server did not finish")
	}
}
