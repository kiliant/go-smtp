package smtpserver

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// FuzzServerTranscript exercises framing through the public server boundary,
// rather than invoking the wire parsers in isolation. The seeds cover command
// decoding, paths, DATA transparency, and BDAT's exact-octet boundary.
func FuzzServerTranscript(f *testing.F) {
	for _, seed := range [][]byte{
		{},
		[]byte("NOOP\r\nQUIT\r\n"),
		[]byte("EHLO client.example\r\nMAIL FROM:<sender@example.test>\r\nRCPT TO:<recipient@example.test>\r\nDATA\r\nhello\r\n.\r\nQUIT\r\n"),
		[]byte("EHLO client.example\r\nMAIL FROM:<sender@example.test>\r\nRCPT TO:<recipient@example.test>\r\nBDAT 3 LAST\r\nabcQUIT\r\n"),
		// Published SMTP-smuggling boundary-confusion families: LF-dot-LF,
		// CR-dot-CRLF, and CRLF-dot-CR.
		[]byte("EHLO client.example\r\nMAIL FROM:<sender@example.test>\r\nRCPT TO:<recipient@example.test>\r\nDATA\r\na\n.\nNOOP\r\n"),
		[]byte("EHLO client.example\r\nMAIL FROM:<sender@example.test>\r\nRCPT TO:<recipient@example.test>\r\nDATA\r\na\r.\r\nNOOP\r\n.\r\n"),
		[]byte("EHLO client.example\r\nMAIL FROM:<sender@example.test>\r\nRCPT TO:<recipient@example.test>\r\nDATA\r\na\r\n.\rNOOP\r\n"),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, transcript []byte) {
		if len(transcript) > 16<<10 {
			return
		}
		fuzzServeTranscript(t, transcript)
	})
}

func fuzzServeTranscript(t *testing.T, transcript []byte) {
	t.Helper()
	listener, clientConn := newPipeListener()
	panicReported := make(chan error, 1)
	opts := &ServerOptions{
		Listener:         listener,
		Backend:          newCommandTestBackend(ModeSMTP).backend(),
		GreetingIdentity: "server.example",
		CommandTimeout:   100 * time.Millisecond,
		DataTimeout:      100 * time.Millisecond,
		MaxMessageBytes:  16 << 10,
		MaxRecipients:    100,
		ErrorLog: func(event ErrorEvent) {
			if strings.Contains(event.Err.Error(), "panic") {
				select {
				case panicReported <- event.Err:
				default:
				}
			}
		},
	}
	enableTestChunking(opts)
	server, err := NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(ctx, nil) }()
	readDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(io.Discard, clientConn)
		close(readDone)
	}()

	if err := clientConn.SetWriteDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	_, _ = clientConn.Write(transcript)
	_ = clientConn.Close()

	select {
	case <-readDone:
	case <-time.After(time.Second):
		t.Fatal("server connection did not close")
	}
	cancel()
	select {
	case err := <-serveDone:
		if err != nil && !isExpectedFuzzServeError(err) {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve did not stop")
	}
	select {
	case err := <-panicReported:
		t.Fatalf("server recovered panic: %v", err)
	default:
	}
}

func isExpectedFuzzServeError(err error) bool {
	return err == nil ||
		strings.Contains(err.Error(), context.Canceled.Error()) ||
		strings.Contains(err.Error(), context.DeadlineExceeded.Error()) ||
		strings.Contains(err.Error(), net.ErrClosed.Error())
}
