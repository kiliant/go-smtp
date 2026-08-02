package smtp

import (
	"strconv"
	"strings"
)

// EnhancedCode is an RFC 3463 enhanced mail system status code: the
// "class.subject.detail" structure sent as the first token of many SMTP
// reply texts (RFC 2034, ENHANCEDSTATUSCODES). RFC 3463 defines the
// structure; RFC 5248 establishes the IANA registry of subject/detail
// values, which grows independently of both RFC 3463 and the extension
// registry.
//
// Per docs/API-STABILITY.md §1c, an enhanced code is never flattened: Raw
// always holds the code exactly as the server sent it, so a caller matching
// on the 5.7.x security-and-policy class can still see a detail value this
// library has never heard of. Class/Subject/Detail are populated only when
// the three dot-separated numeric parts parse; an EnhancedCode built from
// unparseable input keeps Raw with Class, Subject and Detail all zero,
// rather than collapsing to a sentinel "unknown" value.
//
// Match on the numeric fields, not with ==. EnhancedCode is comparable and
// Raw participates in equality, so a parsed 5.7.1 does not equal
// EnhancedCode{Class: 5, Subject: 7, Detail: 1} — the parsed value carries
// Raw: "5.7.1" and the constructed one does not. Compare Class, Subject and
// Detail directly.
//
// Callers constructing an EnhancedCode literal must use keyed fields.
type EnhancedCode struct {
	// Class is the first digit: 2 (success), 4 (persistent transient
	// failure) or 5 (permanent failure). RFC 3463 §3.
	Class int
	// Subject is the second digit group. RFC 3463 §3.2; registry in RFC
	// 5248.
	Subject int
	// Detail is the third digit group. RFC 3463 §3.3; registry in RFC
	// 5248.
	Detail int
	// Raw is the code exactly as sent on the wire (e.g. "5.7.1"),
	// retained even when it does not parse into Class/Subject/Detail, or
	// empty when the reply carried no enhanced code at all.
	Raw string

	_ struct{}
}

// ParseEnhancedCode parses raw as an RFC 3463 "class.subject.detail" code.
// It always succeeds: Raw is set to the input verbatim, and Class, Subject
// and Detail are populated only when all three dot-separated segments parse
// as non-negative decimal integers. An unparseable code is not an error —
// docs/API-STABILITY.md §1c requires it survive as Raw rather than be
// discarded.
//
// Syntactic extraction of the leading "class.subject.detail" token from a
// full reply line — deciding where the code ends and the free-text portion
// begins — is internal/smtpwire's job (T01), not this function's; callers
// pass ParseEnhancedCode the already-isolated code substring.
//
// This function is deliberately more permissive than that extractor: it
// accepts any three dot-separated non-negative integers, so "9.99999.0"
// parses, where the wire-side extractor enforces RFC 3463's class 2/4/5 and
// 1*3DIGIT segments before it will treat a token as a code at all. The
// asymmetry is intentional — the extractor decides *whether these bytes are
// a code*, and must be strict or it will eat the reply text; this function
// is handed something already identified as one, and preserving it beats
// second-guessing it.
func ParseEnhancedCode(raw string) EnhancedCode {
	c := EnhancedCode{Raw: raw}

	segs := strings.Split(raw, ".")
	if len(segs) != 3 {
		return c
	}

	class, ok1 := parseDigits(segs[0])
	subject, ok2 := parseDigits(segs[1])
	detail, ok3 := parseDigits(segs[2])
	if !ok1 || !ok2 || !ok3 {
		return c
	}

	c.Class, c.Subject, c.Detail = class, subject, detail
	return c
}

// parseDigits parses s as a non-negative decimal integer, rejecting empty
// strings and any sign prefix — RFC 3463's grammar for each segment is
// 1*3DIGIT, never signed.
func parseDigits(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

// Valid reports whether Class is one of the three values RFC 3463 §3
// defines: 2 (success), 4 (persistent transient failure) or 5 (permanent
// failure). It does not validate Subject or Detail, whose registry (RFC
// 5248) grows independently and is never exhaustively known to this
// library.
func (c EnhancedCode) Valid() bool {
	return c.Class == 2 || c.Class == 4 || c.Class == 5
}

// String returns Raw when set, since that is the code exactly as the server
// sent it; otherwise it formats Class.Subject.Detail, for an EnhancedCode a
// caller constructed directly rather than parsed.
//
// The zero EnhancedCode formats as the empty string, not "0.0.0": a zero
// value means the reply carried no enhanced code at all, and printing a
// syntactically valid-looking code for its absence is worse than printing
// nothing.
func (c EnhancedCode) String() string {
	if c.Raw != "" {
		return c.Raw
	}
	if c.Class == 0 && c.Subject == 0 && c.Detail == 0 {
		return ""
	}
	return strconv.Itoa(c.Class) + "." + strconv.Itoa(c.Subject) + "." + strconv.Itoa(c.Detail)
}
