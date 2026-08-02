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
//
// CRLF normalisation adds a second trap of exactly the same shape: a CR
// ending one Write cannot be classified until the next Write reveals
// whether an LF follows, so the last four inputs below exist to split a CR
// from its LF. A filter that resolved the CR eagerly would turn "\r\n" into
// "\r\n\r\n" whenever the pair straddled a Write boundary.
func TestDotStuffSplitAcrossWriteBoundary(t *testing.T) {
	inputs := [][]byte{
		[]byte("before\r\n.\r\nafter\r\n"),
		[]byte(".\r\n"),
		[]byte("..\r\n"),
		[]byte("a\r\n.b\r\n..c\r\nd"),
		[]byte(".\r\n.\r\n.\r\n"),
		[]byte("a\r\nb"),
		[]byte("a\r.\r\nb"),
		[]byte("\r\n\r\n"),
		[]byte("x\ry\nz\r\n"),
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

// TestDotStuffNormalisesBareLineEndings pins RFC 5321 §2.3.8: a bare CR or
// bare LF in caller content MUST NOT reach the wire, and becomes a CRLF
// terminator instead. Each promoted terminator also opens a new line, so a
// '.' that follows one is a stuffing opportunity — which is the security
// half of this transform, not a side effect.
func TestDotStuffNormalisesBareLineEndings(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"bare LF", "a\n.\nb\r\n", "a\r\n..\r\nb\r\n.\r\n"},
		{"bare CR", "a\r.b\r\n", "a\r\n..b\r\n.\r\n"},
		{"CR LF pair untouched", "a\r\n.b\r\n", "a\r\n..b\r\n.\r\n"},
		{"CR CR is two terminators", "a\r\r.\r\n", "a\r\n\r\n..\r\n.\r\n"},
		{"LF CR is two terminators", "a\n\r.\r\n", "a\r\n\r\n..\r\n.\r\n"},
		{"trailing bare CR", "a\r", "a\r\n.\r\n"},
		{"trailing bare LF", "a\n", "a\r\n.\r\n"},
		{"lone CR only", "\r", "\r\n.\r\n"},
		// The smuggling vector the old pass-through behaviour left open:
		// a receiver honouring bare CR as a terminator would have seen an
		// unstuffed "." line here and ended the message early.
		{"CR dot CR LF", "a\r.\r\n", "a\r\n..\r\n.\r\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stuffAll(t, []byte(tc.in), writeWhole)
			if !bytes.Equal(got, []byte(tc.want)) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestDotStuffNoBareCRLFReachesTheWire is the blunt invariant behind the
// table above, asserted over every input the other tests use: after
// stuffing, every CR is followed by an LF and every LF is preceded by a CR.
func TestDotStuffNoBareCRLFReachesTheWire(t *testing.T) {
	inputs := []string{
		"a\n.\nb\r\n", "a\r.b\r\n", "\r", "\n", "\r\r\r", "\n\n\n", "\r\n\r\n",
		".\n.\r.\r\n", "no terminator", "", "\r\n.\r", "x\ry\nz",
	}
	for _, in := range inputs {
		for _, wf := range []func(io.Writer, []byte) error{writeWhole, writeByteAtATime} {
			out := stuffAll(t, []byte(in), wf)
			for i := range out {
				if out[i] == '\r' && (i+1 >= len(out) || out[i+1] != '\n') {
					t.Fatalf("input %q -> %q: bare CR at offset %d", in, out, i)
				}
				if out[i] == '\n' && (i == 0 || out[i-1] != '\r') {
					t.Fatalf("input %q -> %q: bare LF at offset %d", in, out, i)
				}
			}
		}
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
		"\r",
		"\n",
		"mixed\rendings\nhere\r\n",
	}
	for _, in := range inputs {
		for _, readSize := range []int{1, 2, 3, 7, 64, 4096} {
			stuffed := stuffAll(t, []byte(in), writeWhole)
			got := unstuffAll(t, stuffed, readSize)
			want := wantRoundTrip([]byte(in))
			if !bytes.Equal(got, want) {
				t.Fatalf("input %q readSize %d: unstuff(stuff(x)) = %q, want %q", in, readSize, got, want)
			}
		}
	}
}

// normalizeCRLF is a reference implementation of the writer's line-ending
// normalisation, written independently of the filter so the round-trip
// property is checked against a statement of the transform rather than
// against the transform checking itself.
func normalizeCRLF(in []byte) []byte {
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); i++ {
		switch in[i] {
		case '\r':
			out = append(out, '\r', '\n')
			if i+1 < len(in) && in[i+1] == '\n' {
				i++ // CRLF pair: consume both, emit one terminator
			}
		case '\n':
			out = append(out, '\r', '\n')
		default:
			out = append(out, in[i])
		}
	}
	return out
}

// wantRoundTrip states what unstuff(stuff(x)) must equal. Since the writer
// normalises, the identity is unstuff(stuff(x)) == normalizeCRLF(x), not
// == x. Close appends a CRLF only when content was written that did not
// already end in one; empty content round-trips to empty, because it stuffs
// to a bare ".CRLF" terminator and demanding a CRLF back would assert that
// an empty message gains a blank line.
func wantRoundTrip(in []byte) []byte {
	want := normalizeCRLF(in)
	if len(want) > 0 && want[len(want)-1] != '\n' {
		want = append(want, '\r', '\n')
	}
	return want
}

// TestDotUnstuffRejectsBareLFTerminator is the receiving half of the
// smuggling fix. RFC 5321 §4.1.1.4: the sequence "<LF>.<LF>" MUST NOT be
// treated as equivalent to "<CRLF>.<CRLF>" as the end of mail data
// indication.
func TestDotUnstuffRejectsBareLFTerminator(t *testing.T) {
	for _, stream := range []string{".\n", "content\r\n.\n", "content\n.\n"} {
		lr := NewLineReader(strings.NewReader(stream))
		ur := NewDotUnstuffReader(lr)
		_, err := io.ReadAll(ur)
		if !errors.Is(err, ErrBareLFTerminator) {
			t.Fatalf("stream %q: err = %v, want ErrBareLFTerminator", stream, err)
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
