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
// # Line endings: strict on send, lenient on receive
//
// DotStuffWriter normalises caller content to CRLF: a bare CR or a bare LF
// in the content becomes a CRLF line terminator on the wire. RFC 5321 §2.3.8
// requires it —
//
//	SMTP client implementations MUST NOT transmit these characters
//	except when they are intended as line terminators and then MUST, as
//	indicated above, transmit them only as a <CRLF> sequence.
//
// — and it is also what makes stuffing safe. Line starts are then defined by
// CRLF alone, so every line start is a stuffing opportunity and there is no
// byte sequence a receiver could read as a line start that this writer did
// not stuff.
//
// Forwarding bare CR/LF instead is not merely non-conforming, it is an
// SMTP-smuggling vector: a filter that honours only one of the two as a line
// boundary stuffs one of "<LF>.<LF>" and "<CR>.<CR><LF>" and leaves the other
// unstuffed, and a receiver that disagrees about which is a terminator then
// sees an early end-of-data with attacker-chosen SMTP commands after it. The
// normalisation removes the disagreement rather than picking a side of it.
//
// Normalisation applies to DATA content only. BDAT content does not pass
// through this filter at all — see CopyBDATChunk, which copies opaque octets
// with no transparency layer — so BINARYMIME stays byte-exact.
//
// DotUnstuffReader is deliberately less strict, because it reads what some
// other implementation chose to send: it un-stuffs after a bare LF as
// readily as after a CRLF. The one place it is strict is the end-of-content
// marker, where RFC 5321 §4.1.1.4 is explicit that "<LF>.<LF>" MUST NOT be
// treated as equivalent to "<CRLF>.<CRLF>" — accepting it there is the
// receiving half of the same smuggling bug, so it is an error, not a
// leniency.

// dotDot is the two bytes written in place of a leading '.' on a stuffed
// line.
var dotDot = []byte("..")

// crlf is the sole line terminator this writer emits.
var crlf = []byte("\r\n")

// terminator is the dot-stuffing end-of-content marker.
var terminator = []byte(".\r\n")

// DotStuffWriter normalises content written to it to CRLF line endings,
// dot-stuffs it, and writes the result to the wrapped io.Writer. Call Close
// when the message content is complete; Close writes the terminating
// ".CRLF" (adding a CRLF first if the content did not already end in one,
// per "content not ending in CRLF — the terminator must still be well
// formed"). Close does not close the underlying writer.
//
// A DotStuffWriter is not safe for concurrent use.
type DotStuffWriter struct {
	w io.Writer
	// pendingCR records a CR whose successor byte has not been seen yet.
	// A CR is only known to be a bare CR once the next byte turns out not
	// to be LF, and that next byte routinely arrives in a later Write —
	// the same statefulness trap as a '.' at a Write boundary, which is
	// why this is filter state and not a local variable.
	pendingCR bool
	// atLineStart, wroteAny and lastByte describe the *normalised* stream
	// — what has actually been written to w — not the caller's input.
	atLineStart bool
	wroteAny    bool
	lastByte    byte
	closed      bool
}

// NewDotStuffWriter returns a DotStuffWriter that writes CRLF-normalised,
// dot-stuffed output to w.
func NewDotStuffWriter(w io.Writer) *DotStuffWriter {
	return &DotStuffWriter{w: w, atLineStart: true}
}

// Write normalises p's line endings to CRLF, dot-stuffs the result and
// writes it to the underlying writer. It never rejects input: every byte
// sequence has a conforming representation, so Write only ever fails when
// the underlying writer does.
//
// On success it returns len(p), nil, matching the io.Writer contract that a
// short count implies a non-nil error — bytes inserted for stuffing or for
// CR/LF normalisation are not part of that count, since they are not part of
// what the caller asked to write. A trailing CR is reported as consumed even
// though it has not been emitted yet: it is held in filter state until the
// next byte (or Close) resolves whether it was a bare CR or half of a CRLF.
func (d *DotStuffWriter) Write(p []byte) (int, error) {
	if d.closed {
		return 0, errors.New("smtpwire: write to closed DotStuffWriter")
	}
	consumed := 0
	for len(p) > 0 {
		if d.pendingCR {
			// The held CR terminates a line either way. If an LF follows,
			// the pair was already a CRLF and the LF is absorbed here;
			// otherwise the CR was bare and is promoted to a CRLF, and the
			// current byte is reprocessed from the top on the next
			// iteration with pendingCR now clear.
			d.pendingCR = false
			if err := d.emitCRLF(); err != nil {
				return consumed, err
			}
			if p[0] == '\n' {
				p = p[1:]
				consumed++
			}
			continue
		}
		i := indexCROrLF(p)
		if i == -1 {
			if err := d.emitLiteral(p); err != nil {
				return consumed, err
			}
			consumed += len(p)
			break
		}
		if i > 0 {
			if err := d.emitLiteral(p[:i]); err != nil {
				return consumed, err
			}
			consumed += i
			p = p[i:]
		}
		if p[0] == '\n' {
			// Bare LF: promoted to a CRLF terminator.
			if err := d.emitCRLF(); err != nil {
				return consumed, err
			}
			p = p[1:]
			consumed++
			continue
		}
		// CR: hold it until its successor decides what it was.
		d.pendingCR = true
		p = p[1:]
		consumed++
	}
	return consumed, nil
}

