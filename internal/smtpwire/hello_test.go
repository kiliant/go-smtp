package smtpwire

import (
	"bytes"
	"reflect"
	"testing"
	"time"
)

func TestParseHelloCommand(t *testing.T) {
	got, err := ParseHelloCommand(Command{Verb: "eHlO", Argument: "client.example"})
	if err != nil {
		t.Fatalf("ParseHelloCommand: %v", err)
	}
	if got != (Hello{Verb: "EHLO", Domain: "client.example"}) {
		t.Fatalf("hello = %#v", got)
	}
}

func TestEncodeEHLOReplyRoundTrip(t *testing.T) {
	want := EHLOReply{
		Domain:   "mx.example.test",
		Greeting: "hello",
		Extensions: []Extension{
			{Keyword: "PIPELINING"},
			{Keyword: "SIZE", Params: []string{"1048576"}, Raw: "1048576"},
			{Keyword: "AUTH", Params: []string{"PLAIN", "LOGIN"}, Raw: "PLAIN LOGIN"},
		},
	}
	var wire bytes.Buffer
	if err := EncodeEHLOReply(&wire, want); err != nil {
		t.Fatalf("EncodeEHLOReply: %v", err)
	}
	framed, err := NewLineReader(&wire).ReadReply(time.Time{}, Limits{})
	if err != nil {
		t.Fatalf("ReadReply: %v", err)
	}
	got, err := ParseEHLOReply(framed.Lines)
	if err != nil {
		t.Fatalf("ParseEHLOReply: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
}
