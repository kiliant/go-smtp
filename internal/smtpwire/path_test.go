package smtpwire

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseReversePath(t *testing.T) {
	tests := []struct {
		name     string
		argument string
		opts     PathOptions
		want     Path
	}{
		{name: "null", argument: "FROM:<>", want: Path{Null: true}},
		{name: "space after colon", argument: "from: <user@example.test>", want: Path{Mailbox: "user@example.test", LocalPart: "user", Domain: "example.test"}},
		{
			name:     "source route discarded",
			argument: "FROM:<@a.example,@[IPv6:2001:db8::1]:user@c.example> SIZE=12 SMTPUTF8",
			want: Path{
				Mailbox:   "user@c.example",
				LocalPart: "user",
				Domain:    "c.example",
				Params:    []Param{{Keyword: "SIZE", Value: "12"}, {Keyword: "SMTPUTF8"}},
			},
		},
		{name: "quoted local", argument: `FROM:<"a@b\" >"@example.test>`, want: Path{Mailbox: `"a@b\" >"@example.test`, LocalPart: `"a@b\" >"`, Domain: "example.test"}},
		{name: "IPv4 literal", argument: "FROM:<user@[192.0.2.1]>", want: Path{Mailbox: "user@[192.0.2.1]", LocalPart: "user", Domain: "[192.0.2.1]"}},
		{name: "IPv6 literal", argument: "FROM:<user@[IPv6:2001:db8::1]>", want: Path{Mailbox: "user@[IPv6:2001:db8::1]", LocalPart: "user", Domain: "[IPv6:2001:db8::1]"}},
		{name: "SMTPUTF8", argument: "FROM:<élise@例え.test>", opts: PathOptions{SMTPUTF8: true}, want: Path{Mailbox: "élise@例え.test", LocalPart: "élise", Domain: "例え.test"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseReversePath(tt.argument, tt.opts)
			if err != nil {
				t.Fatalf("ParseReversePath: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("path = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseForwardPathPostmaster(t *testing.T) {
	for _, argument := range []string{"TO:<Postmaster>", "TO:<postmaster@example.test>"} {
		got, err := ParseForwardPath(argument, PathOptions{})
		if err != nil {
			t.Fatalf("ParseForwardPath(%q): %v", argument, err)
		}
		if !got.Postmaster {
			t.Fatalf("ParseForwardPath(%q) did not identify Postmaster: %#v", argument, got)
		}
	}
}

func TestParsePathFailures(t *testing.T) {
	tests := []struct {
		name    string
		forward bool
		arg     string
		opts    PathOptions
		want    error
	}{
		{name: "prefix", arg: "TO:<a@example.test>", want: ErrPathPrefix},
		{name: "null forward", forward: true, arg: "TO:<>", want: ErrNullForwardPath},
		{name: "domainless", forward: true, arg: "TO:<user>", want: ErrPathSyntax},
		{name: "UTF8 undeclared", arg: "FROM:<élise@example.test>", want: ErrPathUTF8Required},
		{name: "bad quoted local", arg: `FROM:<"a"b"@example.test>`, want: ErrPathSyntax},
		{name: "bad literal", arg: "FROM:<a@[IPv6:not-an-ip]>", want: ErrPathSyntax},
		{name: "bad param", arg: "FROM:<a@example.test> SIZE=", want: ErrPathParameter},
		{name: "too long", arg: "FROM:<a@example.test>", opts: PathOptions{MaxPathLength: 10}, want: ErrPathTooLong},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.forward {
				_, err = ParseForwardPath(tt.arg, tt.opts)
			} else {
				_, err = ParseReversePath(tt.arg, tt.opts)
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestPathLimitCountsOnlyAnglePath(t *testing.T) {
	path := "<a@b>"
	argument := "FROM:" + path + " " + strings.Repeat("X", 100)
	_, err := ParseReversePath(argument, PathOptions{MaxPathLength: len(path)})
	if err != nil {
		t.Fatalf("parameter bytes must not count against the path bound: %v", err)
	}
}
