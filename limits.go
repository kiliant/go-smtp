package smtp

import (
	"fmt"
	"strconv"
	"strings"
)

// Limits is the registered subset of RFC 9422 LIMITS server limits. Unknown
// limits remain available through the raw parameter string of the LIMITS EHLO
// keyword, so later IANA registrations do not need an API change before callers
// can observe them.
//
// LIMITS is an advertisement: a client parses it, a server produces it. It lives
// in package smtp rather than in a direction-specific package for that reason —
// see docs/API-STABILITY.md §10.
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

// ParseLimitsParam parses the raw parameters from a LIMITS EHLO keyword (RFC
// 9422 §3). A malformed parameter list is rejected; malformed individual
// registered limits are ignored as RFC 9422 §3.7 requires.
func ParseLimitsParam(params string) (Limits, error) {
	var result Limits
	if params == "" {
		return result, nil
	}
	for _, field := range strings.Fields(params) {
		name, value, ok := strings.Cut(field, "=")
		if !ok || name == "" || value == "" || strings.Contains(value, "=") {
			return Limits{}, fmt.Errorf("smtp: invalid LIMITS parameter %q", field)
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
