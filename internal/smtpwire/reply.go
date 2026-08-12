// Package smtpwire implements the SMTP wire grammar in both directions:
// RFC 5321 command and reply framing, path parsing, EHLO parsing and
// advertisement encoding, RFC 3463 enhanced status codes, esmtp-param / xtext
// encoding, RFC 5321 §4.5.2 dot-stuffing transparency, RFC 3030 BDAT framing,
// and RFC 5321 §4.4 Received-field generation. Its codecs are total, streaming,
// bounded, and hand-written.
//
// This package deals in wire primitives only: reply lines, three-digit
// codes, keywords, parameters and raw byte transparency. It does not know
// about the root smtp package's semantic types (smtp.Error,
// smtp.EnhancedCode, per-recipient results) and must never import that
// package — semantic assembly happens in smtpclient and smtpserver. See
// docs/tasks/T01-wire-codec.md and docs/ARCHITECTURE.md §Parser.
//
// Nothing here is exported outside the module: this package is internal/ and
// must never leak into a public signature.
//
// Deliberately not net/textproto: ReadResponse there collapses a multiline
// reply into one string and discards the per-line structure the EHLO parser
// needs, and it has no notion of enhanced status codes, BDAT framing or
// dot-stuffing.
package smtpwire

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Limits bounds every accumulation this package performs, checked before any
// allocation grows past them. A zero value in any field falls back to the
// package default for that field, so the zero Limits{} is always safe to use
// directly — callers are never forced to know the defaults just to avoid an
// unusable all-zero limit set.
//
// The wire limits deliberately remain one direction-neutral type rather than
// being split into client/server variants: BDAT framing is shared by both
// directions, fields default independently, and this internal package's
// qualifier already distinguishes it from RFC 9422's public smtp.Limits.
type Limits struct {
	// MaxReplyLineLength caps a single reply line's text, excluding the
	// trailing CRLF. RFC 5321 §4.5.3.1 specifies 512 octets including CRLF
	// as the line servers must accept, but real servers routinely exceed
	// that with long EHLO keyword lists, so the default sits well above it.
	MaxReplyLineLength int
	// MaxReplyLines caps the number of lines (continuation + final) in one
	// reply.
	MaxReplyLines int
	// MaxReplySize caps the cumulative byte size of all lines' text in one
	// reply.
	MaxReplySize int
	// MaxBDATChunkSize caps the chunk size a BDAT command may announce,
	// enforced before any chunk is written or copied.
	MaxBDATChunkSize int64
	// MaxCommandLineLength caps one client command including its terminating
	// CRLF. RFC 5321 requires servers to accept at least 512 octets; the
	// default is larger while still bounding unauthenticated input tightly.
	MaxCommandLineLength int
}

// Package defaults. Exported indirectly via DefaultLimits and via Limits'
// zero-value fallback behaviour.
const (
	defaultMaxReplyLineLength   = 8192     // well above the 512-octet RFC minimum
	defaultMaxReplyLines        = 1000     // generous EHLO extension list
	defaultMaxReplySize         = 1 << 20  // 1 MiB total reply text
	defaultMaxBDATChunkSize     = 64 << 20 // 64 MiB; smtpclient may lower per negotiated SIZE
	defaultMaxCommandLineLength = 4096     // includes CRLF; safely above RFC 5321's 512-octet minimum
)

// DefaultLimits returns the package's built-in limits explicitly. Equivalent
// to the zero Limits{} once withDefaults is applied, provided as a
// documented, discoverable starting point for callers who want to override
// only some fields.
func DefaultLimits() Limits {
	return Limits{
		MaxReplyLineLength:   defaultMaxReplyLineLength,
		MaxReplyLines:        defaultMaxReplyLines,
		MaxReplySize:         defaultMaxReplySize,
		MaxBDATChunkSize:     defaultMaxBDATChunkSize,
		MaxCommandLineLength: defaultMaxCommandLineLength,
	}
}

func (l Limits) withDefaults() Limits {
	if l.MaxReplyLineLength <= 0 {
		l.MaxReplyLineLength = defaultMaxReplyLineLength
	}
	if l.MaxReplyLines <= 0 {
		l.MaxReplyLines = defaultMaxReplyLines
	}
	if l.MaxReplySize <= 0 {
		l.MaxReplySize = defaultMaxReplySize
	}
	if l.MaxBDATChunkSize <= 0 {
		l.MaxBDATChunkSize = defaultMaxBDATChunkSize
	}
	if l.MaxCommandLineLength <= 0 {
		l.MaxCommandLineLength = defaultMaxCommandLineLength
	}
	return l
}

// Reply is one complete SMTP reply: one or more lines sharing a single
// three-digit code, per RFC 5321 §4.2.
type Reply struct {
	// Code is the three-digit reply code shared by every line.
	Code int
	// Lines holds each line's text with the "nnn-"/"nnn " prefix removed,
	// in order. The EHLO parser needs this; a collapsed string does not
	// carry enough structure to recover it.
	Lines []string
	// Text is Lines joined with "\n", for callers that only want the
	// message.
	Text string
}

