package smtp

// Param is a single MAIL FROM or RCPT TO esmtp-param (RFC 5321 §4.1.2,
// "esmtp-param"): an esmtp-keyword and an optional esmtp-value.
//
// Param is the escape hatch required by docs/API-STABILITY.md §1b. Nearly
// every ESMTP extension RFC adds a MAIL/RCPT parameter — SIZE= (RFC 1870),
// BODY= (RFC 6152), RET=/ENVID=/NOTIFY=/ORCPT= (RFC 3461), AUTH= (RFC 4954),
// BY= (RFC 2852), MT-PRIORITY= (RFC 6710), RRVS= (RFC 7293), REQUIRETLS (RFC
// 8689), HOLDFOR=/HOLDUNTIL= (RFC 4865), SOLICIT= (RFC 3865), TRANSID= (RFC
// 3885), SMTPUTF8 (RFC 6531) — and a caller who needs a parameter this
// library has not modelled yet with a typed options field must still be able
// to send it. Options structs throughout smtpclient carry typed fields for
// what is modelled plus an Extra []Param field for everything else, from
// their first commit.
//
// Callers constructing a Param literal must use keyed fields: Param{}'s
// field set may grow, for example to carry parameters that use a delimiter
// other than "=" or that are inherently multi-valued.
type Param struct {
	// Keyword is the esmtp-keyword, e.g. "SIZE" or "REQUIRETLS".
	Keyword string
	// Value is the esmtp-value. Empty for a valueless parameter such as
	// REQUIRETLS.
	Value string

	_ struct{}
}

// BodyType is the value of the MAIL FROM BODY= parameter (RFC 6152 defines
// 8BITMIME; RFC 3030 adds BINARYMIME). It is string-backed rather than an
// enum, per docs/API-STABILITY.md §1b: BODY=BINARYMIME was registered after
// BODY=8BITMIME, so a Go switch that was exhaustive over the known values at
// the time it was written is exactly the kind of code this type must not
// force callers into. Values other than the constants below are valid and
// forwarded as-is.
type BodyType string

// Known BodyType values.
const (
	// BodyType7Bit is the RFC 5321 default and rarely sent explicitly.
	BodyType7Bit BodyType = "7BIT"
	// BodyType8BitMIME is BODY=8BITMIME (RFC 6152).
	BodyType8BitMIME BodyType = "8BITMIME"
	// BodyTypeBinaryMIME is BODY=BINARYMIME (RFC 3030); requires the
	// server to advertise CHUNKING and the message to be sent with BDAT
	// rather than DATA.
	BodyTypeBinaryMIME BodyType = "BINARYMIME"
)

// DSNNotify is a value of the RCPT TO NOTIFY= parameter (RFC 3461 §4.1). The
// wire form allows a comma-separated combination such as "FAILURE,DELAY",
// which is why this is string-backed rather than an enum: a caller composes
// the combination it wants rather than being restricted to one closed value
// per RCPT.
type DSNNotify string

// Known DSNNotify values (RFC 3461 §4.1). NEVER must not be combined with
// any other value; that is a caller contract, not something this type
// enforces.
const (
	DSNNotifyNever   DSNNotify = "NEVER"
	DSNNotifySuccess DSNNotify = "SUCCESS"
	DSNNotifyFailure DSNNotify = "FAILURE"
	DSNNotifyDelay   DSNNotify = "DELAY"
)

// DSNReturn is the value of the MAIL FROM RET= parameter (RFC 3461 §4.3):
// whether a delivery status notification should return the full message or
// only its headers.
type DSNReturn string

// Known DSNReturn values (RFC 3461 §4.3).
const (
	DSNReturnFull    DSNReturn = "FULL"
	DSNReturnHeaders DSNReturn = "HDRS"
)

// MTPriority is the value of the MAIL FROM MT-PRIORITY= parameter (RFC 6710
// §3.1): a signed decimal integer, kept string-backed like its siblings
// above rather than parsed into a Go int so that a value outside whatever
// range this library validates today still round-trips to the wire
// unchanged.
type MTPriority string

// ORcptAddressType is the address-type token of the RCPT TO ORCPT=
// parameter (RFC 3461 §4.4: "orcpt-parameter = 'ORCPT=' addr-type ';'
// xtext-addr"). The set is open by registry design: RFC 6533 added "utf-8"
// alongside the original "rfc822", and a Go switch written against only
// those two values is precisely what the next registration breaks — which
// is why this is string-backed rather than an enum.
type ORcptAddressType string

// Known ORcptAddressType values.
const (
	// ORcptAddressTypeRFC822 is "rfc822" (RFC 3461 §4.4).
	ORcptAddressTypeRFC822 ORcptAddressType = "rfc822"
	// ORcptAddressTypeUTF8 is "utf-8" (RFC 6533), used with SMTPUTF8 (RFC
	// 6531) addresses.
	ORcptAddressTypeUTF8 ORcptAddressType = "utf-8"
)
