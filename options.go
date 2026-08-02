package smtp

// MailOptions carries the esmtp-params sent with a MAIL FROM command (RFC
// 5321 §4.1.1.2).
//
// A nil *MailOptions is always valid and means defaults, per
// docs/API-STABILITY.md §3. That is what allows a future extension RFC to add
// a field here without breaking callers.
//
// The struct is deliberately near-empty today. Typed fields for the modelled
// parameters are added by the extension tasks — SIZE= and BODY= (T08), the DSN
// parameters RET= and ENVID=, plus BY=, HOLDFOR=/HOLDUNTIL=, MT-PRIORITY=,
// RRVS= and REQUIRETLS (T09) — and each of those is an additive change.
// Shipping the struct before the command that takes it is rule 3 operating as
// intended: a command entry point that ships without an options parameter
// cannot gain one without breaking every call site.
//
// Callers constructing a MailOptions literal must use keyed fields.
type MailOptions struct {
	// Extra carries esmtp-params this library does not model with a typed
	// field. It is the escape hatch required by docs/API-STABILITY.md §1b:
	// a caller who needs a parameter that has not been implemented yet must
	// still be able to send it rather than abandon the library.
	//
	// Parameters are written in slice order, after any typed fields. The
	// client validates each keyword against the extensions the server
	// advertised before writing, unless the caller opts out — a local error
	// naming the missing extension is a better diagnostic than a 501 from a
	// strict server.
	Extra []Param

	_ struct{}
}

// RcptOptions carries the esmtp-params sent with a RCPT TO command (RFC 5321
// §4.1.1.3).
//
// A nil *RcptOptions is always valid and means defaults, per
// docs/API-STABILITY.md §3.
//
// As with MailOptions, the struct is near-empty today: the DSN parameters
// NOTIFY= and ORCPT= (RFC 3461) are added by T09, additively.
//
// Callers constructing a RcptOptions literal must use keyed fields.
type RcptOptions struct {
	// Extra carries esmtp-params this library does not model with a typed
	// field. See MailOptions.Extra.
	Extra []Param

	_ struct{}
}
