package smtpwire

import (
	"errors"
	"reflect"
	"testing"
)

// TestParseEHLOGreetingIsNotAKeyword is the TRAP #1 regression test: the
// first line of an EHLO reply is the server's greeting domain, never an
// extension keyword. A parser that got this wrong would register a bogus
// extension named after the hostname.
func TestParseEHLOGreetingIsNotAKeyword(t *testing.T) {
	lines := []string{
		"mail.example.com at your service",
		"PIPELINING",
		"SIZE 10485760",
	}
	reply, err := ParseEHLOReply(lines)
	if err != nil {
		t.Fatalf("ParseEHLOReply: %v", err)
	}
	if reply.Domain != "mail.example.com" {
		t.Errorf("Domain = %q, want %q", reply.Domain, "mail.example.com")
	}
	if reply.Greeting != "at your service" {
		t.Errorf("Greeting = %q, want %q", reply.Greeting, "at your service")
	}
	for _, ext := range reply.Extensions {
		if ext.Keyword == "MAIL.EXAMPLE.COM" {
			t.Fatalf("greeting domain leaked into Extensions: %+v", reply.Extensions)
		}
	}
	if len(reply.Extensions) != 2 {
		t.Fatalf("Extensions = %+v, want 2 entries", reply.Extensions)
	}
	if reply.Extensions[0].Keyword != "PIPELINING" {
		t.Errorf("Extensions[0].Keyword = %q, want PIPELINING", reply.Extensions[0].Keyword)
	}
	if reply.Extensions[1].Keyword != "SIZE" || len(reply.Extensions[1].Params) != 1 || reply.Extensions[1].Params[0] != "10485760" {
		t.Errorf("Extensions[1] = %+v, want SIZE [10485760]", reply.Extensions[1])
	}
}

func TestParseEHLOGreetingOnlyDomain(t *testing.T) {
	reply, err := ParseEHLOReply([]string{"mail.example.com"})
	if err != nil {
		t.Fatalf("ParseEHLOReply: %v", err)
	}
	if reply.Domain != "mail.example.com" || reply.Greeting != "" {
		t.Errorf("got Domain=%q Greeting=%q", reply.Domain, reply.Greeting)
	}
	if reply.Extensions != nil {
		t.Errorf("Extensions = %+v, want nil", reply.Extensions)
	}
}

func TestParseEHLOKeywordCaseNormalised(t *testing.T) {
	reply, err := ParseEHLOReply([]string{"mail.example.com", "pipelining", "Size 100"})
	if err != nil {
		t.Fatalf("ParseEHLOReply: %v", err)
	}
	if reply.Extensions[0].Keyword != "PIPELINING" {
		t.Errorf("Keyword = %q, want PIPELINING (uppercased)", reply.Extensions[0].Keyword)
	}
	if reply.Extensions[1].Keyword != "SIZE" {
		t.Errorf("Keyword = %q, want SIZE (uppercased)", reply.Extensions[1].Keyword)
	}
}

// TestParseEHLOParamsVerbatimAndOrdered exercises the exact examples called
// out in the task spec: SIZE, AUTH with multiple mechanisms, and LIMITS with
// a key=value style parameter, none of which may be dropped or reordered.
func TestParseEHLOParamsVerbatimAndOrdered(t *testing.T) {
	tests := []struct {
		line       string
		wantParams []string
	}{
		{"SIZE 10485760", []string{"10485760"}},
		{"AUTH PLAIN LOGIN", []string{"PLAIN", "LOGIN"}},
		{"LIMITS RCPTMAX=100", []string{"RCPTMAX=100"}},
	}
	for _, tt := range tests {
		reply, err := ParseEHLOReply([]string{"mail.example.com", tt.line})
		if err != nil {
			t.Fatalf("ParseEHLOReply(%q): %v", tt.line, err)
		}
		got := reply.Extensions[0].Params
		if !reflect.DeepEqual(got, tt.wantParams) {
			t.Errorf("line %q: Params = %#v, want %#v", tt.line, got, tt.wantParams)
		}
	}
}

func TestParseEHLOUnknownKeywordPreserved(t *testing.T) {
	reply, err := ParseEHLOReply([]string{"mail.example.com", "X-FUTURE-EXTENSION foo bar"})
	if err != nil {
		t.Fatalf("ParseEHLOReply: %v", err)
	}
	ext := reply.Extensions[0]
	if ext.Keyword != "X-FUTURE-EXTENSION" {
		t.Errorf("Keyword = %q, want X-FUTURE-EXTENSION", ext.Keyword)
	}
	if !reflect.DeepEqual(ext.Params, []string{"foo", "bar"}) {
		t.Errorf("Params = %#v", ext.Params)
	}
}

func TestParseEHLONoParams(t *testing.T) {
	reply, err := ParseEHLOReply([]string{"mail.example.com", "PIPELINING"})
	if err != nil {
		t.Fatalf("ParseEHLOReply: %v", err)
	}
	if reply.Extensions[0].Params != nil {
		t.Errorf("Params = %#v, want nil", reply.Extensions[0].Params)
	}
	if reply.Extensions[0].Raw != "" {
		t.Errorf("Raw = %q, want empty", reply.Extensions[0].Raw)
	}
}

func TestParseEHLOEmptyInput(t *testing.T) {
	_, err := ParseEHLOReply(nil)
	if !errors.Is(err, ErrEHLOEmpty) {
		t.Fatalf("err = %v, want ErrEHLOEmpty", err)
	}
}

func TestParseEHLOEmptyKeywordLine(t *testing.T) {
	_, err := ParseEHLOReply([]string{"mail.example.com", ""})
	if !errors.Is(err, ErrEHLOEmptyKeyword) {
		t.Fatalf("err = %v, want ErrEHLOEmptyKeyword", err)
	}
}
