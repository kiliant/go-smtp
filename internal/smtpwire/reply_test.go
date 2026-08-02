package smtpwire

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// deadlineReader wraps an io.Reader and records/controls SetReadDeadline
// calls, so tests can prove ReadReply is deadline-aware without a real
// net.Conn.
type deadlineReader struct {
	io.Reader
	lastDeadline time.Time
	calls        int
	setErr       error
}

func (d *deadlineReader) SetReadDeadline(t time.Time) error {
	d.calls++
	d.lastDeadline = t
	return d.setErr
}

func mustReadReply(t *testing.T, input string, limits Limits) Reply {
	t.Helper()
	lr := NewLineReader(strings.NewReader(input))
	reply, err := lr.ReadReply(time.Time{}, limits)
	if err != nil {
		t.Fatalf("ReadReply(%q): unexpected error: %v", input, err)
	}
	return reply
}

func TestReadReplySingleLine(t *testing.T) {
	reply := mustReadReply(t, "250 OK\r\n", Limits{})
	if reply.Code != 250 {
		t.Errorf("Code = %d, want 250", reply.Code)
	}
	if want := []string{"OK"}; !equalStrings(reply.Lines, want) {
		t.Errorf("Lines = %#v, want %#v", reply.Lines, want)
	}
	if reply.Text != "OK" {
		t.Errorf("Text = %q, want %q", reply.Text, "OK")
	}
}

func TestReadReplyMultiline(t *testing.T) {
	reply := mustReadReply(t, "250-First\r\n250-Second\r\n250 Third\r\n", Limits{})
	want := []string{"First", "Second", "Third"}
	if reply.Code != 250 {
		t.Errorf("Code = %d, want 250", reply.Code)
	}
	if !equalStrings(reply.Lines, want) {
		t.Errorf("Lines = %#v, want %#v", reply.Lines, want)
	}
	if reply.Text != "First\nSecond\nThird" {
		t.Errorf("Text = %q", reply.Text)
	}
}

func TestReadReplyBareCode(t *testing.T) {
	reply := mustReadReply(t, "250\r\n", Limits{})
	if reply.Code != 250 || len(reply.Lines) != 1 || reply.Lines[0] != "" {
		t.Errorf("got %+v, want code 250 with one empty line", reply)
	}
}

// TestReadReplyNoSeparator covers "nnn with neither - nor space following",
// which occurs in the wild; the documented decision is to treat it as a
// final line whose text starts immediately.
func TestReadReplyNoSeparator(t *testing.T) {
	reply := mustReadReply(t, "250Text\r\n", Limits{})
	if reply.Code != 250 {
		t.Errorf("Code = %d, want 250", reply.Code)
	}
	if want := []string{"Text"}; !equalStrings(reply.Lines, want) {
		t.Errorf("Lines = %#v, want %#v", reply.Lines, want)
	}
}

// TestReadReplyBareLF covers a server using a bare LF instead of CRLF; the
// line reader tolerates it.
func TestReadReplyBareLF(t *testing.T) {
	reply := mustReadReply(t, "250 OK\n", Limits{})
	if reply.Code != 250 || reply.Text != "OK" {
		t.Errorf("got %+v", reply)
	}
}

// TestReadReplyCodeMismatch is the TRAP #2 regression test: RFC 2920 §3.1
// requires every line of one reply to carry the same code. A mismatch must
// be a hard protocol error, never silently accepted — accepting it would
// let a multiline reply be confused with replies to two different pipelined
// commands, attributing a result to the wrong recipient.
func TestReadReplyCodeMismatch(t *testing.T) {
	lr := NewLineReader(strings.NewReader("250-First\r\n251 Second\r\n"))
	_, err := lr.ReadReply(time.Time{}, Limits{})
	if !errors.Is(err, ErrReplyCodeMismatch) {
		t.Fatalf("err = %v, want ErrReplyCodeMismatch", err)
	}
}

func TestReadReplyInvalidCodeDigits(t *testing.T) {
	tests := []string{
		"099 too low\r\n",  // first digit must be 2-5
		"699 too high\r\n", // first digit must be 2-5
		"260 bad\r\n",      // second digit must be 0-5
	}
	for _, in := range tests {
		lr := NewLineReader(strings.NewReader(in))
		_, err := lr.ReadReply(time.Time{}, Limits{})
		if !errors.Is(err, ErrReplyCodeSyntax) {
			t.Errorf("input %q: err = %v, want ErrReplyCodeSyntax", in, err)
		}
	}
}

func TestReadReplyLineTooShort(t *testing.T) {
	lr := NewLineReader(strings.NewReader("25\r\n"))
	_, err := lr.ReadReply(time.Time{}, Limits{})
	if !errors.Is(err, ErrReplyLineTooShort) {
		t.Fatalf("err = %v, want ErrReplyLineTooShort", err)
	}
}

