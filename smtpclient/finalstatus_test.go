package smtpclient

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	smtp "github.com/kiliant/go-smtp"
)

// scriptPeer is a byte-level scripted server for the final-status tests. Unlike
// startFakeServer it can stop reading partway through a command or a chunk,
// which is what places a failure exactly before, inside or after the
// content-completion operation.
type scriptPeer struct {
	t    *testing.T
	conn net.Conn
	r    *bufio.Reader
}

func startScriptPeer(t *testing.T, script func(p *scriptPeer)) (net.Conn, func()) {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer server.Close()
		p := &scriptPeer{t: t, conn: server, r: bufio.NewReader(server)}
		p.send("220 fake.test ready\r\n")
		script(p)
	}()
	return client, func() {
		t.Helper()
		_ = client.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("script peer did not finish")
		}
	}
}

func (p *scriptPeer) send(lines ...string) {
	for _, line := range lines {
		if _, err := p.conn.Write([]byte(line)); err != nil {
			p.t.Errorf("script peer writing %q: %v", line, err)
			return
		}
	}
}

func (p *scriptPeer) expect(command string) {
	line, err := p.r.ReadString('\n')
	if err != nil {
		p.t.Errorf("script peer reading %q: %v", command, err)
		return
	}
	if line != command+"\r\n" {
		p.t.Errorf("script peer command = %q, want %q", line, command+"\r\n")
	}
}

// readThrough consumes bytes until the data read ends with marker.
func (p *scriptPeer) readThrough(marker string) {
	var seen []byte
	for !bytes.HasSuffix(seen, []byte(marker)) {
		b, err := p.r.ReadByte()
		if err != nil {
			p.t.Errorf("script peer reading through %q: %v (read %q)", marker, err, seen)
			return
		}
		seen = append(seen, b)
	}
}

// readBytes consumes exactly n bytes straight from the connection, bypassing
// the buffered reader, so the client's write blocks partway through instead of
// completing into the peer's buffer. Closing afterwards then fails that write.
func (p *scriptPeer) readBytes(n int) {
	if buffered := p.r.Buffered(); buffered != 0 {
		p.t.Errorf("script peer has %d buffered bytes before an exact read", buffered)
		return
	}
	one := make([]byte, 1)
	for range n {
		if _, err := p.conn.Read(one); err != nil {
			p.t.Errorf("script peer reading %d bytes: %v", n, err)
			return
		}
	}
}

// drain reads until the client closes its end, modelling a peer that never
// replies.
func (p *scriptPeer) drain() { _, _ = io.Copy(io.Discard, p.r) }

// open runs the greeting exchange and a transaction for recipients, all
// accepted.
func (p *scriptPeer) open(lmtp bool, extensions []string, recipients ...string) {
	hello := "EHLO client.test"
	if lmtp {
		hello = "LHLO client.test"
	}
	p.expect(hello)
	lines := append([]string{"fake.test"}, extensions...)
	for i, line := range lines {
		sep := "-"
		if i == len(lines)-1 {
			sep = " "
		}
		p.send("250" + sep + line + "\r\n")
	}
	p.expect("MAIL FROM:<sender@example.test>")
	p.send("250 sender ok\r\n")
	for _, recipient := range recipients {
		p.expect("RCPT TO:<" + recipient + ">")
		p.send("250 recipient ok\r\n")
	}
}

func openScriptedTransaction(t *testing.T, raw net.Conn, lmtp bool, recipients ...string) *Client {
	t.Helper()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test", LMTP: lmtp})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, recipient := range recipients {
		if err := c.Rcpt(context.Background(), recipient, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

// requireFinalStatusUnknown checks the whole contract: the top-level error is
// an *smtp.Error without a reply code, it carries the sentinel, and cause (if
// any) is still reachable.
func requireFinalStatusUnknown(t *testing.T, err error, command string, cause error) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want ErrFinalStatusUnknown")
	}
	smtpErr, ok := err.(*smtp.Error)
	if !ok {
		t.Fatalf("error type = %T (%v), want *smtp.Error", err, err)
	}
	if smtpErr.Code != 0 || smtpErr.Command != command {
		t.Errorf("error code/command = %d/%q, want 0/%q: %v", smtpErr.Code, smtpErr.Command, command, err)
	}
	if !errors.Is(err, ErrFinalStatusUnknown) {
		t.Errorf("errors.Is(%v, ErrFinalStatusUnknown) = false", err)
	}
	if cause != nil && !errors.Is(err, cause) {
		t.Errorf("errors.Is(%v, %v) = false; the original cause must stay reachable", err, cause)
	}
}

