package smtpserver

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/kiliant/go-smtp/internal/smtpwire"
)

func TestCommandLoopFlushesBeforeBlockingRead(t *testing.T) {
	source := newBlockingStageReader("X-FIRST\r\n")
	var wire bytes.Buffer
	loop := commandLoop{
		reader: smtpwire.NewLineReader(source),
		writer: bufio.NewWriter(&wire),
		execute: func(_ context.Context, _ smtpwire.Command, _ *smtpwire.LineReader, writer *bufio.Writer) (commandAction, error) {
			_, err := writer.WriteString("250 first\r\n")
			return commandAction{}, err
		},
	}
	done := make(chan error, 1)
	go func() { done <- loop.run(context.Background()) }()

	<-source.blocked
	if got := wire.String(); got != "250 first\r\n" {
		t.Fatalf("wire before blocking read = %q", got)
	}
	close(source.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCommandLoopPreservesPipelinedReplyOrder(t *testing.T) {
	reader := smtpwire.NewLineReader(strings.NewReader("X-ONE\r\nX-TWO\r\n"))
	var wire bytes.Buffer
	loop := commandLoop{
		reader: reader,
		writer: bufio.NewWriter(&wire),
		execute: func(_ context.Context, command smtpwire.Command, _ *smtpwire.LineReader, writer *bufio.Writer) (commandAction, error) {
			_, err := writer.WriteString("250 " + command.Verb + "\r\n")
			return commandAction{}, err
		},
	}
	if err := loop.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := wire.String(), "250 X-ONE\r\n250 X-TWO\r\n"; got != want {
		t.Fatalf("wire = %q, want %q", got, want)
	}
}

func TestCommandLoopFlushesSynchronizationPointWithBufferedInput(t *testing.T) {
	reader := smtpwire.NewLineReader(strings.NewReader("EHLO client.example\r\nMAIL FROM:<a@example.test>\r\n"))
	var wire bytes.Buffer
	seen := 0
	loop := commandLoop{
		reader: reader,
		writer: bufio.NewWriter(&wire),
		execute: func(_ context.Context, command smtpwire.Command, _ *smtpwire.LineReader, writer *bufio.Writer) (commandAction, error) {
			seen++
			if seen == 2 && wire.String() != "250 EHLO\r\n" {
				t.Fatalf("EHLO reply was not flushed before next buffered command: %q", wire.String())
			}
			_, err := writer.WriteString("250 " + command.Verb + "\r\n")
			return commandAction{synchronizationPoint: command.Verb == "EHLO"}, err
		},
	}
	if err := loop.run(context.Background()); err != nil {
		t.Fatal(err)
	}
}

type blockingStageReader struct {
	first   []byte
	blocked chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBlockingStageReader(first string) *blockingStageReader {
	return &blockingStageReader{
		first:   []byte(first),
		blocked: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (r *blockingStageReader) Read(p []byte) (int, error) {
	if len(r.first) > 0 {
		n := copy(p, r.first)
		r.first = r.first[n:]
		return n, nil
	}
	r.once.Do(func() { close(r.blocked) })
	<-r.release
	return 0, io.EOF
}
