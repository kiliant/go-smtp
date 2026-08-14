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
// RFC 9422 §5 creates an IANA registry, so limit keywords will be added. Extra
// preserves limits this version does not model and lets a server advertise one
// without waiting for a package release.
//
// Callers constructing a Limits literal must use keyed fields.
type Limits struct {
	// MailMax is the RFC 9422 MAILMAX transaction limit.
	MailMax uint32
	// RcptMax is the RFC 9422 RCPTMAX recipient limit.
	RcptMax uint32
	// RcptDomainMax is the RFC 9422 RCPTDOMAINMAX recipient-domain limit.
	RcptDomainMax uint32
	// Extra contains open-ended RFC 9422 limit entries in their raw
	// space-separated EHLO form, including valueless entries. A string keeps
	// Limits comparable, preserving that property of the v1 type while allowing
	// future registry entries.
	// Known limits are represented by the fields above and never duplicated
	// here by ParseLimitsParam.
	Extra string
	_     struct{}
}

// ParseLimitsParam parses the raw parameters from a LIMITS EHLO keyword (RFC
// 9422 §3). A malformed parameter list is rejected; malformed individual
// registered limits are ignored as RFC 9422 §3.7 requires.
func ParseLimitsParam(params string) (Limits, error) {
	var result Limits
	if params == "" {
		return result, nil
	}
	fields := strings.Fields(params)
	if strings.Join(fields, " ") != params {
		return Limits{}, fmt.Errorf("smtp: invalid LIMITS parameter %q", params)
	}
	for _, field := range fields {
		name, value, hasValue := strings.Cut(field, "=")
		if !validLimitName(name) || hasValue && !validLimitValue(value) {
			return Limits{}, fmt.Errorf("smtp: invalid LIMITS parameter %q", field)
		}
		n := uint32(0)
		if hasValue {
			n = parseLimit(value)
		}
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
		default:
			if result.Extra != "" {
				result.Extra += " "
			}
			result.Extra += field
		}
	}
	return result, nil
}

func validLimitName(name string) bool {
	if name == "" {
		return false
	}
	for _, c := range name {
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func validLimitValue(value string) bool {
	if value == "" {
		return false
	}
	for _, c := range value {
		if c >= 0x21 && c <= 0x3a || c >= 0x3c && c <= 0x7e {
			continue
		}
		return false
	}
	return true
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
