package smtpclient

import (
	"fmt"
	"strconv"
	"strings"

	smtp "github.com/kiliant/go-smtp"
)

// Limits is the registered subset of RFC 9422 server limits. Unknown limits
// remain available through Extension, so later IANA registrations do not need
// an API change before callers can observe them.
//
// Callers constructing a Limits literal must use keyed fields.
type Limits struct {
	// MailMax is the RFC 9422 MAILMAX transaction limit.
	MailMax uint32
	// RcptMax is the RFC 9422 RCPTMAX recipient limit.
	RcptMax uint32
	// RcptDomainMax is the RFC 9422 RCPTDOMAINMAX recipient-domain limit.
	RcptDomainMax uint32
	_             struct{}
}

// ParseLimitsParam parses the raw parameters from a LIMITS EHLO keyword. A
// malformed parameter list is rejected; malformed individual registered limits
// are ignored as RFC 9422 §3.7 requires.
func ParseLimitsParam(params string) (Limits, error) {
	var result Limits
	if params == "" {
		return result, nil
	}
	for _, field := range strings.Fields(params) {
		name, value, ok := strings.Cut(field, "=")
		if !ok || name == "" || value == "" || strings.Contains(value, "=") {
			return Limits{}, fmt.Errorf("smtpclient: invalid LIMITS parameter %q", field)
		}
		n := parseLimit(value)
		switch strings.ToUpper(name) {
		case "MAILMAX":
			if n != 0 {
				result.MailMax = n
			}
		case "RCPTMAX":
			if n != 0 {
				result.RcptMax = n
			}
		case "RCPTDOMAINMAX":
			if n != 0 {
				result.RcptDomainMax = n
			}
		}
	}
	return result, nil
}

func parseLimit(s string) uint32 {
	if len(s) == 0 || len(s) > 6 || s[0] == '0' {
		return 0
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil || n == 0 {
		return 0
	}
	return uint32(n)
}

// Limits reports the current RFC 9422 limits advertised by the server. It
// returns false if LIMITS was not advertised. A malformed advertisement is
// returned as an error instead of being silently interpreted as a limit.
func (c *Client) Limits() (Limits, bool, error) {
	params, ok := c.Extension(smtp.ExtLimits)
	if !ok {
		return Limits{}, false, nil
	}
	limits, err := ParseLimitsParam(params)
	return limits, true, err
}
