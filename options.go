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
	// Transport configures the transport-core ESMTP parameters (T08). A nil
	// value leaves transport extensions unused.
	Transport *TransportOptions
	// Delivery configures delivery-control ESMTP parameters (T09). A nil value
	// leaves delivery-control extensions unused.
	Delivery *DeliveryOptions
	// Legacy configures the implemented legacy and niche parameters (T10). A
	// nil value leaves those extensions unused.
	Legacy *LegacyOptions
	// Auth is the authenticated identity of the original submitter, sent as
	// the AUTH= MAIL parameter defined by RFC 4954 §5. It is separate from
	// the AUTH command. The client xtext-encodes this value before sending it.
	// An empty value omits the parameter.
	Auth string
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
	// AllowUnadvertisedParameters permits Extra parameters, and the AUTH=
	// parameter when set, even when the server did not advertise their
	// extension keyword. It defaults to false so the client reports a local,
	// actionable validation error before writing a command that a strict
	// server would reject. Enable it only when the caller has independent
	// knowledge that the peer accepts the parameter.
	AllowUnadvertisedParameters bool

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
	// Delivery configures recipient-specific delivery-control parameters (T09).
	// A nil value leaves them unused.
	Delivery *RecipientDeliveryOptions
	// Legacy configures recipient-scoped legacy extension parameters (T10).
	// A nil value leaves them unused.
	Legacy *RecipientLegacyOptions
	// Extra carries esmtp-params this library does not model with a typed
	// field. See MailOptions.Extra.
	Extra []Param
	// AllowUnadvertisedParameters permits Extra parameters even when the
	// server did not advertise their extension keyword. It has the same
	// default and purpose as MailOptions.AllowUnadvertisedParameters.
	AllowUnadvertisedParameters bool

	_ struct{}
}

// TransportOptions configures SIZE, BODY, and SMTPUTF8 MAIL parameters.
// Size is nil when SIZE is omitted; a non-nil zero explicitly declares an
// empty message.
//
// Callers constructing a TransportOptions literal must use keyed fields.
type TransportOptions struct {
	// Size is the RFC 1870 SIZE= declaration. Nil omits it.
	Size *int64
	// Body is the RFC 6152/RFC 3030 BODY= value. Empty omits it.
	Body BodyType
	// SMTPUTF8 requests the RFC 6531 SMTPUTF8 MAIL parameter.
	SMTPUTF8 bool
	_        struct{}
}

// DeliveryOptions configures sender-level delivery-control extensions.
//
// Callers constructing a DeliveryOptions literal must use keyed fields.
type DeliveryOptions struct {
	// DSN configures RFC 3461 MAIL FROM parameters.
	DSN *DSNMailOptions
	// DeliverBy configures the RFC 2852 BY= parameter.
	DeliverBy *DeliverByOptions
	// FutureRelease configures RFC 4865 hold parameters.
	FutureRelease *FutureReleaseOptions
	// MTPriority is the RFC 6710 MT-PRIORITY= value; zero omits it.
	MTPriority MTPriority
	// RRVS is invalid here: RFC 7293 defines RRVS= on RCPT TO. It is
	// retained for source compatibility; Client.Mail returns an actionable
	// error when it is set. Use RecipientDeliveryOptions.RRVS instead.
	RRVS *RRVSOptions
	// RequireTLS requests RFC 8689 REQUIRETLS. It asks every hop from here to
	// final delivery — not merely the connection to the immediate peer — to
	// relay the message only over TLS, tagged with REQUIRETLS in turn, or
	// else bounce it: the IANA registration text for the extension (RFC 8689
	// §7) describes its behavior as causing the message "to require the use
	// of TLS and tagging with REQUIRETLS for all onward relay." Because that
	// promise is meaningless if this client's own session is not itself
	// TLS-protected, RFC 8689 §2 makes that a precondition on the sender
	// too: "This option MUST only be specified in the context of an SMTP
	// session meeting the security requirements of REQUIRETLS: ... The
	// session itself MUST employ TLS transmission." smtpclient enforces that
	// one precondition locally and rejects RequireTLS when the session was
	// not negotiated over STARTTLS or ClientOptions.ImplicitTLS; the RFC's
	// remaining §2 preconditions (DNSSEC/MTA-STS validation of the next
	// hop's MX record, certificate trust) describe an onward relay's own
	// outbound leg and are out of this client's scope, per
	// docs/ARCHITECTURE.md's deferral of MX/transport-policy decisions to
	// the post-v1 smtpdeliver layer.
	//
	// RFC 8689 has no coupling to the DSN NOTIFY= parameter — the RFC never
	// mentions NOTIFY. Its one DSN-related rule (§5) instead binds a server
	// that later generates a non-delivery report for a REQUIRETLS message:
	// that server must disregard a RET=FULL request in favor of RET=HDRS,
	// and, unless redacted, must itself tag the resulting bounce with
	// REQUIRETLS. Both are the receiving/relaying server's obligations, not
	// something the sending client validates.
	RequireTLS bool
	_          struct{}
}

