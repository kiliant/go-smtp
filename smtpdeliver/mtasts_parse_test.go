package smtpdeliver

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseSTSRecords(t *testing.T) {
	cases := []struct {
		name    string
		records []string
		id      string
		err     error
	}{
		{"RFC example", []string{"v=STSv1; id=20160831085700Z;"}, "20160831085700Z", nil},
		{"no trailing delimiter", []string{"v=STSv1; id=abc"}, "abc", nil},
		{"whitespace around delimiters", []string{"v=STSv1 ;\tid=abc ; "}, "abc", nil},
		{"extension fields ignored, order free", []string{"v=STSv1; ext_1-a.b=x!y; id=7"}, "7", nil},
		{"duplicate id keeps the first", []string{"v=STSv1; id=first; id=second"}, "first", nil},
		{"unrelated TXT records are discarded", []string{"google-site-verification=x", "v=STSv1; id=1"}, "1", nil},
		{"none", nil, "", errNoSTSRecord},
		{"only unrelated records", []string{"v=spf1 -all"}, "", errNoSTSRecord},
		{"two STS records", []string{"v=STSv1; id=1", "v=STSv1; id=2"}, "", errNoSTSRecord},
		{"version without delimiter is not a candidate", []string{"v=STSv1"}, "", errNoSTSRecord},
		{"wrong version is not a candidate", []string{"v=STSv2; id=1"}, "", errNoSTSRecord},
		{"missing id", []string{"v=STSv1; ext=1"}, "", errInvalidSTSRecord},
		{"id too long", []string{"v=STSv1; id=" + strings.Repeat("a", 33)}, "", errInvalidSTSRecord},
		{"id with punctuation", []string{"v=STSv1; id=2016-08-31"}, "", errInvalidSTSRecord},
		{"empty field", []string{"v=STSv1;; id=1"}, "", errInvalidSTSRecord},
		{"field without value", []string{"v=STSv1; id=1; ext="}, "", errInvalidSTSRecord},
		{"value with space", []string{"v=STSv1; id=1; ext=a b"}, "", errInvalidSTSRecord},
		{"bad extension name", []string{"v=STSv1; id=1; _ext=1"}, "", errInvalidSTSRecord},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := parseSTSRecords(tc.records)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("parseSTSRecords = %q, %v; want %v", id, err, tc.err)
				}
				return
			}
			if err != nil || id != tc.id {
				t.Fatalf("parseSTSRecords = %q, %v; want %q", id, err, tc.id)
			}
		})
	}
}

const examplePolicy = "version: STSv1\r\nmode: enforce\r\nmx: mail.example.com\r\nmx: *.example.net\r\nmx: backupmx.example.com\r\nmax_age: 604800\r\n"

func TestParsePolicy(t *testing.T) {
	p, err := parsePolicy([]byte(examplePolicy))
	if err != nil {
		t.Fatal(err)
	}
	if p.Version != "STSv1" || p.Mode != MTASTSEnforce || p.MaxAge != 604800*time.Second || strings.Join(p.MX, ",") != "mail.example.com,*.example.net,backupmx.example.com" {
		t.Fatalf("RFC 8461 §3.2 example = %+v", p)
	}

	valid := []struct{ name, body string }{
		{"LF line endings", "version: STSv1\nmode: testing\nmx: mx.example.com\nmax_age: 86400\n"},
		{"no final terminator", "version: STSv1\nmode: none\nmax_age: 0"},
		{"trailing whitespace and blank lines", "version: STSv1  \n\nmode: none\t\nmax_age: 1\n\n"},
		{"unknown fields ignored", "version: STSv1\nmode: none\nmax_age: 1\nfuture_key: anything, even ünïcode\n"},
		{"duplicate mode keeps the first", "version: STSv1\nmode: none\nmode: enforce\nmax_age: 1\n"},
		{"upper-case mx normalised", "version: STSv1\nmode: enforce\nmx: MX.Example.COM.\nmax_age: 1\n"},
	}
	for _, tc := range valid {
		if _, err := parsePolicy([]byte(tc.body)); err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
	}
	if p, _ := parsePolicy([]byte(valid[5].body)); len(p.MX) != 1 || p.MX[0] != "mx.example.com" {
		t.Errorf("mx not normalised: %v", p.MX)
	}
	if p, _ := parsePolicy([]byte("version: STSv1\nmode: none\nmax_age: 9999999999\n")); p.MaxAge != maxPolicyAge {
		t.Errorf("max_age above the RFC maximum = %v, want clamped to %v", p.MaxAge, maxPolicyAge)
	}

	invalid := []struct{ name, body string }{
		{"missing version", "mode: none\nmax_age: 1\n"},
		{"wrong version", "version: STSv2\nmode: none\nmax_age: 1\n"},
		{"missing mode", "version: STSv1\nmax_age: 1\n"},
		{"unknown mode", "version: STSv1\nmode: strict\nmax_age: 1\n"},
		{"missing max_age", "version: STSv1\nmode: none\n"},
		{"negative max_age", "version: STSv1\nmode: none\nmax_age: -1\n"},
		{"max_age too many digits", "version: STSv1\nmode: none\nmax_age: 12345678901\n"},
		{"enforce without mx", "version: STSv1\nmode: enforce\nmax_age: 1\n"},
		{"testing without mx", "version: STSv1\nmode: testing\nmax_age: 1\n"},
		{"bare wildcard mx", "version: STSv1\nmode: enforce\nmx: *\nmax_age: 1\n"},
		{"mid-label wildcard", "version: STSv1\nmode: enforce\nmx: mail.*.example.com\nmax_age: 1\n"},
		{"line without colon", "version: STSv1\nmode none\nmax_age: 1\n"},
		{"empty value", "version: STSv1\nmode:\nmax_age: 1\n"},
		{"control character", "version: STSv1\nmode: none\x00\nmax_age: 1\n"},
		{"invalid UTF-8", "version: STSv1\nmode: none\nmax_age: 1\nx: \xff\n"},
		{"over 64 KiB", "version: STSv1\nmode: none\nmax_age: 1\n" + strings.Repeat("x: y\n", 64<<10/5+1)},
	}
	for _, tc := range invalid {
		if _, err := parsePolicy([]byte(tc.body)); !errors.Is(err, errInvalidPolicy) {
			t.Errorf("%s: err = %v, want errInvalidPolicy", tc.name, err)
		}
	}
}

func TestMXMatches(t *testing.T) {
	cases := []struct {
		pattern, host string
		want          bool
	}{
		// RFC 8461 §4.1's examples.
		{"*.example.com", "mail.example.com", true},
		{"*.example.com", "example.com", false},
		{"*.example.com", "foo.bar.example.com", false},
		{"mail.example.com", "mail.example.com", true},
		{"mail.example.com", "MAIL.Example.com.", true},
		{"mail.example.com", "mx.example.com", false},
		{"*.example.com", ".example.com", false},
		{"*.example.com", "mailexample.com", false},
	}
	for _, tc := range cases {
		if got := mxMatches(tc.pattern, tc.host); got != tc.want {
			t.Errorf("mxMatches(%q, %q) = %v, want %v", tc.pattern, tc.host, got, tc.want)
		}
	}
	p := MTASTSPolicy{MX: []string{"a.example.com", "*.example.net"}}
	if !policyMatchesMX(p, "x.example.net") || policyMatchesMX(p, "example.net") {
		t.Error("policyMatchesMX disagrees with mxMatches")
	}
}