func TestReadReplyLineTooLong(t *testing.T) {
	longText := strings.Repeat("x", 100)
	lr := NewLineReader(strings.NewReader("250 " + longText + "\r\n"))
	_, err := lr.ReadReply(time.Time{}, Limits{MaxReplyLineLength: 10})
	if !errors.Is(err, ErrReplyLineTooLong) {
		t.Fatalf("err = %v, want ErrReplyLineTooLong", err)
	}
}

func TestReadReplyTooManyLines(t *testing.T) {
	input := "250-a\r\n250-b\r\n250-c\r\n250 d\r\n"
	lr := NewLineReader(strings.NewReader(input))
	_, err := lr.ReadReply(time.Time{}, Limits{MaxReplyLines: 2})
	if !errors.Is(err, ErrTooManyReplyLines) {
		t.Fatalf("err = %v, want ErrTooManyReplyLines", err)
	}
}

func TestReadReplyTooLarge(t *testing.T) {
	input := "250-0123456789\r\n250 done\r\n"
	lr := NewLineReader(strings.NewReader(input))
	_, err := lr.ReadReply(time.Time{}, Limits{MaxReplySize: 5})
	if !errors.Is(err, ErrReplyTooLarge) {
		t.Fatalf("err = %v, want ErrReplyTooLarge", err)
	}
}

func TestReadReplyCleanEOF(t *testing.T) {
	lr := NewLineReader(strings.NewReader(""))
	_, err := lr.ReadReply(time.Time{}, Limits{})
	if !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
	if errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, must not be io.ErrUnexpectedEOF for a clean boundary", err)
	}
}

func TestReadReplyUnexpectedEOFMidLine(t *testing.T) {
	lr := NewLineReader(strings.NewReader("25"))
	_, err := lr.ReadReply(time.Time{}, Limits{})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestReadReplyUnexpectedEOFMidMultiline(t *testing.T) {
	lr := NewLineReader(strings.NewReader("250-First\r\n"))
	_, err := lr.ReadReply(time.Time{}, Limits{})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestReadReplyDeadlineApplied(t *testing.T) {
	dr := &deadlineReader{Reader: strings.NewReader("250 OK\r\n")}
	lr := NewLineReader(dr)
	deadline := time.Now().Add(time.Minute)
	if _, err := lr.ReadReply(deadline, Limits{}); err != nil {
		t.Fatalf("ReadReply: %v", err)
	}
	if dr.calls != 1 {
		t.Fatalf("SetReadDeadline calls = %d, want 1", dr.calls)
	}
	if !dr.lastDeadline.Equal(deadline) {
		t.Fatalf("deadline = %v, want %v", dr.lastDeadline, deadline)
	}
}

func TestReadReplyZeroDeadlineIsNoop(t *testing.T) {
	dr := &deadlineReader{Reader: strings.NewReader("250 OK\r\n")}
	lr := NewLineReader(dr)
	if _, err := lr.ReadReply(time.Time{}, Limits{}); err != nil {
		t.Fatalf("ReadReply: %v", err)
	}
	if dr.calls != 0 {
		t.Fatalf("SetReadDeadline calls = %d, want 0 for zero deadline", dr.calls)
	}
}

func TestReadReplyDeadlineErrorPropagates(t *testing.T) {
	wantErr := errors.New("deadline boom")
	dr := &deadlineReader{Reader: strings.NewReader("250 OK\r\n"), setErr: wantErr}
	lr := NewLineReader(dr)
	_, err := lr.ReadReply(time.Now().Add(time.Second), Limits{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want wrapping %v", err, wantErr)
	}
}

// TestReadReplySharedBufferAcrossCalls proves the LineReader reuses its
// buffered reader across successive ReadReply calls, so bytes read ahead
// for one reply (e.g. because the server pipelined its response with the
// next one) are not lost before the next call.
func TestReadReplySharedBufferAcrossCalls(t *testing.T) {
	lr := NewLineReader(strings.NewReader("250 first\r\n251 second\r\n"))
	r1, err := lr.ReadReply(time.Time{}, Limits{})
	if err != nil {
		t.Fatalf("first ReadReply: %v", err)
	}
	if r1.Code != 250 || r1.Text != "first" {
		t.Fatalf("first reply = %+v", r1)
	}
	r2, err := lr.ReadReply(time.Time{}, Limits{})
	if err != nil {
		t.Fatalf("second ReadReply: %v", err)
	}
	if r2.Code != 251 || r2.Text != "second" {
		t.Fatalf("second reply = %+v", r2)
	}
}

func TestDefaultLimitsFillsZeroLimits(t *testing.T) {
	got := Limits{}.withDefaults()
	want := DefaultLimits()
	if got != want {
		t.Fatalf("Limits{}.withDefaults() = %+v, want %+v", got, want)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
