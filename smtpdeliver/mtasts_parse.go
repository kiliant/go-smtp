package smtpdeliver

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// maxPolicyBody is RFC 8461 §3.3's suggested maximum policy size, which
// DELIVERY-DESIGN.md §5 makes a hard limit.
const maxPolicyBody = 64 << 10

// maxPolicyAge is RFC 8461 §3.2's maximum max_age.
const maxPolicyAge = 31557600 * time.Second

var (
	errNoSTSRecord      = errors.New("smtpdeliver: no usable MTA-STS TXT record (RFC 8461 §3.1)")
	errInvalidSTSRecord = errors.New("smtpdeliver: invalid MTA-STS TXT record (RFC 8461 §3.1)")
	errInvalidPolicy    = errors.New("smtpdeliver: invalid MTA-STS policy (RFC 8461 §3.2)")
)

// parseSTSRecords selects and parses the "_mta-sts" TXT record from a TXT
// answer (RFC 8461 §3.1) and returns its policy id. Records that do not begin
// with the version field are discarded; anything other than exactly one
// remaining record means the domain has no available policy.
func parseSTSRecords(records []string) (string, error) {
	var candidates []string
	for _, r := range records {
		if beginsWithSTSVersion(r) {
			candidates = append(candidates, r)
		}
	}
	if len(candidates) != 1 {
		return "", fmt.Errorf("%w: %d candidate records", errNoSTSRecord, len(candidates))
	}
	return parseSTSRecord(candidates[0])
}

// beginsWithSTSVersion reports whether r starts with "v=STSv1" followed by
// the field delimiter *WSP ";" (RFC 8461 §3.1 ABNF).
func beginsWithSTSVersion(r string) bool {
	rest, ok := strings.CutPrefix(r, "v=STSv1")
	if !ok {
		return false
	}
	return strings.HasPrefix(strings.TrimLeft(rest, " \t"), ";")
}

// parseSTSRecord parses one record against the RFC 8461 §3.1 ABNF. Unknown
// extension fields are validated and ignored; a duplicated field keeps its
// first value.
func parseSTSRecord(r string) (string, error) {
	fields := strings.Split(r, ";")
	// A single trailing delimiter is allowed: "v=STSv1; id=1;".
	if last := strings.Trim(fields[len(fields)-1], " \t"); last == "" && len(fields) > 1 {
		fields = fields[:len(fields)-1]
	}
	if strings.TrimRight(fields[0], " \t") != "v=STSv1" {
		return "", fmt.Errorf("%w: must begin with v=STSv1", errInvalidSTSRecord)
	}
	id := ""
	for _, raw := range fields[1:] {
		field := strings.Trim(raw, " \t")
		name, value, ok := strings.Cut(field, "=")
		if !ok || !validSTSExtName(name) {
			return "", fmt.Errorf("%w: malformed field %q", errInvalidSTSRecord, field)
		}
		if name == "id" {
			if !validSTSID(value) {
				return "", fmt.Errorf("%w: id %q must be 1 to 32 letters or digits", errInvalidSTSRecord, value)
			}
			if id == "" {
				id = value
			}
			continue
		}
		if !validSTSExtValue(value) {
			return "", fmt.Errorf("%w: malformed value for %q", errInvalidSTSRecord, name)
		}
	}
	if id == "" {
		return "", fmt.Errorf("%w: missing id", errInvalidSTSRecord)
	}
	return id, nil
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func validSTSID(v string) bool {
	if len(v) == 0 || len(v) > 32 {
		return false
	}
	for i := 0; i < len(v); i++ {
		if !isAlnum(v[i]) {
			return false
		}
	}
	return true
}

// validSTSExtName checks sts-ext-name / sts-policy-ext-name: an alphanumeric
// followed by up to 31 alphanumerics, "_", "-" or ".".
func validSTSExtName(n string) bool {
	if len(n) == 0 || len(n) > 32 || !isAlnum(n[0]) {
		return false
	}
	for i := 1; i < len(n); i++ {
		if c := n[i]; !isAlnum(c) && c != '_' && c != '-' && c != '.' {
			return false
		}
	}
	return true
}

// validSTSExtValue checks sts-ext-value: visible ASCII excluding "=" and ";".
func validSTSExtValue(v string) bool {
	if v == "" {
		return false
	}
	for i := 0; i < len(v); i++ {
		if c := v[i]; c < 0x21 || c > 0x7e || c == '=' || c == ';' {
			return false
		}
	}
	return true
}

// parsePolicy parses an RFC 8461 §3.2 policy file. Lines end in LF or CRLF;
// empty lines are tolerated. Unknown fields are ignored, a repeated field
// other than "mx" keeps its first value, and a max_age above the RFC maximum
// is clamped to it, so a domain asking for longer protection gets the
// longest the RFC allows rather than none.
func parsePolicy(body []byte) (MTASTSPolicy, error) {
	if len(body) > maxPolicyBody {
		return MTASTSPolicy{}, fmt.Errorf("%w: body exceeds %d bytes", errInvalidPolicy, maxPolicyBody)
	}
	if !utf8.Valid(body) {
		return MTASTSPolicy{}, fmt.Errorf("%w: body is not UTF-8", errInvalidPolicy)
	}
	var (
		p                     MTASTSPolicy
		haveVersion, haveMode bool
		haveMaxAge            bool
	)
	for _, line := range bytes.Split(body, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		text := strings.TrimRight(string(line), " \t")
		if text == "" {
			continue
		}
		name, value, ok := strings.Cut(text, ":")
		if !ok || !validSTSExtName(name) {
			return MTASTSPolicy{}, fmt.Errorf("%w: malformed line %q", errInvalidPolicy, text)
		}
		value = strings.TrimLeft(value, " \t")
		if value == "" || hasControl(value) {
			return MTASTSPolicy{}, fmt.Errorf("%w: malformed value for %q", errInvalidPolicy, name)
		}
		switch name {
		case "version":
			if haveVersion {
				continue
			}
			haveVersion = true
			p.Version = value
		case "mode":
			if haveMode {
				continue
			}
			haveMode = true
			p.Mode = MTASTSMode(value)
		case "max_age":
			if haveMaxAge {
				continue
			}
			haveMaxAge = true
			age, err := parseMaxAge(value)
			if err != nil {
				return MTASTSPolicy{}, err
			}
			p.MaxAge = age
		case "mx":
			pattern, err := normalizeMXPattern(value)
			if err != nil {
				return MTASTSPolicy{}, err
			}
			p.MX = append(p.MX, pattern)
		}
	}
	if err := validatePolicy(p, haveVersion, haveMode, haveMaxAge); err != nil {
		return MTASTSPolicy{}, err
	}
	return p, nil
}

func validatePolicy(p MTASTSPolicy, haveVersion, haveMode, haveMaxAge bool) error {
	switch {
	case !haveVersion || p.Version != "STSv1":
		return fmt.Errorf("%w: version must be STSv1", errInvalidPolicy)
	case !haveMode:
		return fmt.Errorf("%w: missing mode", errInvalidPolicy)
	case !haveMaxAge:
		return fmt.Errorf("%w: missing max_age", errInvalidPolicy)
	}
	switch p.Mode {
	case MTASTSEnforce, MTASTSTesting:
		if len(p.MX) == 0 {
			return fmt.Errorf("%w: mode %s requires at least one mx", errInvalidPolicy, p.Mode)
		}
	case MTASTSNone:
	default:
		return fmt.Errorf("%w: unknown mode %q", errInvalidPolicy, p.Mode)
	}
	return nil
}

func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < 0x20 && c != '\t') || c == 0x7f {
			return true
		}
	}
	return false
}

