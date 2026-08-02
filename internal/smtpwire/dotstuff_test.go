package smtpwire

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// stuffAll runs data through a DotStuffWriter, writing it via the supplied
// writeFunc (which controls how data is chunked across Write calls), and
// returns the fully dot-stuffed, terminated output.
func stuffAll(t *testing.T, data []byte, writeFunc func(w io.Writer, data []byte) error) []byte {
	t.Helper()
	var buf bytes.Buffer
	dw := NewDotStuffWriter(&buf)
	if err := writeFunc(dw, data); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := dw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.Bytes()
}

func writeWhole(w io.Writer, data []byte) error {
	_, err := w.Write(data)
	return err
}

// writeSplitAt returns a writeFunc that issues two Write calls, splitting
// data at offset off.
func writeSplitAt(off int) func(io.Writer, []byte) error {
	return func(w io.Writer, data []byte) error {
		if off > len(data) {
			off = len(data)
		}
		if _, err := w.Write(data[:off]); err != nil {
			return err
		}
		_, err := w.Write(data[off:])
		return err
	}
}

// writeByteAtATime issues one Write call per byte — the most adversarial
// possible chunking, and the one most likely to expose a filter that
// forgets state between calls.
func writeByteAtATime(w io.Writer, data []byte) error {
	for i := range data {
		if _, err := w.Write(data[i : i+1]); err != nil {
			return err
		}
	}
	return nil
}

