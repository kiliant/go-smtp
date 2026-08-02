package smtp

import "strings"

// EncodeXtext encodes raw as RFC 3461 §4 "xtext", the escaping DSN uses for
// esmtp-param values that would otherwise be illegal on the wire:
//
//	xtext = *( xchar / hexchar )
//	xchar = any ASCII CHAR between "!" (33) and "~" (126) except "+" and "="
//	hexchar = ASCII "+" immediately followed by two upper case hexadecimal digits
//
// Bytes in 33-126 other than '+' and '=' pass through unchanged; every other
// byte, and '+' and '=' themselves, become a "+XX" upper-case hex escape.
// Encoding is byte-wise, so raw may carry any text encoding including UTF-8 —
// RFC 3461 defines xtext over octets, not characters.
//
// It never fails: every byte has a representation, so any value can be made
// safe to send. For a non-empty raw the result is always a valid esmtp-value
// (RFC 5321 §4.1.2), because unescaped bytes are already in 33-126 excluding
// '=', and an escape uses only '+' and hex digits, all within that range.
//
// The empty string is the one exception, and it is worth stating because it
// fails quietly rather than loudly. esmtp-value is 1*(%d33-60 / %d62-126) —
// one octet or more — so "" is not a valid esmtp-value, and there is nothing
// EncodeXtext can do about it: emptiness is not a property escaping can
// remove. A Param whose Value is empty is written as a *valueless* parameter,
// so
//
//	smtp.Param{Keyword: "ENVID", Value: smtp.EncodeXtext("")}
//
// is sent as "ENVID", not "ENVID=" — a different parameter, with no error
// along the way. Callers that may hold an empty value must decide what that
// means before encoding it, rather than relying on this function to signal
// it.
//
// Use it for the MAIL and RCPT parameters whose RFCs require xtext — ENVID=
// and ORCPT= (RFC 3461 §4.1, §4.2), AUTH= (RFC 4954 §5) — and for any
// parameter carried through MailOptions.Extra or RcptOptions.Extra whose
// value could contain a space, an '=', or any non-printable byte. Forgetting
// it is a routinely-made mistake that only shows up as a 501 from a strict
// server:
//
//	opts := &smtp.RcptOptions{Extra: []smtp.Param{{
//		Keyword: "ORCPT",
//		Value:   "rfc822;" + smtp.EncodeXtext("user@example.com"),
//	}}}
//
// Note that ORCPT's addr-type prefix and its ";" separator are *not* xtext —
// only the address after the ";" is encoded, as in the example above.
//
// This duplicates the encoder in internal/smtpwire rather than sharing it.
// It has to: package smtp imports nothing from this module (see
// TestNoModuleImports), which is what lets the client, a future smtpdeliver
// package and a future server framework all depend on this vocabulary
// without depending on each other. The two implementations are pinned to
// identical golden vectors in their respective tests, so a drift between
// them fails a test rather than producing a subtly different wire encoding.
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

// xtextHexDigits is upper case because RFC 3461 §4's hexchar production
// requires "two upper case hexadecimal digits"; a lower-case escape is not
// conformant xtext.
const xtextHexDigits = "0123456789ABCDEF"
