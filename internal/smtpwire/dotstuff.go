package smtpwire

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"time"
)

// DotStuffWriter and DotUnstuffReader implement RFC 5321 §4.5.2 transparency
// as streaming, stateful filters — never a transform over a []byte. A 200
// MiB message must not buffer, and correctness must not depend on the
// caller's Write/Read call boundaries lining up with line boundaries: a "."
// arriving as the last byte of one Write and the newline arriving in the
// next is the specific bug that a test written against a single Write call
// never catches, so both types carry state across calls.
//
// Line-boundary decision, applied identically by both types so stuffing and
// unstuffing invert each other exactly: a byte is "at the start of a line"
// if it immediately follows an LF (0x0A), and the stream begins at the
// start of a line. A bare CR is ordinary content and does not, by itself,
// start a new line — only LF does. This means a lone CR is passed through
// unchanged and creates no stuffing opportunity, while a bare LF (with no
// preceding CR) is still honoured as a line boundary; both are deliberate
// leniencies rather than silently malformed output, since real-world
// message sources are not always strictly CRLF-disciplined internally even
// though the wire terminator this package emits always is.

// dotDot is the two bytes written in place of a leading '.' on a stuffed
// line.
var dotDot = []byte("..")

// terminator is the dot-stuffing end-of-content marker.
var terminator = []byte(".\r\n")

// DotStuffWriter dot-stuffs content written to it and writes the result to
// the wrapped io.Writer. Call Close when the message content is complete;
// Close writes the terminating ".CRLF" (adding a CRLF first if the content
// did not already end in one, per "content not ending in CRLF — the
// terminator must still be well formed"). Close does not close the
// underlying writer.
//
// A DotStuffWriter is not safe for concurrent use.
type DotStuffWriter struct {
	w           io.Writer
	atLineStart bool
	wroteAny    bool
	lastByte    byte
	closed      bool
}

// NewDotStuffWriter returns a DotStuffWriter that writes dot-stuffed output
// to w.
func NewDotStuffWriter(w io.Writer) *DotStuffWriter {
	return &DotStuffWriter{w: w, atLineStart: true}
}

// Write dot-stuffs p and writes the result to the underlying writer. It
// never rejects input: any byte sequence is representable in dot-stuffed
// form, so Write only ever fails when the underlying writer does.
//
// On success it returns len(p), nil, matching the io.Writer contract that a
// short count implies a non-nil error — the extra bytes inserted for
// stuffing are not part of that count, since they are not part of what the
// caller asked to write.
func (d *DotStuffWriter) Write(p []byte) (int, error) {
	if d.closed {
		return 0, errors.New("smtpwire: write to closed DotStuffWriter")
	}
	consumed := 0
	for len(p) > 0 {
		if d.atLineStart && p[0] == '.' {
			if _, err := d.w.Write(dotDot); err != nil {
				return consumed, err
			}
			d.atLineStart = false
			d.wroteAny = true
			d.lastByte = '.'
			p = p[1:]
			consumed++
			continue
		}
		nl := bytes.IndexByte(p, '\n')
		if nl == -1 {
			if _, err := d.w.Write(p); err != nil {
				return consumed, err
			}
			d.atLineStart = false
			d.wroteAny = true
			d.lastByte = p[len(p)-1]
			consumed += len(p)
			p = nil
			break
		}
		chunk := p[:nl+1]
		if _, err := d.w.Write(chunk); err != nil {
			return consumed, err
		}
		d.atLineStart = true
		d.wroteAny = true
		d.lastByte = '\n'
		consumed += len(chunk)
		p = p[nl+1:]
	}
	return consumed, nil
}

// Close writes the end-of-content terminator: a CRLF first if the content
// written so far did not already end with one (so the terminator itself is
// always well formed regardless of what the caller supplied), then ".\r\n".
// It does not close the underlying writer. Close is idempotent-safe to call
// once; calling it twice returns an error rather than writing the
// terminator twice.
func (d *DotStuffWriter) Close() error {
	if d.closed {
		return errors.New("smtpwire: DotStuffWriter already closed")
	}
	d.closed = true
	if d.wroteAny && d.lastByte != '\n' {
		if _, err := d.w.Write([]byte("\r\n")); err != nil {
			return err
		}
	}
	_, err := d.w.Write(terminator)
	return err
}