// parseMaxAge parses 1*10 DIGIT seconds, clamped to RFC 8461's maximum.
func parseMaxAge(v string) (time.Duration, error) {
	if len(v) == 0 || len(v) > 10 {
		return 0, fmt.Errorf("%w: max_age %q must be 1 to 10 digits", errInvalidPolicy, v)
	}
	for i := 0; i < len(v); i++ {
		if v[i] < '0' || v[i] > '9' {
			return 0, fmt.Errorf("%w: max_age %q must be digits", errInvalidPolicy, v)
		}
	}
	n, err := strconv.ParseUint(v, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: max_age %q: %v", errInvalidPolicy, v, err)
	}
	age := time.Duration(n) * time.Second
	if n > uint64(maxPolicyAge/time.Second) {
		age = maxPolicyAge
	}
	return age, nil
}

// normalizeMXPattern validates an "mx" value, ["*."] Domain (RFC 8461 §3.2),
// and returns it in lower case without a trailing dot.
func normalizeMXPattern(v string) (string, error) {
	wildcard := strings.HasPrefix(v, "*.")
	domain, err := normalizeDomain(strings.TrimPrefix(v, "*."))
	if err != nil {
		return "", fmt.Errorf("%w: mx %q: %v", errInvalidPolicy, v, err)
	}
	if wildcard {
		return "*." + domain, nil
	}
	return domain, nil
}

// mxMatches reports whether an MX host name matches a normalised policy
// pattern (RFC 8461 §4.1): exactly, or by replacing exactly one left-most
// label for a "*." pattern.
func mxMatches(pattern, host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	suffix, wildcard := strings.CutPrefix(pattern, "*.")
	if !wildcard {
		return host == pattern
	}
	label, rest, ok := strings.Cut(host, ".")
	return ok && label != "" && rest == suffix
}

// policyMatchesMX reports whether any of p's patterns matches host.
func policyMatchesMX(p MTASTSPolicy, host string) bool {
	for _, pattern := range p.MX {
		if mxMatches(pattern, host) {
			return true
		}
	}
	return false
}
