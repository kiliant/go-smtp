package smtpwire

import (
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

// BDAT framing per RFC 3030: "BDAT <size>[ LAST]\r\n" followed by exactly
// <size> octets of message content with no dot-stuffing or other
// transparency encoding, then one reply. Chunk sizing policy — how big a
// chunk to send, and when to mark LAST — belongs to smtpclient; this file
// only frames whatever chunk size it is given, and refuses to frame or copy
// one that is unsafe to attempt.

var (
	// ErrBDATChunkTooLarge is returned when a requested chunk size exceeds
	// Limits.MaxBDATChunkSize. Checked before anything is written or
	// copied — a "BDAT 4294967295" must be rejected, not attempted.
	ErrBDATChunkTooLarge = errors.New("smtpwire: BDAT chunk size exceeds configured limit")
	// ErrBDATSizeOverflow is returned when a chunk size cannot be
	// represented as a non-negative int64, which io.CopyN requires.
	ErrBDATSizeOverflow = errors.New("smtpwire: BDAT chunk size overflows")
	// ErrBDATShortSource is returned by CopyBDATChunk when r produced fewer
	// than size bytes.
	ErrBDATShortSource = errors.New("smtpwire: BDAT chunk source exhausted before announced size")
	// ErrBDATCommandSyntax is returned when a server-side BDAT argument is
	// not exactly "<size>" or "<size> LAST".
	ErrBDATCommandSyntax = errors.New("smtpwire: invalid BDAT command syntax")
)

// BDATCommand is the decoded framing portion of a BDAT command.
type BDATCommand struct {
	Size uint64
	Last bool
}

// ParseBDATArgument parses "<size> [LAST]" and enforces the configured chunk
// limit before a server attempts to read any content octets.
func ParseBDATArgument(argument string, limits Limits) (BDATCommand, error) {
	for i := 0; i < len(argument); i++ {
		c := argument[i]
		if c != ' ' && (c < 0x21 || c > 0x7e) {
			return BDATCommand{}, ErrBDATCommandSyntax
		}
	}
	fields := strings.Fields(argument)
	if len(fields) < 1 || len(fields) > 2 || (len(fields) == 2 && !strings.EqualFold(fields[1], "LAST")) {
		return BDATCommand{}, ErrBDATCommandSyntax
	}
	if fields[0] == "" || strings.HasPrefix(fields[0], "+") || strings.HasPrefix(fields[0], "-") {
		return BDATCommand{}, ErrBDATCommandSyntax
	}
	size, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return BDATCommand{}, fmt.Errorf("%w: %v", ErrBDATCommandSyntax, err)
	}
	if err := checkBDATSize(size, limits); err != nil {
		return BDATCommand{}, err
	}
	return BDATCommand{Size: size, Last: len(fields) == 2}, nil
}

// ReadBDATChunk copies exactly size bytes from the LineReader's existing
// buffer. Reusing that buffer is essential: bytes beyond the chunk may already
// be the next pipelined BDAT command and must not be dropped or over-read.
// deadline is applied to the underlying connection before content is read.
func (lr *LineReader) ReadBDATChunk(w io.Writer, size uint64, deadline time.Time, limits Limits) (int64, error) {
	if err := lr.setDeadline(deadline); err != nil {
		return 0, err
	}
	return CopyBDATChunk(w, lr.br, size, limits)
}

// checkBDATSize validates size against limits before any write or copy is
// attempted, guarding both an oversized-by-policy chunk and an
// integer-overflow-by-arithmetic one.
func checkBDATSize(size uint64, limits Limits) error {
	limits = limits.withDefaults()
	if size > math.MaxInt64 {
		return fmt.Errorf("%w: %d", ErrBDATSizeOverflow, size)
	}
	if int64(size) > limits.MaxBDATChunkSize {
		return fmt.Errorf("%w: %d > %d", ErrBDATChunkTooLarge, size, limits.MaxBDATChunkSize)
	}
	return nil
}

// EncodeBDATCommand writes "BDAT <size>\r\n" (or "BDAT <size> LAST\r\n" when
// last is true) to w. size is validated against limits before anything is
// written.
func EncodeBDATCommand(w io.Writer, size uint64, last bool, limits Limits) error {
	if err := checkBDATSize(size, limits); err != nil {
		return err
	}
	line := "BDAT " + strconv.FormatUint(size, 10)
	if last {
		line += " LAST"
	}
	line += "\r\n"
	_, err := io.WriteString(w, line)
	return err
}

// CopyBDATChunk copies exactly size bytes from r to w, streaming, with no
// transparency encoding applied — BDAT content is opaque octets, not
// dot-stuffed text. size is validated against limits before a single byte
// is copied. If r produces fewer than size bytes, CopyBDATChunk returns
// ErrBDATShortSource wrapping the underlying cause.
func CopyBDATChunk(w io.Writer, r io.Reader, size uint64, limits Limits) (int64, error) {
	if err := checkBDATSize(size, limits); err != nil {
		return 0, err
	}
	n, err := io.CopyN(w, r, int64(size))
	if err != nil {
		if errors.Is(err, io.EOF) {
			return n, fmt.Errorf("%w: got %d of %d bytes: %v", ErrBDATShortSource, n, size, err)
		}
		return n, err
	}
	return n, nil
}
