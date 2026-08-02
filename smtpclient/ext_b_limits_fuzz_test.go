package smtpclient

import "testing"

// FuzzParseLimitsParam keeps RFC 9422's open-ended advertisement parser
// total. Unknown limit names must never prevent known values from being
// observed, and malformed input must return an error rather than panic.
func FuzzParseLimitsParam(f *testing.F) {
	f.Add("")
	f.Add("RCPTMAX=20 MAILMAX=5 RCPTDOMAINMAX=3 FUTURE=word")
	f.Add("RCPTMAX")
	f.Add("RCPTMAX=000")

	f.Fuzz(func(t *testing.T, params string) {
		if len(params) > 64<<10 {
			t.Skip()
		}
		_, _ = ParseLimitsParam(params)
	})
}