func requireFinalStatusKnown(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want a failure")
	}
	if errors.Is(err, ErrFinalStatusUnknown) {
		t.Errorf("errors.Is(%v, ErrFinalStatusUnknown) = true for a failure that cannot have completed the transaction or that has an authoritative reply", err)
	}
}

func TestDataFinalStatusUnknownAfterTerminator(t *testing.T) {
	cases := []struct {
		name    string
		timeout time.Duration
		after   func(p *scriptPeer)
		cause   error
	}{
		{"connection closed before final reply", 0, func(p *scriptPeer) {}, nil},
		{"malformed final reply", 0, func(p *scriptPeer) { p.send("garbage\r\n") }, nil},
		{"truncated multiline final reply", 0, func(p *scriptPeer) { p.send("250-partial\r\n") }, io.ErrUnexpectedEOF},
		// The deadline races the read deadline it sets, so the cause may be
		// either; TestDataFinalStatusUnknownOnCancelAfterTerminator pins it.
		{"context deadline while waiting", 50 * time.Millisecond, func(p *scriptPeer) { p.drain() }, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, done := startScriptPeer(t, func(p *scriptPeer) {
				p.open(false, nil, "one@example.test")
				p.expect("DATA")
				p.send("354 go ahead\r\n")
				p.readThrough("hello\r\n.\r\n")
				tc.after(p)
			})
			defer done()
			c := openScriptedTransaction(t, raw, false, "one@example.test")
			ctx := context.Background()
			if tc.timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.timeout)
				defer cancel()
			}
			got, err := c.Data(ctx, strings.NewReader("hello\r\n"), nil)
			if got != nil {
				t.Errorf("result = %#v, want nil", got)
			}
			requireFinalStatusUnknown(t, err, "DATA", tc.cause)
		})
	}
}

func TestDataFinalStatusUnknownOnCancelAfterTerminator(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	raw, done := startScriptPeer(t, func(p *scriptPeer) {
		p.open(false, nil, "one@example.test")
		p.expect("DATA")
		p.send("354 go ahead\r\n")
		p.readThrough("hello\r\n.\r\n")
		cancel()
		p.drain()
	})
	defer done()
	c := openScriptedTransaction(t, raw, false, "one@example.test")
	_, err := c.Data(ctx, strings.NewReader("hello\r\n"), nil)
	requireFinalStatusUnknown(t, err, "DATA", context.Canceled)
}

func TestDataFinalStatusUnknownWhenTerminatorWriteFails(t *testing.T) {
	raw, done := startScriptPeer(t, func(p *scriptPeer) {
		p.open(false, nil, "one@example.test")
		p.expect("DATA")
		p.send("354 go ahead\r\n")
		p.readThrough("hello\r\n")
		p.readBytes(1) // one byte of ".\r\n", then the connection drops
	})
	defer done()
	c := openScriptedTransaction(t, raw, false, "one@example.test")
	_, err := c.Data(context.Background(), strings.NewReader("hello\r\n"), nil)
	requireFinalStatusUnknown(t, err, "DATA", io.ErrClosedPipe)
}