// Sentinel errors returned by the reply reader. Wrapped with additional
// context via fmt.Errorf's %w, so errors.Is still matches these.
var (
	// ErrReplyLineTooLong means a line exceeded Limits.MaxReplyLineLength
	// before a terminating LF was found.
	ErrReplyLineTooLong = errors.New("smtpwire: reply line too long")
	// ErrTooManyReplyLines means a reply accumulated more continuation
	// lines than Limits.MaxReplyLines allows.
	ErrTooManyReplyLines = errors.New("smtpwire: too many reply continuation lines")
	// ErrReplyTooLarge means a reply's cumulative text exceeded
	// Limits.MaxReplySize.
	ErrReplyTooLarge = errors.New("smtpwire: reply too large")
	// ErrReplyLineTooShort means a line had fewer than 3 bytes, too short
	// to hold a reply code.
	ErrReplyLineTooShort = errors.New("smtpwire: reply line shorter than a reply code")
	// ErrReplyCodeSyntax means the three bytes present were not a valid
	// reply code: first digit must be 2-5, second 0-5, third any digit.
	ErrReplyCodeSyntax = errors.New("smtpwire: invalid reply code syntax")
	// ErrReplyCodeMismatch means a continuation line's code did not match
	// the reply's established code. RFC 2920 §3.1: all lines of one reply
	// carry the same code; a mismatch means the client and server have
	// lost synchronisation (e.g. a multiline reply confused with replies
	// to multiple pipelined commands) and must not be papered over.
	ErrReplyCodeMismatch = errors.New("smtpwire: reply code mismatch across continuation lines")
)

// deadlineSetter matches net.Conn's SetReadDeadline method structurally, so
// this package never needs to import net. Readers that do not implement it
// (a bytes.Reader in tests, or in fuzzing) simply do not get deadline
// enforcement — that is the "deadline-capable" qualifier in the task spec.
type deadlineSetter interface {
	SetReadDeadline(t time.Time) error
}

// LineReader is a byte-oriented, bounded line reader shared by reply framing
// and DATA content transparency. A connection constructs exactly one
// LineReader and reuses it for every reply and for the message body that
// follows a 354: reusing the same *bufio.Reader across phases is what
// preserves look-ahead bytes at the reply/content boundary. A fresh
// bufio.Reader per call would silently drop buffered bytes read ahead of an
// intervening EHLO reply, corrupting the next command's reply.
type LineReader struct {
	src io.Reader
	br  *bufio.Reader
}

// NewLineReader wraps r for reply and content reading. r may optionally
// implement SetReadDeadline(time.Time) error (as net.Conn does); when it
// does, deadlines passed to ReadReply and to the dot-stuffing reader are
// applied to it.
func NewLineReader(r io.Reader) *LineReader {
	return &LineReader{src: r, br: bufio.NewReaderSize(r, defaultMaxReplyLineLength)}
}

// Buffered reports bytes already read ahead of the current protocol element.
// Session code uses it only to detect unsolicited replies that would otherwise
// be attributed to a later command; it never treats an empty buffer as proof
// that the peer cannot send a delayed unsolicited reply.
func (lr *LineReader) Buffered() int { return lr.br.Buffered() }

// setDeadline applies t to the underlying reader if it is deadline-capable.
// A zero t means "no deadline" and is a no-op. Readers that are not
// deadline-capable silently do not get a deadline — the caller chose an
// io.Reader without one, most commonly in tests or fuzzing.
func (lr *LineReader) setDeadline(t time.Time) error {
	if t.IsZero() {
		return nil
	}
	ds, ok := lr.src.(deadlineSetter)
	if !ok {
		return nil
	}
	if err := ds.SetReadDeadline(t); err != nil {
		return fmt.Errorf("smtpwire: set read deadline: %w", err)
	}
	return nil
}