// emitLiteral writes a run of content bytes containing no CR and no LF,
// stuffing a leading '.' when the run begins at a line start. Because the
// run holds no line terminator, at most one stuffing decision applies to it.
func (d *DotStuffWriter) emitLiteral(chunk []byte) error {
	if len(chunk) == 0 {
		return nil
	}
	if d.atLineStart && chunk[0] == '.' {
		if _, err := d.w.Write(dotDot); err != nil {
			return err
		}
		chunk = chunk[1:]
		d.atLineStart = false
		d.wroteAny = true
		d.lastByte = '.'
		if len(chunk) == 0 {
			return nil
		}
	}
	if _, err := d.w.Write(chunk); err != nil {
		return err
	}
	d.atLineStart = false
	d.wroteAny = true
	d.lastByte = chunk[len(chunk)-1]
	return nil
}

// emitCRLF writes the one line terminator this writer produces and opens a
// new line, making the next '.' a stuffing opportunity.
func (d *DotStuffWriter) emitCRLF() error {
	if _, err := d.w.Write(crlf); err != nil {
		return err
	}
	d.atLineStart = true
	d.wroteAny = true
	d.lastByte = '\n'
	return nil
}

// indexCROrLF returns the index of the first CR or LF in p, or -1. Two
// bytes.IndexByte scans beat a hand-rolled loop here because each is
// assembly-optimised, and message bodies are long runs of neither.
func indexCROrLF(p []byte) int {
	cr := bytes.IndexByte(p, '\r')
	lf := bytes.IndexByte(p, '\n')
	switch {
	case cr < 0:
		return lf
	case lf < 0:
		return cr
	case cr < lf:
		return cr
	default:
		return lf
	}
}

// Close flushes any held CR as a CRLF, then writes the end-of-content
// terminator: a CRLF first if the content written so far did not already end
// with one (so the terminator itself is always well formed regardless of
// what the caller supplied), then ".\r\n". It does not close the underlying
// writer. Calling Close twice returns an error rather than writing the
// terminator twice.
func (d *DotStuffWriter) Close() error {
	if d.closed {
		return errors.New("smtpwire: DotStuffWriter already closed")
	}
	d.closed = true
	if d.pendingCR {
		// Content ended on a bare CR. It is a line terminator, so it is
		// promoted like any other — and that also satisfies the
		// "ends in CRLF" condition below.
		d.pendingCR = false
		if err := d.emitCRLF(); err != nil {
			return err
		}
	}
	if d.wroteAny && d.lastByte != '\n' {
		if _, err := d.w.Write(crlf); err != nil {
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

// ErrBareLFTerminator is returned by DotUnstuffReader when a line begins
// with a '.' immediately followed by a bare LF. RFC 5321 §4.1.1.4 is
// explicit that the sequence "<LF>.<LF>" MUST NOT be treated as equivalent
// to "<CRLF>.<CRLF>" as the end of mail data indication, so this cannot be
// accepted as a terminator. Nor can it be treated as content: an
// implementation that disagreed would end the message here, so continuing
// past it is precisely the SMTP-smuggling desynchronisation the rule exists
// to prevent. It is an error.
var ErrBareLFTerminator = errors.New("smtpwire: bare-LF end-of-content marker (RFC 5321 §4.1.1.4)")

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
				switch peek[0] {
				case '\r':
					if tailErr := d.consumeTerminatorTail(); tailErr != nil {
						return n, d.fail(n, tailErr)
					}
					return n, d.fail(n, io.EOF)
				case '\n':
					return n, d.fail(n, ErrBareLFTerminator)
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
// '.' has been read and peeking confirmed the next byte is CR. The only
// accepted form is ".\r\n"; a CR not followed by LF is
// ErrMalformedTerminator. The bare-LF form is rejected before this is
// reached — see ErrBareLFTerminator.
func (d *DotUnstuffReader) consumeTerminatorTail() error {
	br := d.lr.br
	if _, err := br.ReadByte(); err != nil { // the peeked CR
		return normalizeReadErr(err)
	}
	b, err := br.ReadByte()
	if err != nil {
		return normalizeReadErr(err)
	}
	if b != '\n' {
		return ErrMalformedTerminator
	}
	return nil
}
