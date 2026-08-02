package unicodenorm

import "testing"

// FuzzNormalise exercises both public normalization forms with arbitrary
// UTF-8 and arbitrary byte strings. Normalization must be total and
// idempotent; malformed UTF-8 is deliberately included because Go strings
// can carry it and both functions document replacement-rune handling.
func FuzzNormalise(f *testing.F) {
	f.Add("")
	f.Add("e\u0301")
	f.Add("\u00b5")
	f.Add("\xff")
	f.Add("\u1100\u1161\u11a8")

	f.Fuzz(func(t *testing.T, in string) {
		for _, normalize := range []func(string) string{NFC, NFKC} {
			got := normalize(in)
			if again := normalize(got); again != got {
				t.Fatalf("normalization is not idempotent: %q -> %q -> %q", in, got, again)
			}
		}
	})
}
