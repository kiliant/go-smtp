package saslprep

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func FuzzPrepare(f *testing.F) {
	f.Add("user")
	f.Add("I\u00ADX")
	f.Add("a\u00A0b")
	f.Add("\u05D0\u05D1")
	f.Add("\xff")
	f.Add("\u0000")

	f.Fuzz(func(t *testing.T, in string) {
		prepared, err := Prepare(in)
		if err != nil {
			return
		}
		if !utf8.ValidString(prepared) {
			t.Fatalf("Prepare accepted %q but returned invalid UTF-8 %q", in, prepared)
		}
		again, err := Prepare(prepared)
		if err != nil || again != prepared {
			t.Fatalf("Prepare is not idempotent: %q -> %q -> %q, err=%v", in, prepared, again, err)
		}
		if strings.ContainsRune(prepared, '\u00ad') {
			t.Fatalf("Prepare retained mapped-to-nothing soft hyphen: %q", prepared)
		}
		_, _ = PrepareStored(in)
	})
}