// RecipientDeliveryOptions configures recipient-level delivery-control
// extensions.
//
// Callers constructing a RecipientDeliveryOptions literal must use keyed fields.
type RecipientDeliveryOptions struct {
	// DSN configures RFC 3461 RCPT TO parameters.
	DSN *DSNRcptOptions
	// RRVS configures the RFC 7293 RRVS= parameter.
	RRVS *RRVSOptions
	_    struct{}
}

// DSNMailOptions configures DSN sender parameters.
// Callers constructing a DSNMailOptions literal must use keyed fields.
type DSNMailOptions struct {
	// Return is the open RFC 3461 RET= value; empty omits it.
	Return DSNReturn
	// EnvelopeID is xtext-encoded as the RFC 3461 ENVID= value.
	EnvelopeID string
	_          struct{}
}

// DSNRcptOptions configures DSN recipient parameters.
// Callers constructing a DSNRcptOptions literal must use keyed fields.
type DSNRcptOptions struct {
	// Notify supplies open RFC 3461 NOTIFY= tokens. NEVER must be alone.
	Notify []DSNNotify
	// OriginalType is the ORCPT address type, such as "rfc822" or "utf-8".
	OriginalType string
	// Original is xtext-encoded as the ORCPT address value.
	Original string
	_        struct{}
}

// DeliverByOptions configures the DELIVERBY BY= parameter.
// Callers constructing a DeliverByOptions literal must use keyed fields.
type DeliverByOptions struct {
	// Seconds is BY's relative deadline. It may be zero or negative only
	// when Mode is "N"; values are limited to nine decimal digits.
	Seconds int64
	// Mode is the required RFC 2852 by-mode: "N" or "R".
	Mode string
	_    struct{}
}

// FutureReleaseOptions configures mutually exclusive HOLDFOR and HOLDUNTIL.
// Callers constructing a FutureReleaseOptions literal must use keyed fields.
type FutureReleaseOptions struct {
	// HoldForSeconds is the RFC 4865 HOLDFOR= delay; zero omits it.
	HoldForSeconds int64
	// HoldUntil is the RFC 4865 HOLDUNTIL= timestamp; empty omits it.
	HoldUntil string
	_         struct{}
}

// RRVSOptions configures RRVS=.
// Callers constructing a RRVSOptions literal must use keyed fields.
type RRVSOptions struct {
	// Timestamp is the RFC 7293 recipient-valid-since time.
	Timestamp string
	// Disposition is the optional RFC 7293 "C" or "R" suffix.
	Disposition string
	_           struct{}
}

// LegacyOptions configures the implemented legacy MAIL parameters.
// Callers constructing a LegacyOptions literal must use keyed fields.
type LegacyOptions struct {
	// Solicit is the RFC 3865 SOLICIT= value.
	Solicit string
	// TransitID is the RFC 3885 TRANSID= value.
	TransitID string
	// Submitter is the RFC 4405 SUBMITTER= value.
	Submitter string
	// ConPerm requests RFC 4141 CONPERM.
	ConPerm bool
	_       struct{}
}

// RecipientLegacyOptions configures implemented legacy RCPT parameters.
// Callers constructing a RecipientLegacyOptions literal must use keyed fields.
type RecipientLegacyOptions struct {
	// ConNeg requests RFC 4141 CONNEG on RCPT TO.
	ConNeg bool
	_      struct{}
}