// ErrMalformedTerminator is returned by DotUnstuffReader when a line begins
// with a '.' immediately followed by a CR that is not itself followed by an
// LF — a malformed terminator attempt.
var ErrMalformedTerminator = errors.New("smtpwire: malformed dot-stuffing terminator")

// DotUnstuffReader reverses DotStuffWriter's transform, reading dot-stuffed
// content from a LineReader and presenting the original content through
// Read. It stops at the ".CRLF" terminator without emitting it, and returns
// io.EOF from that point on (matching io.Reader's contract for reads after
// the logical end of the stream).
//
// It reads from the *bufio.Reader owned by a LineReader — the same one
// ReadReply uses — rather than owning its own buffering, so no bytes
// buffered ahead while reading the preceding 354 reply are lost, and so the
// caller can apply the same read-deadline mechanism used for replies to the
// message body (a server that sends 354 and then stalls must time out).
//
// A DotUnstuffReader is not safe for concurrent use.
type DotUnstuffReader struct {
	lr          *LineReader
	atLineStart bool
	done        bool
	err         error
}

// NewDotUnstuffReader returns a DotUnstuffReader reading from lr.
func NewDotUnstuffReader(lr *LineReader) *DotUnstuffReader {
	return &DotUnstuffReader{lr: lr, atLineStart: true}
}

// SetDeadline applies t to the underlying reader when it is deadline-capable
// (see LineReader.setDeadline); a zero t is a no-op.
func (d *DotUnstuffReader) SetDeadline(t time.Time) error {
	return d.lr.setDeadline(t)
}

// Read implements io.Reader. It only ever buffers O(1) state (the
// line-start flag and a 1-byte peek at a possible terminator) regardless of
// line length, so it never buffers the message it is unstuffing.
func (d *DotUnstuffReader) Read(p []byte) (int, error) {
	if d.done {
		if d.err != nil {
			return 0, d.err
		}
		return 0, io.EOF
	}
	if len(p) == 0 {
		return 0, nil
	}

	br := d.lr.br
	n := 0
	for n < len(p) {
		if d.atLineStart {
			b, err := br.ReadByte()
			if err != nil {
				return n, d.fail(n, normalizeReadErr(err))
			}
			if b == '.' {
				peek, err := br.Peek(1)
				if err != nil {
					return n, d.fail(n, fmt.Errorf("smtpwire: truncated dot-stuffed stream after '.': %w", normalizeReadErr(err)))
				}
				if peek[0] == '\r' || peek[0] == '\n' {
					if tailErr := d.consumeTerminatorTail(); tailErr != nil {
						return n, d.fail(n, tailErr)
					}
					return n, d.fail(n, io.EOF)
				}
				// Stuffed line: drop the leading '.', the rest of the line
				// is ordinary content.
				d.atLineStart = false
				continue
			}
			p[n] = b
			n++
			d.atLineStart = b == '\n'
			continue
		}
		b, err := br.ReadByte()
		if err != nil {
			return n, d.fail(n, normalizeReadErr(err))
		}
		p[n] = b
		n++
		d.atLineStart = b == '\n'
	}
	return n, nil
}

// fail records the terminal state (err, possibly io.EOF for the clean case)
// and returns nil when the caller already has n>0 bytes to deliver this
// call (the error is reported on the next Read, per common io.Reader
// practice), or the error itself when n==0.
func (d *DotUnstuffReader) fail(n int, err error) error {
	d.done = true
	d.err = err
	if n > 0 {
		return nil
	}
	return err
}

// consumeTerminatorTail consumes the remainder of a terminator line after a
// '.' has been read and peeking confirmed the next byte is CR or LF. It
// accepts both "\r\n" and a lenient bare "\n"; a CR not followed by LF is
// ErrMalformedTerminator.
func (d *DotUnstuffReader) consumeTerminatorTail() error {
	br := d.lr.br
	b, err := br.ReadByte()
	if err != nil {
		return normalizeReadErr(err)
	}
	switch b {
	case '\n':
		return nil
	case '\r':
		b2, err := br.ReadByte()
		if err != nil {
			return normalizeReadErr(err)
		}
		if b2 != '\n' {
			return ErrMalformedTerminator
		}
		return nil
	default:
		return ErrMalformedTerminator
	}
}