func TestDotStuffExactDotLine(t *testing.T) {
	got := stuffAll(t, []byte("before\r\n.\r\nafter\r\n"), writeWhole)
	want := []byte("before\r\n..\r\nafter\r\n.\r\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDotStuffLeadingDoubleDot(t *testing.T) {
	// A line that already begins with ".." must gain one more leading dot
	// (symmetric stuffing: every leading dot is doubled, not just a lone
	// one), so unstuffing removes exactly one.
	got := stuffAll(t, []byte("..already\r\n"), writeWhole)
	want := []byte("...already\r\n.\r\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDotStuffMissingTrailingCRLF(t *testing.T) {
	got := stuffAll(t, []byte("no trailing terminator"), writeWhole)
	want := []byte("no trailing terminator\r\n.\r\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDotStuffAlreadyEndsInCRLF(t *testing.T) {
	got := stuffAll(t, []byte("clean line\r\n"), writeWhole)
	want := []byte("clean line\r\n.\r\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDotStuffEmptyContent(t *testing.T) {
	got := stuffAll(t, []byte{}, writeWhole)
	want := []byte(".\r\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDotStuffMultipleDotLines(t *testing.T) {
	in := []byte(".\r\n.line\r\n..line\r\nnormal\r\n")
	got := stuffAll(t, in, writeWhole)
	want := []byte("..\r\n..line\r\n...line\r\nnormal\r\n.\r\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestDotStuffSplitAcrossWriteBoundary is the TRAP #3 regression test: a
// "." arriving as the last byte of one Write and the newline arriving in
// the next must still be recognised as a line consisting of exactly ".",
// and stuffed. Splitting the whole input at every possible offset — and
// additionally one byte at a time — proves the filter's state truly
// survives across Write calls; a test that writes the whole body in one
// call proves nothing.
func TestDotStuffSplitAcrossWriteBoundary(t *testing.T) {
	inputs := [][]byte{
		[]byte("before\r\n.\r\nafter\r\n"),
		[]byte(".\r\n"),
		[]byte("..\r\n"),
		[]byte("a\r\n.b\r\n..c\r\nd"),
		[]byte(".\r\n.\r\n.\r\n"),
	}
	for _, in := range inputs {
		whole := stuffAll(t, in, writeWhole)
		for off := 0; off <= len(in); off++ {
			got := stuffAll(t, in, writeSplitAt(off))
			if !bytes.Equal(got, whole) {
				t.Fatalf("input %q split at offset %d: got %q, want %q (whole-write result)", in, off, got, whole)
			}
		}
		byByte := stuffAll(t, in, writeByteAtATime)
		if !bytes.Equal(byByte, whole) {
			t.Fatalf("input %q written byte-at-a-time: got %q, want %q", in, byByte, whole)
		}
	}
}

func TestDotStuffBareLFStartsNewLine(t *testing.T) {
	// Documented decision: line boundaries for stuffing purposes are
	// defined by LF alone, so a bare-LF-terminated line still triggers
	// stuffing on the next line.
	got := stuffAll(t, []byte("a\n.\nb\r\n"), writeWhole)
	want := []byte("a\n..\nb\r\n.\r\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDotStuffBareCRIsOrdinaryContent(t *testing.T) {
	// Documented decision: a lone CR does not start a new line and creates
	// no stuffing opportunity by itself.
	got := stuffAll(t, []byte("a\r.b\r\n"), writeWhole)
	want := []byte("a\r.b\r\n.\r\n")
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDotStuffWriteAfterCloseFails(t *testing.T) {
	var buf bytes.Buffer
	dw := NewDotStuffWriter(&buf)
	if err := dw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := dw.Write([]byte("x")); err == nil {
		t.Fatal("Write after Close: want error, got nil")
	}
	if err := dw.Close(); err == nil {
		t.Fatal("second Close: want error, got nil")
	}
}

// unstuffAll reads everything DotUnstuffReader produces, using readFunc to
// control how many bytes are requested per Read call.
func unstuffAll(t *testing.T, stuffed []byte, readSize int) []byte {
	t.Helper()
	lr := NewLineReader(bytes.NewReader(stuffed))
	ur := NewDotUnstuffReader(lr)
	var out bytes.Buffer
	buf := make([]byte, readSize)
	for {
		n, err := ur.Read(buf)
		out.Write(buf[:n])
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatalf("Read: %v", err)
		}
	}
	return out.Bytes()
}

func TestDotUnstuffRoundTrip(t *testing.T) {
	inputs := []string{
		"",
		"hello world\r\n",
		".\r\n.line\r\n..line\r\nnormal\r\n",
		"..already\r\n",
		"no trailing terminator",
		"a\n.\nb\r\n",
		"a\r.b\r\n",
	}
	for _, in := range inputs {
		for _, readSize := range []int{1, 2, 3, 7, 64, 4096} {
			stuffed := stuffAll(t, []byte(in), writeWhole)
			got := unstuffAll(t, stuffed, readSize)
			// Close appends a CRLF only when content was written that did
			// not already end in one. Empty content round-trips to empty:
			// it stuffs to a bare ".CRLF" terminator, so demanding a CRLF
			// back would assert that an empty message gains a blank line.
			want := []byte(in)
			if len(want) > 0 && want[len(want)-1] != '\n' {
				want = append(append([]byte{}, want...), '\r', '\n')
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("input %q readSize %d: unstuff(stuff(x)) = %q, want %q", in, readSize, got, want)
			}
		}
	}
}

func TestDotUnstuffStopsBeforeSubsequentData(t *testing.T) {
	// A DotUnstuffReader must not consume bytes past its own terminator —
	// whatever the server sends next (e.g. the final reply after the
	// content) must remain untouched in the shared buffer.
	stream := ".\r\n250 OK\r\n"
	lr := NewLineReader(strings.NewReader(stream))
	ur := NewDotUnstuffReader(lr)
	got, err := io.ReadAll(ur)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("content = %q, want empty (input was just the terminator)", got)
	}
	reply, err := lr.ReadReply(time.Time{}, Limits{})
	if err != nil {
		t.Fatalf("ReadReply after unstuff: %v", err)
	}
	if reply.Code != 250 || reply.Text != "OK" {
		t.Fatalf("reply = %+v", reply)
	}
}

func TestDotUnstuffMalformedTerminator(t *testing.T) {
	// "." CR not followed by LF.
	lr := NewLineReader(strings.NewReader(".\rX"))
	ur := NewDotUnstuffReader(lr)
	_, err := io.ReadAll(ur)
	if !errors.Is(err, ErrMalformedTerminator) {
		t.Fatalf("err = %v, want ErrMalformedTerminator", err)
	}
}

func TestDotUnstuffTruncatedAfterDot(t *testing.T) {
	lr := NewLineReader(strings.NewReader("content\r\n."))
	ur := NewDotUnstuffReader(lr)
	_, err := io.ReadAll(ur)
	if err == nil {
		t.Fatal("want error for truncated stream, got nil")
	}
}
