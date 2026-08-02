package smtpwire

import (
	"bytes"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
)

func TestEncodeBDATCommand(t *testing.T) {
	tests := []struct {
		name string
		size uint64
		last bool
		want string
	}{
		{"non-final chunk", 12, false, "BDAT 12\r\n"},
		{"final chunk", 12, true, "BDAT 12 LAST\r\n"},
		{"empty final chunk", 0, true, "BDAT 0 LAST\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got bytes.Buffer
			if err := EncodeBDATCommand(&got, tt.size, tt.last, Limits{MaxBDATChunkSize: 12}); err != nil {
				t.Fatalf("EncodeBDATCommand: %v", err)
			}
			if got.String() != tt.want {
				t.Errorf("command = %q, want %q", got.String(), tt.want)
			}
		})
	}
}

func TestEncodeBDATCommandRejectsInvalidSizeBeforeWrite(t *testing.T) {
	tests := []struct {
		name    string
		size    uint64
		limits  Limits
		wantErr error
	}{
		{"over configured limit", 13, Limits{MaxBDATChunkSize: 12}, ErrBDATChunkTooLarge},
		{"over int64", uint64(math.MaxInt64) + 1, Limits{MaxBDATChunkSize: math.MaxInt64}, ErrBDATSizeOverflow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got bytes.Buffer
			err := EncodeBDATCommand(&got, tt.size, false, tt.limits)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("EncodeBDATCommand error = %v, want %v", err, tt.wantErr)
			}
			if got.Len() != 0 {
				t.Errorf("wrote %q despite rejected size", got.String())
			}
		})
	}
}

func TestCopyBDATChunkCopiesExactOpaqueBytes(t *testing.T) {
	// BDAT payloads are opaque: bytes that have special meaning to DATA must
	// pass through unchanged, and the source must retain bytes after the chunk.
	source := bytes.NewReader([]byte(".\r\n..\r\n\x00binary\xfftail"))
	var got bytes.Buffer
	n, err := CopyBDATChunk(&got, source, 14, Limits{MaxBDATChunkSize: 14})
	if err != nil {
		t.Fatalf("CopyBDATChunk: %v", err)
	}
	if n != 14 {
		t.Errorf("bytes copied = %d, want 14", n)
	}
	if want := ".\r\n..\r\n\x00binary"; got.String() != want {
		t.Errorf("payload = %q, want %q", got.Bytes(), []byte(want))
	}
	if rest, _ := io.ReadAll(source); string(rest) != "\xfftail" {
		t.Errorf("source remainder = %q, want %q", rest, []byte("\xfftail"))
	}
}

func TestCopyBDATChunkShortSource(t *testing.T) {
	var got bytes.Buffer
	n, err := CopyBDATChunk(&got, strings.NewReader("short"), 6, Limits{MaxBDATChunkSize: 6})
	if !errors.Is(err, ErrBDATShortSource) {
		t.Fatalf("CopyBDATChunk error = %v, want ErrBDATShortSource", err)
	}
	if n != 5 {
		t.Errorf("bytes copied = %d, want 5", n)
	}
	if got.String() != "short" {
		t.Errorf("payload = %q, want %q", got.String(), "short")
	}
}

func TestCopyBDATChunkRejectsInvalidSizeBeforeIO(t *testing.T) {
	for _, tt := range []struct {
		name    string
		size    uint64
		limits  Limits
		wantErr error
	}{
		{"over configured limit", 4, Limits{MaxBDATChunkSize: 3}, ErrBDATChunkTooLarge},
		{"over int64", uint64(math.MaxInt64) + 1, Limits{MaxBDATChunkSize: math.MaxInt64}, ErrBDATSizeOverflow},
	} {
		t.Run(tt.name, func(t *testing.T) {
			reader := &failReader{t: t}
			writer := &failWriter{t: t}
			n, err := CopyBDATChunk(writer, reader, tt.size, tt.limits)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("CopyBDATChunk error = %v, want %v", err, tt.wantErr)
			}
			if n != 0 {
				t.Errorf("bytes copied = %d, want 0", n)
			}
		})
	}
}

func TestCopyBDATChunkPropagatesWriterError(t *testing.T) {
	wantErr := errors.New("writer failed")
	n, err := CopyBDATChunk(errorWriter{err: wantErr}, strings.NewReader("payload"), 7, Limits{MaxBDATChunkSize: 7})
	if !errors.Is(err, wantErr) {
		t.Fatalf("CopyBDATChunk error = %v, want writer error", err)
	}
	if n != 0 {
		t.Errorf("bytes copied = %d, want 0", n)
	}
}

type failReader struct{ t *testing.T }

func (r *failReader) Read([]byte) (int, error) {
	r.t.Error("reader was called")
	return 0, errors.New("unexpected read")
}

type failWriter struct{ t *testing.T }

func (w *failWriter) Write([]byte) (int, error) {
	w.t.Error("writer was called")
	return 0, errors.New("unexpected write")
}

type errorWriter struct{ err error }

func (w errorWriter) Write([]byte) (int, error) { return 0, w.err }