// normalizeReadErr turns a bare io.EOF encountered mid-structure (partway
// through a line, or partway through a multiline reply) into
// io.ErrUnexpectedEOF, so callers can distinguish a clean "no more data will
// ever arrive" from a truncated protocol element. A plain io.EOF is
// preserved when it occurs at a legitimate boundary (see ReadReply).
func normalizeReadErr(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

// readLine reads one line, stopping at LF, stripping one trailing CR if
// present, and stopping totality dead in its tracks if the line grows past
// maxLen before an LF is found — checked before every append, never after,
// so the allocation itself stays bounded.
//
// Returns io.EOF, unwrapped, when zero bytes were read for this line before
// the underlying reader reported EOF — the only case a caller may treat as a
// clean boundary rather than a truncation.
func (lr *LineReader) readLine(maxLen int) ([]byte, error) {
	var buf []byte
	for {
		b, err := lr.br.ReadByte()
		if err != nil {
			if len(buf) == 0 {
				return nil, err
			}
			return nil, normalizeReadErr(err)
		}
		if b == '\n' {
			if n := len(buf); n > 0 && buf[n-1] == '\r' {
				buf = buf[:n-1]
			}
			return buf, nil
		}
		if len(buf) >= maxLen {
			return nil, ErrReplyLineTooLong
		}
		buf = append(buf, b)
	}
}

// parseReplyLine splits a single already-dechomped reply line into its
// three-digit code, continuation marker and text.
//
// Grammar (RFC 5321 §4.2):
//
//	Reply-line = Reply-code "-" [ textstring ] CRLF   (continuation)
//	           | Reply-code [ SP textstring ] CRLF    (final)
//	Reply-code = %x32-35 %x30-35 %x30-39
//
// Two deliberate leniencies, documented per the task spec rather than left
// implicit:
//
//   - "nnn" with nothing following (bare code, no text, no separator) is
//     valid and treated as a final line with empty text.
//   - "nnn" followed by a byte that is neither '-' nor ' ' (seen in the
//     wild — some servers omit the space before short texts) is treated
//     leniently as a final line whose text starts at that byte, rather than
//     rejected outright. Only '-' means continuation; anything else, or
//     nothing at all, means final.
func parseReplyLine(line []byte) (code int, continuation bool, text string, err error) {
	if len(line) < 3 {
		return 0, false, "", ErrReplyLineTooShort
	}
	d0, d1, d2 := line[0], line[1], line[2]
	if d0 < '2' || d0 > '5' || d1 < '0' || d1 > '5' || d2 < '0' || d2 > '9' {
		return 0, false, "", ErrReplyCodeSyntax
	}
	code = int(d0-'0')*100 + int(d1-'0')*10 + int(d2-'0')
	if len(line) == 3 {
		return code, false, "", nil
	}
	switch line[3] {
	case '-':
		return code, true, string(line[4:]), nil
	case ' ':
		return code, false, string(line[4:]), nil
	default:
		// Lenient: no separator present, treat as final line, text starts
		// immediately.
		return code, false, string(line[3:]), nil
	}
}

// ReadReply reads one complete SMTP reply per RFC 5321 §4.2: zero or more
// "nnn-text" continuation lines followed by one "nnn text" (or bare "nnn")
// final line, all sharing the same code.
//
// deadline, if non-zero, is applied to the underlying reader when it is
// deadline-capable (see LineReader.setDeadline) before any read — a server
// that sends a 354 and then stalls must time out rather than hang forever.
//
// limits bounds line length, continuation-line count and total reply size,
// each checked before the corresponding allocation grows, never after.
//
// A bare io.EOF is returned only when the very first line of the reply
// could not be read at all (zero bytes consumed) — a clean "the connection
// is closed, no reply is coming" signal. An EOF discovered after the first
// line has committed the caller to a reply — mid-line, or expecting a
// promised continuation — is reported as io.ErrUnexpectedEOF instead.
func (lr *LineReader) ReadReply(deadline time.Time, limits Limits) (Reply, error) {
	limits = limits.withDefaults()
	if err := lr.setDeadline(deadline); err != nil {
		return Reply{}, err
	}

	var (
		code      int
		haveCode  bool
		lines     []string
		totalSize int
	)
	for lineIdx := 0; ; lineIdx++ {
		// MaxReplyLineLength is a limit on reply *text*, not on the three
		// code bytes, separator, or CRLF framing. Allow precisely enough
		// room for that framing while still bounding allocation before it
		// grows. A CRLF line has five non-text bytes before readLine removes
		// the LF (three code bytes, a separator, and CR).
		raw, err := lr.readLine(limits.MaxReplyLineLength + 5)
		if err != nil {
			if errors.Is(err, io.EOF) && lineIdx > 0 {
				// A continuation was promised; running out of bytes now is
				// a truncation, not a clean boundary.
				return Reply{}, io.ErrUnexpectedEOF
			}
			return Reply{}, err
		}
		lineCode, cont, text, err := parseReplyLine(raw)
		if err != nil {
			return Reply{}, err
		}
		if len(text) > limits.MaxReplyLineLength {
			return Reply{}, ErrReplyLineTooLong
		}
		if !haveCode {
			code = lineCode
			haveCode = true
		} else if lineCode != code {
			return Reply{}, fmt.Errorf("%w: first line was %d, line %d is %d", ErrReplyCodeMismatch, code, lineIdx+1, lineCode)
		}

		totalSize += len(text)
		if totalSize > limits.MaxReplySize {
			return Reply{}, ErrReplyTooLarge
		}
		lines = append(lines, text)
		if len(lines) > limits.MaxReplyLines {
			return Reply{}, ErrTooManyReplyLines
		}

		if !cont {
			return Reply{Code: code, Lines: lines, Text: strings.Join(lines, "\n")}, nil
		}
	}
}