func TestDataFinalStatusKnown(t *testing.T) {
	t.Run("authoritative transient final reply", func(t *testing.T) {
		raw, done := startScriptPeer(t, func(p *scriptPeer) {
			p.open(false, nil, "one@example.test")
			p.expect("DATA")
			p.send("354 go ahead\r\n")
			p.readThrough("\r\n.\r\n")
			p.send("451 try later\r\n")
		})
		defer done()
		c := openScriptedTransaction(t, raw, false, "one@example.test")
		_, err := c.Data(context.Background(), strings.NewReader("hello\r\n"), nil)
		requireFinalStatusKnown(t, err)
		var smtpErr *smtp.Error
		if !errors.As(err, &smtpErr) || smtpErr.Code != 451 {
			t.Fatalf("error = %v, want 451", err)
		}
	})
	t.Run("authoritative 421 final reply", func(t *testing.T) {
		raw, done := startScriptPeer(t, func(p *scriptPeer) {
			p.open(false, nil, "one@example.test")
			p.expect("DATA")
			p.send("354 go ahead\r\n")
			p.readThrough("\r\n.\r\n")
			p.send("421 closing\r\n")
		})
		defer done()
		c := openScriptedTransaction(t, raw, false, "one@example.test")
		_, err := c.Data(context.Background(), strings.NewReader("hello\r\n"), nil)
		requireFinalStatusKnown(t, err)
	})
	t.Run("connection lost during content", func(t *testing.T) {
		raw, done := startScriptPeer(t, func(p *scriptPeer) {
			p.open(false, nil, "one@example.test")
			p.expect("DATA")
			p.send("354 go ahead\r\n")
			p.readBytes(10)
		})
		defer done()
		c := openScriptedTransaction(t, raw, false, "one@example.test")
		_, err := c.Data(context.Background(), strings.NewReader(strings.Repeat("x", 64*1024)+"\r\n"), nil)
		requireFinalStatusKnown(t, err)
	})
	t.Run("content reader error", func(t *testing.T) {
		readErr := errors.New("source failed")
		raw, done := startScriptPeer(t, func(p *scriptPeer) {
			p.open(false, nil, "one@example.test")
			p.expect("DATA")
			p.send("354 go ahead\r\n")
			p.drain()
		})
		defer done()
		c := openScriptedTransaction(t, raw, false, "one@example.test")
		_, err := c.Data(context.Background(), io.MultiReader(strings.NewReader("partial"), &failingReader{err: readErr}), nil)
		requireFinalStatusKnown(t, err)
		if !errors.Is(err, readErr) {
			t.Errorf("errors.Is(%v, source error) = false", err)
		}
	})
	t.Run("context cancelled during content", func(t *testing.T) {
		raw, done := startScriptPeer(t, func(p *scriptPeer) {
			p.open(false, nil, "one@example.test")
			p.expect("DATA")
			p.send("354 go ahead\r\n")
			p.drain()
		})
		defer done()
		c := openScriptedTransaction(t, raw, false, "one@example.test")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		// Cancellation may surface as the context error or as the write
		// failure caused by the cancel watcher closing the connection; either
		// way the terminator was never sent.
		_, err := c.Data(ctx, &cancellingReader{cancel: cancel, data: []byte("hello\r\n")}, nil)
		requireFinalStatusKnown(t, err)
	})
}

type failingReader struct{ err error }

func (r *failingReader) Read([]byte) (int, error) { return 0, r.err }

// cancellingReader returns its data once and cancels the context, so the next
// loop iteration sees cancellation before the terminator is written.
type cancellingReader struct {
	cancel context.CancelFunc
	data   []byte
}

func (r *cancellingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	r.cancel()
	return n, nil
}

var chunking = []string{"CHUNKING"}

