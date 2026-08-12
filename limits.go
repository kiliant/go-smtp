package smtp

import (
	"fmt"
	"strconv"
	"strings"
)

// Limits is the registered subset of RFC 9422 LIMITS server limits.
//
// LIMITS is an advertisement: a client parses it, a server produces it. It lives
// in package smtp rather than in a direction-specific package for that reason —
// see docs/API-STABILITY.md §10.
//
// RFC 9422 §5 creates an IANA registry, so limit keywords will be added. The two
// directions are not yet symmetric about them, and this is deliberate rather
// than overlooked: a *reading* client sees an unmodelled limit in the raw
// parameter string of the LIMITS keyword, via smtpclient.Client.Extension, so
// nothing is lost. An *advertising* server has no way to express one — this type
// has three uint32 fields, no Extra, and this package ships a parser with no
// formatter. Adding either is additive (the field guard below is what makes it
// so) and belongs to the task that gives a server something to advertise with,
// not to a speculative field now.
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
