package smtpwire

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// Sentinel errors returned by the command and parameter encoders. All
// encoding failures are returned to the caller before anything is written —
// a command that would desynchronise the session must never reach the wire.
var (
	// ErrInvalidCommandToken is returned when a verb or argument contains a
	// control byte (0x00-0x1F or 0x7F), which could otherwise smuggle a
	// bare CR, LF or NUL into the command stream.
	ErrInvalidCommandToken = errors.New("smtpwire: command token contains a control byte")
	// ErrEmptyKeyword is returned when an esmtp-param keyword is empty.
	ErrEmptyKeyword = errors.New("smtpwire: esmtp-param keyword is empty")
	// ErrInvalidKeyword is returned when an esmtp-param keyword does not
	// match esmtp-keyword = (ALPHA/DIGIT) *(ALPHA/DIGIT/"-").
	ErrInvalidKeyword = errors.New("smtpwire: invalid esmtp-param keyword")
	// ErrInvalidParamValue is returned when an esmtp-param value contains a
	// byte outside esmtp-value = 1*(%d33-60 / %d62-126) — i.e. it needs
	// xtext encoding (see EncodeXtext) before it can be sent.
	ErrInvalidParamValue = errors.New("smtpwire: esmtp-param value is not a valid esmtp-value; xtext-encode it first")
	// ErrXtextTruncatedEscape is returned by DecodeXtext when a "+" escape
	// is not followed by two hex digits.
	ErrXtextTruncatedEscape = errors.New("smtpwire: truncated xtext escape")
	// ErrXtextInvalidEscape is returned by DecodeXtext when a "+" escape's
	// two following bytes are not both hex digits.
	ErrXtextInvalidEscape = errors.New("smtpwire: invalid xtext escape")
	// ErrXtextInvalidChar is returned by DecodeXtext when an unescaped byte
	// falls outside the xtext-permitted printable range.
	ErrXtextInvalidChar = errors.New("smtpwire: invalid unescaped xtext character")
)

// EncodeCommand writes "VERB[ arg]...\r\n" to w. Every token is validated
// before anything is written: none may contain a control byte (0x00-0x1F or
// 0x7F, which includes CR and LF), so a caller-supplied argument can never
// desynchronise the command stream by smuggling a line break or NUL into it.
// Bytes 0x80-0xFF are permitted, so SMTPUTF8 addresses pass through
// untouched — this function only guards command-stream framing, not
// character set.
func EncodeCommand(w io.Writer, verb string, args ...string) error {
	if err := validateCommandToken(verb); err != nil {
		return fmt.Errorf("smtpwire: command verb %q: %w", verb, err)
	}
	var b strings.Builder
	b.Grow(len(verb) + 16)
	b.WriteString(verb)
	for _, a := range args {
		if err := validateCommandToken(a); err != nil {
			return fmt.Errorf("smtpwire: command argument %q: %w", a, err)
		}
		b.WriteByte(' ')
		b.WriteString(a)
	}
	b.WriteString("\r\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func validateCommandToken(s string) error {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7F {
			return ErrInvalidCommandToken
		}
	}
	return nil
}

// Param is a single esmtp-param, as sent on MAIL FROM or RCPT TO:
// "KEYWORD" when Value is empty, "KEYWORD=VALUE" otherwise.
type Param struct {
	Keyword string
	Value   string // "" means a valueless parameter.
}

// EncodeParam renders p as it appears on the wire, validating both the
// keyword and (if present) the value first. A value containing bytes
// outside esmtp-value — including CR, LF, space or '=' — is rejected with
// ErrInvalidParamValue rather than silently sent malformed; callers with
// such a value (ENVID, ORCPT) must xtext-encode it first with EncodeXtext.
func EncodeParam(p Param) (string, error) {
	if err := validateKeyword(p.Keyword); err != nil {
		return "", err
	}
	if p.Value == "" {
		return p.Keyword, nil
	}
	if err := validateESMTPValue(p.Value); err != nil {
		return "", err
	}
	return p.Keyword + "=" + p.Value, nil
}

// validateKeyword enforces esmtp-keyword = (ALPHA/DIGIT) *(ALPHA/DIGIT/"-").
func validateKeyword(kw string) error {
	if kw == "" {
		return ErrEmptyKeyword
	}
	for i := 0; i < len(kw); i++ {
		c := kw[i]
		alnum := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		if i == 0 {
			if !alnum {
				return fmt.Errorf("%w: %q", ErrInvalidKeyword, kw)
			}
			continue
		}
		if !alnum && c != '-' {
			return fmt.Errorf("%w: %q", ErrInvalidKeyword, kw)
		}
	}
	return nil
}

// validateESMTPValue enforces esmtp-value = 1*(%d33-60 / %d62-126): any
// printable US-ASCII byte except space (32) and '=' (61). Control bytes,
// DEL and non-ASCII bytes are rejected too, since none of them fall in
// 33-126.
func validateESMTPValue(v string) error {
	if v == "" {
		return fmt.Errorf("%w: empty value would be sent as valueless", ErrInvalidParamValue)
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c < 33 || c > 126 || c == '=' {
			return fmt.Errorf("%w: byte 0x%02X at offset %d", ErrInvalidParamValue, c, i)
		}
	}
	return nil
}

const xtextHexDigits = "0123456789ABCDEF"

// EncodeXtext encodes raw per RFC 3461 §4 xtext: bytes in the printable
// range 33-126 other than '+' and '=' pass through unchanged; every other
// byte, and '+' and '=' themselves, become a "+XX" uppercase-hex escape.
// Operates byte-wise, so it is agnostic to any text encoding raw carries
// (including UTF-8) — RFC 3461 defines xtext over octets, not characters.
//
// This function never fails: every byte has a representation, so an
// argument can always be made xtext-safe. Its output is always a valid
// esmtp-value (see validateESMTPValue): unescaped bytes are already in
// 33-126 excluding '=', and an escape triple uses only '+' (43) and hex
// digits (48-57, 65-70), all within the same safe range.
func EncodeXtext(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == '+' || c == '=' || c < 33 || c > 126 {
			b.WriteByte('+')
			b.WriteByte(xtextHexDigits[c>>4])
			b.WriteByte(xtextHexDigits[c&0x0F])
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// XtextParam builds a Param whose value is the xtext encoding of rawValue —
// the common case for ENVID= (RFC 3461) and ORCPT=.
func XtextParam(keyword, rawValue string) Param {
	return Param{Keyword: keyword, Value: EncodeXtext(rawValue)}
}

// DecodeXtext reverses EncodeXtext. It is lenient about which escapes it
// accepts — any "+XX" hex pair decodes, not only the ones this package's
// own encoder would have produced — per RFC 3461's guidance that receivers
// need not distinguish escaped from unescaped forms of the same octet. It
// is total: malformed input (a trailing bare '+', a non-hex escape, or an
// unescaped byte outside the permitted range) returns an error rather than
// panicking.
func DecodeXtext(s string) (string, error) {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '+' {
			if i+2 >= len(s) {
				return "", ErrXtextTruncatedEscape
			}
			hi, ok1 := hexVal(s[i+1])
			lo, ok2 := hexVal(s[i+2])
			if !ok1 || !ok2 {
				return "", ErrXtextInvalidEscape
			}
			b.WriteByte(byte(hi<<4 | lo))
			i += 2
			continue
		}
		if c < 33 || c > 126 {
			return "", ErrXtextInvalidChar
		}
		b.WriteByte(c)
	}
	return b.String(), nil
}

func hexVal(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	default:
		return 0, false
	}
}
