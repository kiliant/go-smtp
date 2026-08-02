package smtpwire

// EnhancedCode is an RFC 3463 enhanced mail system status code
// ("class.subject.detail") extracted from the front of a reply's text.
//
// Extraction is purely syntactic — it never consults whether the server
// advertised ENHANCEDSTATUSCODES. That policy decision (whether to expect,
// require, or ignore the prefix) belongs to smtpclient.
type EnhancedCode struct {
	Class, Subject, Detail int
	// Raw is the code exactly as it appeared on the wire, e.g. "2.1.5".
	// Always populated when a code was found, even if Class/Subject/Detail
	// could not be parsed as expected — an unparseable or unregistered
	// code must survive to the caller rather than collapse to zero. In
	// this package's extraction, Raw only exists when the strict grammar
	// below matched, so Class/Subject/Detail are always meaningful
	// alongside it; the "preserve the raw form" contract chiefly matters
	// one layer up, where smtpclient decides how to handle a code this
	// package did not recognise as present at all.
	Raw string
}

// ExtractEnhancedCode looks for an RFC 3463 status code at the very start of
// text: a single class digit (2, 4 or 5), a dot, 1-3 subject digits, a dot,
// 1-3 detail digits, and then either end-of-string or a single space
// separating it from the free-form message.
//
// Grammar (RFC 3463 §3, as used in a reply per RFC 3463 §2 / RFC 2034):
//
//	status-code = class "." subject "." detail
//	class       = "2" / "4" / "5"
//	subject     = 1*3DIGIT
//	detail      = 1*3DIGIT
//
// On a match, ok is true, code holds the parsed integers plus the exact
// matched substring in Raw, and rest is text with the code and its
// separating space removed. On no match — including a fourth consecutive
// digit in subject or detail, which the 1*3DIGIT grammar forbids — ok is
// false, code is the zero value, and rest is text unchanged. This function
// never errors and never panics: any input, including the empty string, is
// handled by returning ok=false.
func ExtractEnhancedCode(text string) (code EnhancedCode, rest string, ok bool) {
	i := 0
	n := len(text)

	if i >= n || (text[i] != '2' && text[i] != '4' && text[i] != '5') {
		return EnhancedCode{}, text, false
	}
	class := int(text[i] - '0')
	i++

	if i >= n || text[i] != '.' {
		return EnhancedCode{}, text, false
	}
	i++

	subject, adv, ok := scanUpTo3Digits(text[i:])
	if !ok {
		return EnhancedCode{}, text, false
	}
	i += adv

	if i >= n || text[i] != '.' {
		return EnhancedCode{}, text, false
	}
	i++

	detail, adv, ok := scanUpTo3Digits(text[i:])
	if !ok {
		return EnhancedCode{}, text, false
	}
	i += adv

	// Terminator: end of string, or exactly one space before free text.
	// A digit here means subject/detail had a fourth digit, which the
	// grammar forbids; anything else that isn't a space is not a valid
	// separator either way, so neither is a match.
	raw := text[:i]
	switch {
	case i == n:
		return EnhancedCode{Class: class, Subject: subject, Detail: detail, Raw: raw}, "", true
	case text[i] == ' ':
		return EnhancedCode{Class: class, Subject: subject, Detail: detail, Raw: raw}, text[i+1:], true
	default:
		return EnhancedCode{}, text, false
	}
}

// scanUpTo3Digits reads 1 to 3 ASCII digits from the front of s and returns
// their integer value and how many bytes were consumed. ok is false if s
// does not start with a digit, or if a fourth consecutive digit follows the
// first three (the 1*3DIGIT grammar caps at 3; no backtracking is
// attempted, matching a strict reading of RFC 3463).
func scanUpTo3Digits(s string) (val int, consumed int, ok bool) {
	i := 0
	for i < len(s) && i < 3 && s[i] >= '0' && s[i] <= '9' {
		val = val*10 + int(s[i]-'0')
		i++
	}
	if i == 0 {
		return 0, 0, false
	}
	if i < len(s) && s[i] >= '0' && s[i] <= '9' {
		// A fourth digit — not a valid 1*3DIGIT field.
		return 0, 0, false
	}
	return val, i, true
}