func TestBDATFinalStatus(t *testing.T) {
	opts := &DataOptions{UseChunking: true, ChunkSize: 4}
	cases := []struct {
		name    string
		after   func(p *scriptPeer)
		unknown bool
		cause   error
	}{
		{"non-LAST chunk write fails", func(p *scriptPeer) { p.readBytes(3) }, false, nil},
		{"non-LAST chunk reply lost", func(p *scriptPeer) { p.readThrough("BDAT 4\r\nabcd") }, false, nil},
		{"LAST frame write fails", func(p *scriptPeer) {
			p.readThrough("BDAT 4\r\nabcd")
			p.send("250 chunk ok\r\n")
			p.readBytes(3)
		}, true, io.ErrClosedPipe},
		{"LAST frame reply lost", func(p *scriptPeer) {
			p.readThrough("BDAT 4\r\nabcd")
			p.send("250 chunk ok\r\n")
			p.readThrough("BDAT 2 LAST\r\nef")
		}, true, nil},
		{"LAST frame authoritative rejection", func(p *scriptPeer) {
			p.readThrough("BDAT 4\r\nabcd")
			p.send("250 chunk ok\r\n")
			p.readThrough("BDAT 2 LAST\r\nef")
			p.send("554 rejected\r\n")
		}, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, done := startScriptPeer(t, func(p *scriptPeer) {
				p.open(false, chunking, "one@example.test")
				tc.after(p)
			})
			defer done()
			c := openScriptedTransaction(t, raw, false, "one@example.test")
			got, err := c.Data(context.Background(), strings.NewReader("abcdef"), opts)
			if got != nil {
				t.Errorf("result = %#v, want nil", got)
			}
			if tc.unknown {
				requireFinalStatusUnknown(t, err, "BDAT", tc.cause)
			} else {
				requireFinalStatusKnown(t, err)
			}
		})
	}
}

func TestLMTPFinalStatusUnknownReturnsAuthoritativePrefix(t *testing.T) {
	recipients := []string{"one@example.test", "two@example.test", "three@example.test"}
	for _, chunked := range []bool{false, true} {
		for received := 0; received < len(recipients); received++ {
			name := fmt.Sprintf("DATA/%d of %d", received, len(recipients))
			command := "DATA"
			if chunked {
				name = fmt.Sprintf("BDAT/%d of %d", received, len(recipients))
				command = "BDAT"
			}
			t.Run(name, func(t *testing.T) {
				raw, done := startScriptPeer(t, func(p *scriptPeer) {
					var ext []string
					if chunked {
						ext = chunking
					}
					p.open(true, ext, recipients...)
					if chunked {
						p.readThrough("BDAT 5 LAST\r\nhello")
					} else {
						p.expect("DATA")
						p.send("354 go ahead\r\n")
						p.readThrough("hello\r\n.\r\n")
					}
					for i := range received {
						p.send(fmt.Sprintf("%d reply %d\r\n", 250+i, i))
					}
				})
				defer done()
				c := openScriptedTransaction(t, raw, true, recipients...)
				var opts *DataOptions
				content := "hello\r\n"
				if chunked {
					opts = &DataOptions{UseChunking: true}
					content = "hello"
				}
				got, err := c.Data(context.Background(), strings.NewReader(content), opts)
				requireFinalStatusUnknown(t, err, command, nil)
				if received == 0 {
					if got != nil {
						t.Fatalf("result = %#v, want nil before the first reply", got)
					}
					return
				}
				if len(got) != received {
					t.Fatalf("result length = %d, want the %d replies received: %#v", len(got), received, got)
				}
				for i, r := range got {
					if r.Recipient != recipients[i] || r.Code != 250+i || r.Text != fmt.Sprintf("reply %d", i) || r.Command != command {
						t.Errorf("result[%d] = %#v, want %s's authoritative reply", i, r, recipients[i])
					}
				}
			})
		}
	}
}

func TestLMTPExtraFinalReplyIsFinalStatusUnknown(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		command := "DATA"
		if chunked {
			command = "BDAT"
		}
		t.Run(command, func(t *testing.T) {
			raw, done := startScriptPeer(t, func(p *scriptPeer) {
				var ext []string
				if chunked {
					ext = chunking
				}
				p.open(true, ext, "one@example.test")
				if chunked {
					p.readThrough("BDAT 5 LAST\r\nhello")
				} else {
					p.expect("DATA")
					p.send("354 go ahead\r\n")
					p.readThrough("\r\n.\r\n")
				}
				p.send("250 one delivered\r\n", "250 hostile extra\r\n")
				p.drain()
			})
			defer done()
			c := openScriptedTransaction(t, raw, true, "one@example.test")
			var opts *DataOptions
			content := "hello\r\n"
			if chunked {
				opts = &DataOptions{UseChunking: true}
				content = "hello"
			}
			got, err := c.Data(context.Background(), strings.NewReader(content), opts)
			requireFinalStatusUnknown(t, err, command, nil)
			if got != nil {
				t.Errorf("result = %#v, want nil: replies cannot be attributed once the count is wrong", got)
			}
		})
	}
}

