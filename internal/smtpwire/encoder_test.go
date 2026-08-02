package smtpwire

import (
	"bytes"
	"errors"
	"testing"
)

func TestEncodeCommand(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeCommand(&buf, "MAIL", "FROM:<a@b.example>", "SIZE=100"); err != nil {
		t.Fatalf("EncodeCommand: %v", err)
	}
	want := "MAIL FROM:<a@b.example> SIZE=100\r\n"
	if buf.String() != want {
		t.Fatalf("got %q, want %q", buf.String(), want)
	}
}

func TestEncodeCommandNoArgs(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeCommand(&buf, "QUIT"); err != nil {
		t.Fatalf("EncodeCommand: %v", err)
	}
	if buf.String() != "QUIT\r\n" {
		t.Fatalf("got %q", buf.String())
	}
}

func TestEncodeCommandRejectsControlBytes(t *testing.T) {
	tests := []struct {
		name string
		verb string
		args []string
	}{
		{"CR in arg", "MAIL", []string{"FROM:<a@b>\rINJECTED"}},
		{"LF in arg", "MAIL", []string{"FROM:<a@b>\nINJECTED"}},
		{"CRLF in arg", "RCPT", []string{"TO:<a@b>\r\nDATA"}},
		{"NUL in arg", "RCPT", []string{"TO:<a@b>\x00"}},
		{"CR in verb", "MA\rIL", nil},
		{"DEL in arg", "MAIL", []string{"FROM:<a@b>\x7F"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			err := EncodeCommand(&buf, tt.verb, tt.args...)
			if !errors.Is(err, ErrInvalidCommandToken) {
				t.Fatalf("err = %v, want ErrInvalidCommandToken", err)
			}
			if buf.Len() != 0 {
				t.Fatalf("buffer has %d bytes written despite rejection; a command that cannot be encoded must never reach the wire", buf.Len())
			}
		})
	}
}

func TestEncodeCommandAllowsHighBytes(t *testing.T) {
	// SMTPUTF8 addresses use bytes 0x80-0xFF; the encoder must not reject
	// them, only control bytes.
	var buf bytes.Buffer
	if err := EncodeCommand(&buf, "RCPT", "TO:<élise@example.com>"); err != nil {
		t.Fatalf("EncodeCommand: %v", err)
	}
}

