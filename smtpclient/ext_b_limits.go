package smtpclient

import (
	smtp "github.com/kiliant/go-smtp"
)

// Limits is the registered subset of RFC 9422 server limits. It is an alias for
// smtp.Limits, which is where the type lives: LIMITS is an advertisement a
// server produces and a client parses, so it is shared vocabulary rather than
// client vocabulary (docs/API-STABILITY.md §10). The alias preserves type
// identity, so existing callers and keyed struct literals are unaffected.
type Limits = smtp.Limits

// ParseLimitsParam parses the raw parameters from a LIMITS EHLO keyword (RFC
// 9422 §3). It forwards to smtp.ParseLimitsParam; see Limits for why the
// implementation moved.
func ParseLimitsParam(params string) (smtp.Limits, error) {
	return smtp.ParseLimitsParam(params)
}

// Limits reports the current RFC 9422 limits advertised by the server. It
// returns false if LIMITS was not advertised. A malformed advertisement is
// returned as an error instead of being silently interpreted as a limit.
//
// This accessor stays on Client: it reads negotiated session state, which is
// client-side by nature even though the type it returns is not.
func (c *Client) Limits() (smtp.Limits, bool, error) {
	params, ok := c.Extension(smtp.ExtLimits)
	if !ok {
		return smtp.Limits{}, false, nil
	}
	limits, err := smtp.ParseLimitsParam(params)
	return limits, true, err
}
