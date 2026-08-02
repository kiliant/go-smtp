package smtpwire

import (
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
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
)

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
