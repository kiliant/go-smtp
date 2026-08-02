package smtpclient

import "testing"

// FuzzParseSizeParam keeps RFC 1870's optional SIZE EHLO parameter parser
// total for arbitrary server input. A bare SIZE is valid; malformed or
// overflowing maxima must return an error rather than panic.
func FuzzParseSizeParam(f *testing.F) {
	f.Add("")
	f.Add("10485760")
	f.Add("0")
	f.Add("18446744073709551615")
	f.Add("1 2")

	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 64<<10 {
			t.Skip()
		}
		_, _ = parseSizeParam(raw)
	})
}