func TestEncodeParam(t *testing.T) {
	tests := []struct {
		p       Param
		want    string
		wantErr error
	}{
		{Param{Keyword: "SIZE", Value: "12345"}, "SIZE=12345", nil},
		{Param{Keyword: "SMTPUTF8"}, "SMTPUTF8", nil},
		{Param{Keyword: "BODY", Value: "8BITMIME"}, "BODY=8BITMIME", nil},
		{Param{Keyword: "", Value: "x"}, "", ErrEmptyKeyword},
		{Param{Keyword: "1-bad-start-ok", Value: "x"}, "1-bad-start-ok=x", nil}, // digit start is valid
		{Param{Keyword: "bad keyword", Value: "x"}, "", ErrInvalidKeyword},
		{Param{Keyword: "RET", Value: "has space"}, "", ErrInvalidParamValue},
		{Param{Keyword: "RET", Value: "has=equals"}, "", ErrInvalidParamValue},
		{Param{Keyword: "RET", Value: "has\rcr"}, "", ErrInvalidParamValue},
	}
	for _, tt := range tests {
		got, err := EncodeParam(tt.p)
		if tt.wantErr != nil {
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("EncodeParam(%+v) err = %v, want %v", tt.p, err, tt.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("EncodeParam(%+v): unexpected error %v", tt.p, err)
			continue
		}
		if got != tt.want {
			t.Errorf("EncodeParam(%+v) = %q, want %q", tt.p, got, tt.want)
		}
	}
}

func TestEncodeXtextDecodeXtextRoundTrip(t *testing.T) {
	tests := []string{
		"",
		"plain-ascii",
		"has space",
		"has+plus",
		"has=equals",
		"has\rcr\nlf",
		"has\x00nul",
		"unicode-é-中文",
		"a+b=c",
		string([]byte{0, 1, 2, 3, 254, 255}),
	}
	for _, raw := range tests {
		enc := EncodeXtext(raw)
		if err := validateESMTPValue(enc); err != nil && enc != "" {
			t.Errorf("EncodeXtext(%q) = %q is not a valid esmtp-value: %v", raw, enc, err)
		}
		dec, err := DecodeXtext(enc)
		if err != nil {
			t.Fatalf("DecodeXtext(EncodeXtext(%q)=%q): %v", raw, enc, err)
		}
		if dec != raw {
			t.Errorf("round trip: raw=%q encoded=%q decoded=%q", raw, enc, dec)
		}
	}
}

// TestEncodeXtextKnownVectors pins the exact golden vectors that the root
// package's TestEncodeXtextKnownVectors pins. EncodeXtext exists twice in
// this module — here and in smtp/xtext.go — because package smtp imports
// nothing from this module and this package can never be exported, so the
// duplication is forced by the layering rule rather than chosen. These two
// tables are the only thing keeping the copies from drifting apart into two
// subtly different wire encodings.
//
// Drift will be introduced on THIS side: the root copy is frozen at v1.0 and
// will not be edited, while this one is the live codec. If you change the
// encoder here, change smtp/xtext.go and its table too.
func TestEncodeXtextKnownVectors(t *testing.T) {
	tests := []struct{ raw, want string }{
		{"", ""},
		{"plain", "plain"},
		{"+", "+2B"},
		{"=", "+3D"},
		{"a+b", "a+2Bb"},
		{"a=b", "a+3Db"},
		{" ", "+20"},
		{"\r\n", "+0D+0A"},
	}
	for _, tt := range tests {
		got := EncodeXtext(tt.raw)
		if got != tt.want {
			t.Errorf("EncodeXtext(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

// TestXtextParamForENVIDAndORCPT is the TRAP #4 regression test: ENVID and
// ORCPT (RFC 3461 §4) must be xtext-encoded, which is routinely forgotten
// and only surfaces as a 501 against strict servers.
func TestXtextParamForENVIDAndORCPT(t *testing.T) {
	p := XtextParam("ENVID", "queue-id with spaces+plus")
	encoded, err := EncodeParam(p)
	if err != nil {
		t.Fatalf("EncodeParam: %v", err)
	}
	want := "ENVID=" + EncodeXtext("queue-id with spaces+plus")
	if encoded != want {
		t.Fatalf("got %q, want %q", encoded, want)
	}
	// The raw value contained a space, which is illegal in an unescaped
	// esmtp-value; prove the encoded form no longer contains one.
	if got, _ := DecodeXtext(p.Value); got != "queue-id with spaces+plus" {
		t.Fatalf("decoded xtext = %q", got)
	}

	orcpt := XtextParam("ORCPT", "rfc822;user@example.com")
	encodedOrcpt, err := EncodeParam(orcpt)
	if err != nil {
		t.Fatalf("EncodeParam: %v", err)
	}
	dec, err := DecodeXtext(orcpt.Value)
	if err != nil || dec != "rfc822;user@example.com" {
		t.Fatalf("decoded ORCPT = %q, err = %v", dec, err)
	}
	if encodedOrcpt == "" {
		t.Fatalf("empty encoded ORCPT param")
	}
}

func TestDecodeXtextInvalid(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"truncated escape", "abc+"},
		{"truncated escape one hex", "abc+2"},
		{"invalid hex digit", "abc+ZZ"},
		{"unescaped control byte", "abc\x01"},
		{"unescaped space", "abc def"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := DecodeXtext(tt.in); err == nil {
				t.Fatalf("DecodeXtext(%q): want error, got nil", tt.in)
			}
		})
	}
}
