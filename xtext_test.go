package smtp

import "testing"

// TestEncodeXtextKnownVectors pins the exact golden vectors that
// internal/smtpwire's TestEncodeXtextKnownVectors pins. The two encoders are
// deliberate twins — package smtp may not import the module's internal
// packages — so these tables are the mechanism that keeps them from
// drifting. If you change one, change the other.
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
		if got := EncodeXtext(tt.raw); got != tt.want {
			t.Errorf("EncodeXtext(%q) = %q, want %q", tt.raw, got, tt.want)
		}
	}
}

// TestEncodeXtextForDSNParameters is the reason this function is exported:
// a caller reaching for the Extra escape hatch to send a parameter this
// library does not model yet must be able to produce a conformant value
// without reimplementing RFC 3461 §4.
func TestEncodeXtextForDSNParameters(t *testing.T) {
	opts := &RcptOptions{Extra: []Param{{
		Keyword: "ORCPT",
		Value:   "rfc822;" + EncodeXtext("user name@example.com"),
	}}}
	got := opts.Extra[0].Value
	want := "rfc822;user+20name@example.com"
	if got != want {
		t.Fatalf("ORCPT value = %q, want %q", got, want)
	}

	envid := EncodeXtext("queue-id with spaces+plus")
	if envid != "queue-id+20with+20spaces+2Bplus" {
		t.Fatalf("ENVID value = %q", envid)
	}
}

// isESMTPValue reports whether s satisfies RFC 5321 §4.1.2's
// esmtp-value = 1*(%d33-60 / %d62-126): printable US-ASCII except space and
// '='. Written here rather than imported because package smtp imports
// nothing from this module.
//
// The 1* is enforced, not decorative. A version of this predicate that
// accepted "" would have quoted the grammar in its comment and then been
// unable to catch the single input that violates it — see
// TestEncodeXtextEmptyIsNotAnESMTPValue.
func isESMTPValue(s string) bool {
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 33 || c > 126 || c == '=' {
			return false
		}
	}
	return true
}

// TestEncodeXtextEmptyIsNotAnESMTPValue pins the documented exception. The
// behaviour is correct — escaping cannot make an empty string non-empty —
// but it is silent: smtpwire.EncodeParam renders a Param with an empty Value
// as a valueless parameter, so Param{Keyword: "ENVID", Value:
// EncodeXtext("")} goes out as "ENVID" rather than "ENVID=", with no error.
// The test exists so the exception is visible to the next reader instead of
// hiding inside a golden vector.
func TestEncodeXtextEmptyIsNotAnESMTPValue(t *testing.T) {
	if got := EncodeXtext(""); got != "" {
		t.Fatalf("EncodeXtext(\"\") = %q, want \"\"", got)
	}
	if isESMTPValue("") {
		t.Fatal("isESMTPValue(\"\") = true; esmtp-value is 1*(...), so the empty string does not satisfy it")
	}
}

// FuzzEncodeXtext asserts the property the doc comment promises for every
// input, not just the table above: the output of EncodeXtext is always a
// valid esmtp-value, so it can always be sent. A value that needed further
// escaping would desynchronise the command stream against a strict server.
func FuzzEncodeXtext(f *testing.F) {
	seeds := []string{
		"", "plain", "+", "=", " ", "\r\n", "user@example.com",
		"rfc822;user@example.com", "\x00\x7f\xff", "naïve@example.com",
		"queue-id with spaces+plus", "=+=+=",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		got := EncodeXtext(raw)
		// The empty input is the documented exception: "" encodes to "",
		// which is not an esmtp-value, and no escaping can change that.
		// Handled explicitly so the property below is asserted for every
		// other input rather than weakened to accommodate this one.
		if raw == "" {
			if got != "" {
				t.Fatalf("EncodeXtext(\"\") = %q, want \"\"", got)
			}
			return
		}
		if !isESMTPValue(got) {
			t.Fatalf("EncodeXtext(%q) = %q, which is not a valid esmtp-value", raw, got)
		}
		// Every escape must be a '+' followed by exactly two upper-case
		// hex digits (RFC 3461 §4); a truncated or lower-case escape is
		// not conformant and a receiver may reject or misread it.
		for i := 0; i < len(got); i++ {
			if got[i] != '+' {
				continue
			}
			if i+2 >= len(got) {
				t.Fatalf("EncodeXtext(%q) = %q: truncated escape at %d", raw, got, i)
			}
			for _, h := range []byte{got[i+1], got[i+2]} {
				isUpperHex := (h >= '0' && h <= '9') || (h >= 'A' && h <= 'F')
				if !isUpperHex {
					t.Fatalf("EncodeXtext(%q) = %q: non-upper-hex escape digit %q at %d", raw, got, h, i)
				}
			}
			i += 2
		}
	})
}