var burlExtensions = []string{"BURL imap"}

const burlURL = "imap://example.test/inbox/;uid=1"

func TestBURLFinalStatus(t *testing.T) {
	cases := []struct {
		name    string
		last    bool
		after   func(p *scriptPeer)
		unknown bool
		cause   error
	}{
		{"LAST reply lost", true, func(p *scriptPeer) { p.expect("BURL " + burlURL + " LAST") }, true, nil},
		{"LAST write fails", true, func(p *scriptPeer) { p.readBytes(4) }, true, io.ErrClosedPipe},
		{"LAST authoritative rejection", true, func(p *scriptPeer) {
			p.expect("BURL " + burlURL + " LAST")
			p.send("554 cannot fetch\r\n")
		}, false, nil},
		{"non-LAST reply lost", false, func(p *scriptPeer) { p.expect("BURL " + burlURL) }, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, done := startScriptPeer(t, func(p *scriptPeer) {
				p.open(false, burlExtensions, "one@example.test")
				tc.after(p)
			})
			defer done()
			c := openScriptedTransaction(t, raw, false, "one@example.test")
			got, err := c.BURL(context.Background(), burlURL, &BURLOptions{Last: tc.last})
			if got != nil {
				t.Errorf("result = %#v, want nil", got)
			}
			if tc.unknown {
				requireFinalStatusUnknown(t, err, "BURL", tc.cause)
			} else {
				requireFinalStatusKnown(t, err)
			}
		})
	}
}

func TestBURLLastFailureBeforeWriteIsNotFinalStatusUnknown(t *testing.T) {
	raw, done := startScriptPeer(t, func(p *scriptPeer) {
		p.open(false, burlExtensions, "one@example.test")
		p.drain()
	})
	defer done()
	c := openScriptedTransaction(t, raw, false, "one@example.test")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.BURL(ctx, burlURL, nil)
	requireFinalStatusKnown(t, err)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("errors.Is(%v, context.Canceled) = false", err)
	}
}

func TestFinalStatusUnknownWrapping(t *testing.T) {
	if got := finalStatusUnknown("DATA", nil); got != nil {
		t.Errorf("finalStatusUnknown(nil) = %v, want nil", got)
	}
	reply := &smtp.Error{Code: 554, Command: "DATA", Text: "rejected"}
	if got := finalStatusUnknown("DATA", reply); got != error(reply) {
		t.Errorf("finalStatusUnknown(reply) = %v, want the reply unchanged", got)
	}
	cause := errors.New("wire broke")
	transport := &smtp.Error{Command: "BDAT", Err: cause}
	got := finalStatusUnknown("DATA", transport)
	requireFinalStatusUnknown(t, got, "BDAT", cause)
	if transport.Err != cause {
		t.Error("finalStatusUnknown mutated its argument")
	}
	if again := finalStatusUnknown("DATA", got); again != got {
		t.Error("finalStatusUnknown wrapped an already classified error twice")
	}
	if want := "BDAT: " + ErrFinalStatusUnknown.Error() + ": wire broke"; got.Error() != want {
		t.Errorf("Error() = %q, want %q", got.Error(), want)
	}
}

