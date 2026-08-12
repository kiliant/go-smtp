package smtpwire

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestEncodeReplyRoundTrip(t *testing.T) {
	want := Reply{Code: 250, Lines: []string{"mx.example.test", "PIPELINING", "SIZE 1024"}}
	var wire bytes.Buffer
	if err := EncodeReply(&wire, want, ReplyOptions{}); err != nil {
		t.Fatalf("EncodeReply: %v", err)
	}
	got, err := NewLineReader(&wire).ReadReply(time.Time{}, Limits{})
	if err != nil {
		t.Fatalf("ReadReply: %v", err)
	}
	if got.Code != want.Code || len(got.Lines) != len(want.Lines) {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
	for i := range want.Lines {
		if got.Lines[i] != want.Lines[i] {
			t.Fatalf("line %d = %q, want %q", i, got.Lines[i], want.Lines[i])
		}
	}
}

func TestEncodeReplyEnhancedStatusPlacement(t *testing.T) {
	enhanced := EnhancedCode{Class: 5, Subject: 7, Detail: 1}
	for _, tt := range []struct {
		name    string
		context ReplyContext
		want    string
	}{
		{name: "command", context: ReplyContextCommand, want: "550 5.7.1 denied\r\n"},
		{name: "greeting", context: ReplyContextGreeting, want: "550 denied\r\n"},
		{name: "hello", context: ReplyContextHello, want: "550 denied\r\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var wire bytes.Buffer
			err := EncodeReply(&wire, Reply{Code: 550, Lines: []string{"denied"}}, ReplyOptions{Enhanced: &enhanced, Context: tt.context})
			if err != nil {
				t.Fatalf("EncodeReply: %v", err)
			}
			if wire.String() != tt.want {
				t.Fatalf("wire = %q, want %q", wire.String(), tt.want)
			}
		})
	}
}

func TestEncodeReplyRejectsBeforeWrite(t *testing.T) {
	tests := []struct {
		name  string
		reply Reply
		opts  ReplyOptions
		want  error
	}{
		{name: "code", reply: Reply{Code: 199, Lines: []string{"bad"}}, want: ErrReplyEncodeCode},
		{name: "injection", reply: Reply{Code: 250, Lines: []string{"ok\r\n550 injected"}}, want: ErrReplyEncodeText},
		{name: "class", reply: Reply{Code: 550, Lines: []string{"bad"}}, opts: ReplyOptions{Enhanced: &EnhancedCode{Class: 4, Subject: 7, Detail: 1}}, want: ErrEnhancedClassMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var wire bytes.Buffer
			err := EncodeReply(&wire, tt.reply, tt.opts)
			if !errors.Is(err, tt.want) {
				t.Fatalf("EncodeReply = %v, want %v", err, tt.want)
			}
			if wire.Len() != 0 {
				t.Fatalf("wrote %q before rejecting", wire.String())
			}
		})
	}
}

func TestEncodeReplyAllowsEmptyText(t *testing.T) {
	var wire bytes.Buffer
	if err := EncodeReply(&wire, Reply{Code: 250}, ReplyOptions{}); err != nil {
		t.Fatalf("EncodeReply: %v", err)
	}
	if wire.String() != "250 \r\n" {
		t.Fatalf("wire = %q", wire.String())
	}
}
