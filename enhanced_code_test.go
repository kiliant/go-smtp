package smtp

import "testing"

func TestParseEnhancedCode(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		want  EnhancedCode
		valid bool
	}{
		{"success", "2.1.5", EnhancedCode{Class: 2, Subject: 1, Detail: 5, Raw: "2.1.5"}, true},
		{"permanent policy", "5.7.1", EnhancedCode{Class: 5, Subject: 7, Detail: 1, Raw: "5.7.1"}, true},
		{"transient", "4.4.1", EnhancedCode{Class: 4, Subject: 4, Detail: 1, Raw: "4.4.1"}, true},
		{"multi-digit segments", "2.123.456", EnhancedCode{Class: 2, Subject: 123, Detail: 456, Raw: "2.123.456"}, true},
		{"zero segments still parse", "2.0.0", EnhancedCode{Class: 2, Subject: 0, Detail: 0, Raw: "2.0.0"}, true},

		// RFC 6533 registers "utf-8;" as an ORCPT= address-type, not an
		// enhanced code — included here only to document that this
		// parser is not the place that decides that; it just fails to
		// find three numeric segments and preserves Raw, which is the
		// named future-extension case this task must stress (T02 spec,
		// "Done when").
		{"garbage", "not-a-code", EnhancedCode{Raw: "not-a-code"}, false},
		{"too few segments", "2.1", EnhancedCode{Raw: "2.1"}, false},
		{"too many segments", "2.1.5.6", EnhancedCode{Raw: "2.1.5.6"}, false},
		{"empty segment", "2.1.", EnhancedCode{Raw: "2.1."}, false},
		{"leading empty segment", ".1.5", EnhancedCode{Raw: ".1.5"}, false},
		{"negative segment rejected", "2.-1.5", EnhancedCode{Raw: "2.-1.5"}, false},
		{"empty input", "", EnhancedCode{Raw: ""}, false},
		{"unregistered class not flattened", "9.9.9", EnhancedCode{Class: 9, Subject: 9, Detail: 9, Raw: "9.9.9"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseEnhancedCode(tt.raw)
			if got != tt.want {
				t.Errorf("ParseEnhancedCode(%q) = %+v, want %+v", tt.raw, got, tt.want)
			}
			if got.Raw != tt.raw {
				t.Errorf("ParseEnhancedCode(%q).Raw = %q, want the input preserved verbatim", tt.raw, got.Raw)
			}
			if got.Valid() != tt.valid {
				t.Errorf("ParseEnhancedCode(%q).Valid() = %v, want %v", tt.raw, got.Valid(), tt.valid)
			}
		})
	}
}

func TestEnhancedCodeString(t *testing.T) {
	// Raw wins when set, even for a code that also parsed cleanly — the
	// point is fidelity to what the server actually sent.
	if got, want := ParseEnhancedCode("2.1.5").String(), "2.1.5"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	// A caller-built EnhancedCode with no Raw formats from the fields.
	c := EnhancedCode{Class: 5, Subject: 7, Detail: 1}
	if got, want := c.String(), "5.7.1"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

// FuzzParseEnhancedCode asserts ParseEnhancedCode never panics on
// adversarial input and always preserves the input in Raw — the "must not
// be flattened" contract in docs/API-STABILITY.md §1c holds for every byte
// sequence, not just the examples in the table test above.
func FuzzParseEnhancedCode(f *testing.F) {
	seeds := []string{
		"2.1.5", "5.7.1", "4.4.1", "", ".", "..", "2.1.5.6", "2.1",
		"9.9.9", "-1.-1.-1", "2..5", "２.１.５", "2.1.5\x00", "2.1.5 ",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		c := ParseEnhancedCode(raw)
		if c.Raw != raw {
			t.Fatalf("ParseEnhancedCode(%q).Raw = %q, want input preserved verbatim", raw, c.Raw)
		}
		_ = c.String()
		_ = c.Valid()
	})
}
