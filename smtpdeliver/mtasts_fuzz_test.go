package smtpdeliver

// Fuzz targets for the RFC 8461 parsers. Created by T27; owned by T30 from
// here on (docs/tasks/BOARD.md).

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func FuzzParseSTSRecord(f *testing.F) {
	for _, seed := range []string{
		"v=STSv1; id=20160831085700Z;",
		"v=STSv1;id=1",
		"v=STSv1 ;\tid=abc ; ext=x",
		"v=STSv1; id=" + strings.Repeat("a", 33),
		"v=STSv1;; id=1",
		"",
		";",
	} {
		f.Add(seed, "v=spf1 -all")
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		id, err := parseSTSRecords([]string{a, b})
		if err != nil {
			return
		}
		if !validSTSID(id) {
			t.Fatalf("accepted id %q that violates 1*32(ALPHA / DIGIT)", id)
		}
		if !beginsWithSTSVersion(a) && !beginsWithSTSVersion(b) {
			t.Fatalf("accepted %q / %q without a v=STSv1 record", a, b)
		}
	})
}

func FuzzParsePolicy(f *testing.F) {
	for _, seed := range []string{
		examplePolicy,
		"version: STSv1\nmode: none\nmax_age: 0",
		"version: STSv1\nmode: enforce\nmx: *.example.com\nmax_age: 9999999999\n",
		"version: STSv1\r\nmode: testing\r\nmx: a\r\nmax_age: 1\r\n\r\n",
		"\x00\xff",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		p, err := parsePolicy(body)
		if err != nil {
			return
		}
		if len(body) > maxPolicyBody || !utf8.Valid(body) {
			t.Fatal("accepted an over-long or non-UTF-8 body")
		}
		if p.Version != "STSv1" || p.MaxAge < 0 || p.MaxAge > maxPolicyAge {
			t.Fatalf("accepted invalid policy %+v", p)
		}
		switch p.Mode {
		case MTASTSEnforce, MTASTSTesting:
			if len(p.MX) == 0 {
				t.Fatalf("accepted mode %s without mx", p.Mode)
			}
		case MTASTSNone:
		default:
			t.Fatalf("accepted unknown mode %q", p.Mode)
		}
		for _, pattern := range p.MX {
			if _, err := normalizeMXPattern(pattern); err != nil || pattern != strings.ToLower(pattern) {
				t.Fatalf("accepted un-normalised mx %q", pattern)
			}
		}
		// Reparsing is what a cache Load does; it must agree with itself.
		again, err := parsePolicy(body)
		if err != nil || again.Mode != p.Mode || again.MaxAge != p.MaxAge || strings.Join(again.MX, ",") != strings.Join(p.MX, ",") {
			t.Fatal("reparsing the same body disagreed")
		}
	})
}

func FuzzMXMatches(f *testing.F) {
	f.Add("*.example.com", "mail.example.com")
	f.Add("mail.example.com", "MAIL.example.com.")
	f.Add("*.example.com", "a.b.example.com")
	f.Fuzz(func(t *testing.T, raw, host string) {
		pattern, err := normalizeMXPattern(raw)
		if err != nil {
			return
		}
		if !mxMatches(pattern, host) {
			return
		}
		h := strings.ToLower(strings.TrimSuffix(host, "."))
		if suffix, wildcard := strings.CutPrefix(pattern, "*."); wildcard {
			label, rest, _ := strings.Cut(h, ".")
			if label == "" || rest != suffix {
				t.Fatalf("%q matched %q with more or less than one label", pattern, host)
			}
		} else if h != pattern {
			t.Fatalf("%q matched %q without equality", pattern, host)
		}
	})
}