// A session-level 421 inside an RFC 2033 reply stream answers no recipient:
// the replies before it stay authoritative, and the rest have unknown status.
func TestLMTP421MidStreamIsFinalStatusUnknown(t *testing.T) {
	recipients := []string{"one@example.test", "two@example.test", "three@example.test"}
	for _, chunked := range []bool{false, true} {
		for _, received := range []int{0, 1, len(recipients) - 1} {
			command := "DATA"
			if chunked {
				command = "BDAT"
			}
			t.Run(fmt.Sprintf("%s/421 after %d", command, received), func(t *testing.T) {
				raw, done := startScriptPeer(t, func(p *scriptPeer) {
					var ext []string
					if chunked {
						ext = chunking
					}
					p.open(true, ext, recipients...)
					if chunked {
						p.readThrough("BDAT 5 LAST\r\nhello")
					} else {
						p.expect("DATA")
						p.send("354 go ahead\r\n")
						p.readThrough("hello\r\n.\r\n")
					}
					for i := range received {
						p.send(fmt.Sprintf("250 reply %d\r\n", i))
					}
					p.send("421 shutting down\r\n")
				})
				defer done()
				c := openScriptedTransaction(t, raw, true, recipients...)
				var opts *DataOptions
				content := "hello\r\n"
				if chunked {
					opts = &DataOptions{UseChunking: true}
					content = "hello"
				}
				got, err := c.Data(context.Background(), strings.NewReader(content), opts)
				if !errors.Is(err, ErrFinalStatusUnknown) {
					t.Fatalf("error = %v, want ErrFinalStatusUnknown: recipients after the 421 may have been delivered", err)
				}
				var smtpErr *smtp.Error
				if !errors.As(err, &smtpErr) || smtpErr.Code != 421 || !smtpErr.IsTransient() || smtpErr.Text != "shutting down" {
					t.Errorf("error = %#v, want the 421 reply preserved", smtpErr)
				}
				if len(got) != received || (received == 0 && got != nil) {
					t.Fatalf("result = %#v, want the %d replies before the 421", got, received)
				}
				for i, r := range got {
					if r.Recipient != recipients[i] || r.Code != 250 {
						t.Errorf("result[%d] = %#v, want %s's 250", i, r, recipients[i])
					}
				}
			})
		}
	}
}

func TestSMTPBDAT421FinalReplyIsAuthoritative(t *testing.T) {
	raw, done := startScriptPeer(t, func(p *scriptPeer) {
		p.open(false, chunking, "one@example.test")
		p.readThrough("BDAT 5 LAST\r\nhello")
		p.send("421 closing\r\n")
	})
	defer done()
	c := openScriptedTransaction(t, raw, false, "one@example.test")
	_, err := c.Data(context.Background(), strings.NewReader("hello"), &DataOptions{UseChunking: true})
	requireFinalStatusKnown(t, err)
}

// The extra-reply probe cannot tell a clean close after the last reply from a
// truncated extra reply. The conservative classification is pinned here; a
// later release may return the full result instead (see ErrFinalStatusUnknown).
func TestLMTPCloseAfterLastReplyIsFinalStatusUnknown(t *testing.T) {
	raw, done := startScriptPeer(t, func(p *scriptPeer) {
		p.open(true, nil, "one@example.test")
		p.expect("DATA")
		p.send("354 go ahead\r\n")
		p.readThrough("\r\n.\r\n")
		p.send("250 one delivered\r\n")
	})
	defer done()
	c := openScriptedTransaction(t, raw, true, "one@example.test")
	got, err := c.Data(context.Background(), strings.NewReader("hello\r\n"), nil)
	requireFinalStatusUnknown(t, err, "DATA", nil)
	if got != nil {
		t.Errorf("result = %#v, want nil", got)
	}
}

func TestBURLOverlongCommandIsRejectedBeforeWrite(t *testing.T) {
	raw, done := startScriptPeer(t, func(p *scriptPeer) {
		p.open(false, burlExtensions, "one@example.test")
		// Nothing of the over-long command may reach the wire: the next
		// line must be the NOOP that proves the session is still usable.
		p.expect("NOOP")
		p.send("250 still usable\r\n")
	})
	defer done()
	c := openScriptedTransaction(t, raw, false, "one@example.test")
	_, err := c.BURL(context.Background(), "imap://example.test/"+strings.Repeat("a", 70*1024), nil)
	requireFinalStatusKnown(t, err)
	if err := c.Noop(context.Background(), nil); err != nil {
		t.Errorf("NOOP after a locally rejected BURL: %v", err)
	}
}
