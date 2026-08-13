package smtpwire

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestReadCommandPreservesArgumentAndPipeline(t *testing.T) {
	lr := NewLineReader(bytes.NewBufferString("mAiL   FROM:<a@example.test> SIZE=12\r\nNOOP\r\n"))
	got, err := lr.ReadCommand(time.Time{}, Limits{})
	if err != nil {
		t.Fatalf("ReadCommand MAIL: %v", err)
	}
	want := Command{Verb: "mAiL", Argument: "FROM:<a@example.test> SIZE=12"}
	if got != want {
		t.Fatalf("ReadCommand MAIL = %#v, want %#v", got, want)
	}
	got, err = lr.ReadCommand(time.Time{}, Limits{})
	if err != nil {
		t.Fatalf("ReadCommand NOOP: %v", err)
	}
	if got != (Command{Verb: "NOOP"}) {
		t.Fatalf("ReadCommand NOOP = %#v", got)
	}
	if _, err := lr.ReadCommand(time.Time{}, Limits{}); !errors.Is(err, io.EOF) {
		t.Fatalf("ReadCommand at boundary = %v, want io.EOF", err)
	}
}

func TestReadSASLResponseAcceptsBase64PaddingAndAbort(t *testing.T) {
	reader := NewLineReader(strings.NewReader("dXNlcg==\r\n*\r\n"))
	first, err := reader.ReadSASLResponse(time.Time{}, Limits{})
	if err != nil || first != "dXNlcg==" {
		t.Fatalf("first = %q, %v", first, err)
	}
	second, err := reader.ReadSASLResponse(time.Time{}, Limits{})
	if err != nil || second != "*" {
		t.Fatalf("second = %q, %v", second, err)
	}
}

func TestReadSASLResponseHasIndependentLineLimit(t *testing.T) {
	response := strings.Repeat("A", 5000)
	got, err := NewLineReader(strings.NewReader(response+"\r\n")).ReadSASLResponse(time.Time{}, Limits{})
	if err != nil || got != response {
		t.Fatalf("ReadSASLResponse length %d: %v", len(got), err)
	}
	reader := NewLineReader(strings.NewReader("AAAAAAAA\r\nNOOP\r\n"))
	_, err = reader.ReadSASLResponse(time.Time{}, Limits{MaxSASLResponseLength: 5})
	if !errors.Is(err, ErrCommandLineTooLong) {
		t.Fatalf("limited ReadSASLResponse = %v, want ErrCommandLineTooLong", err)
	}
	command, err := reader.ReadCommand(time.Time{}, Limits{})
	if err != nil || command.Verb != "NOOP" {
		t.Fatalf("command after drained SASL line = %+v, %v", command, err)
	}
}

func TestReadCommandFramingFailures(t *testing.T) {
	tests := []struct {
		name   string
		wire   string
		limits Limits
		want   error
	}{
		{name: "bare LF", wire: "NOOP\n", want: ErrCommandBareLF},
		{name: "empty", wire: "\r\n", want: ErrCommandEmpty},
		{name: "bad verb", wire: "NO:OP\r\n", want: ErrCommandVerbSyntax},
		{name: "argument control", wire: "NOOP a\tb\r\n", want: ErrCommandArgumentControl},
		{name: "partial", wire: "NOOP", want: io.ErrUnexpectedEOF},
		{name: "too long", wire: "NOOP x\r\n", limits: Limits{MaxCommandLineLength: 6}, want: ErrCommandLineTooLong},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewLineReader(bytes.NewBufferString(tt.wire)).ReadCommand(time.Time{}, tt.limits)
			if !errors.Is(err, tt.want) {
				t.Fatalf("ReadCommand = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestReadCommandPreservesUnknownExtensionVerb(t *testing.T) {
	got, err := NewLineReader(bytes.NewBufferString("X-EXT2 value\r\n")).ReadCommand(time.Time{}, Limits{})
	if err != nil {
		t.Fatalf("ReadCommand: %v", err)
	}
	if got != (Command{Verb: "X-EXT2", Argument: "value"}) {
		t.Fatalf("command = %#v", got)
	}
}

func TestCommandEncodeDecodeRoundTrip(t *testing.T) {
	var wire bytes.Buffer
	if err := EncodeCommand(&wire, "X-EXT2", "value"); err != nil {
		t.Fatalf("EncodeCommand: %v", err)
	}
	got, err := NewLineReader(&wire).ReadCommand(time.Time{}, Limits{})
	if err != nil {
		t.Fatalf("ReadCommand: %v", err)
	}
	if got != (Command{Verb: "X-EXT2", Argument: "value"}) {
		t.Fatalf("round trip = %#v", got)
	}
}

func TestReadCommandLimitIncludesCRLF(t *testing.T) {
	got, err := NewLineReader(bytes.NewBufferString("NOOP\r\n")).ReadCommand(time.Time{}, Limits{MaxCommandLineLength: 6})
	if err != nil {
		t.Fatalf("exact limit: %v", err)
	}
	if got.Verb != "NOOP" {
		t.Fatalf("verb = %q", got.Verb)
	}
}
