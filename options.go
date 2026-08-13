package smtp

// MailOptions carries the esmtp-params sent with a MAIL FROM command (RFC
// 5321 §4.1.1.2).
//
// A nil *MailOptions is always valid and means defaults, per
// docs/API-STABILITY.md §3. That is what allows a future extension RFC to add
// a field here without breaking callers.
//
// MailOptions is direction-neutral: it is what a client sends and what a
// server's parser produces from a received MAIL FROM. Every field must
// therefore be meaningful in both directions, which is why the send-side
// validation opt-out is smtpclient.MailSendOptions and not a field here — see
// docs/API-STABILITY.md §10.
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
	//
	// RFC 4954 §5 gives AUTH=<> — "the message is not authenticated" asserted
	// explicitly — a meaning distinct from omitting the parameter entirely.
	// The representation for it is Auth: "<>", which survives xtext encoding
	// unchanged because '<' and '>' are passthrough bytes. Both directions use
	// that representation: a receive-side parser must fill "<>" here rather
	// than collapsing it to the empty string, which would lose the
	// distinction.
	Auth string
	// AuthOriginal preserves the exact received RFC 4954 AUTH esmtp-param,
	// including keyword case and xtext spelling. It is populated by a server
	// parser alongside decoded Auth and is ignored when sending; callers that
	// forward the parameter verbatim may opt to use it explicitly.
	AuthOriginal *Param
	// Extra carries esmtp-params this library does not model with a typed
	// field. It is the escape hatch required by docs/API-STABILITY.md §1b:
	// a caller who needs a parameter that has not been implemented yet must
	// still be able to send it rather than abandon the library.
	//
	// Parameters are written in slice order, after any typed fields. When
	// sending, the client validates each keyword against the extensions the
	// server advertised before writing, unless the caller opts out through
	// smtpclient.MailSendOptions — a local error naming the missing extension
	// is a better diagnostic than a 501 from a strict server. When receiving,
	// Extra carries the parameters the parser did not recognise, preserved
	// verbatim per docs/API-STABILITY.md §1b.
	Extra []Param

	_ struct{}
}

// RcptOptions carries the esmtp-params sent with a RCPT TO command (RFC 5321
// §4.1.1.3).
//
// A nil *RcptOptions is always valid and means defaults, per
// docs/API-STABILITY.md §3. Like MailOptions it is direction-neutral — sent by
// a client, produced by a server's parser — so its send-side validation opt-out
// lives in smtpclient.RcptSendOptions instead of here.
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
	// field. See MailOptions.Extra; the send-side opt-out for this command is
	// smtpclient.RcptSendOptions.
	Extra []Param

	_ struct{}
}

// TransportOptions configures RFC 1870 SIZE, RFC 6152/RFC 3030 BODY, and RFC
// 6531 SMTPUTF8 MAIL parameters.
// Size is nil when SIZE is omitted; a non-nil zero explicitly declares an
// empty message.
// A nil *TransportOptions means defaults and omits these parameters.
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

// DeliveryOptions configures sender-level delivery-control extensions from RFC
// 3461, RFC 2852, RFC 4865, RFC 6710, RFC 7293, and RFC 8689.
// A nil *DeliveryOptions means defaults and leaves these extensions unused.
//
// There is deliberately no RRVS field here: RFC 7293 defines RRVS= on RCPT TO,
// so a sender-level field could only ever produce an error when sent and could
// never be filled by a receive-side parser. Use RecipientDeliveryOptions.RRVS.
// It existed as a source-compatibility shim until T16 removed it —
// docs/API-STABILITY.md §10.
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
// extensions from RFC 3461 and RFC 7293.
// A nil *RecipientDeliveryOptions means defaults and omits these parameters.
//
// Callers constructing a RecipientDeliveryOptions literal must use keyed fields.
type RecipientDeliveryOptions struct {
	// DSN configures RFC 3461 RCPT TO parameters.
	DSN *DSNRcptOptions
	// RRVS configures the RFC 7293 RRVS= parameter.
	RRVS *RRVSOptions
	_    struct{}
}

// DSNMailOptions configures RFC 3461 DSN sender parameters.
// A nil *DSNMailOptions means defaults and omits DSN sender parameters.
// Callers constructing a DSNMailOptions literal must use keyed fields.
type DSNMailOptions struct {
	// Return is the open RFC 3461 RET= value; empty omits it.
	Return DSNReturn
	// EnvelopeID is xtext-encoded as the RFC 3461 ENVID= value.
	EnvelopeID string
	_          struct{}
}

// DSNRcptOptions configures RFC 3461 DSN recipient parameters.
// A nil *DSNRcptOptions means defaults and omits DSN recipient parameters.
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

// DeliverByOptions configures the RFC 2852 DELIVERBY BY= parameter.
// A nil *DeliverByOptions means defaults and omits BY=.
// Callers constructing a DeliverByOptions literal must use keyed fields.
type DeliverByOptions struct {
	// Seconds is BY's relative deadline. It may be zero or negative only
	// when Mode is "N"; values are limited to nine decimal digits.
	Seconds int64
	// Mode is the required RFC 2852 by-mode: "N" or "R".
	Mode string
	_    struct{}
}

// FutureReleaseOptions configures RFC 4865 mutually exclusive HOLDFOR and
// HOLDUNTIL parameters.
// A nil *FutureReleaseOptions means defaults and omits both parameters.
// Callers constructing a FutureReleaseOptions literal must use keyed fields.
type FutureReleaseOptions struct {
	// HoldForSeconds is the RFC 4865 HOLDFOR= delay; zero omits it.
	HoldForSeconds int64
	// HoldUntil is the RFC 4865 HOLDUNTIL= timestamp; empty omits it.
	HoldUntil string
	_         struct{}
}

// RRVSOptions configures RFC 7293 RRVS=.
// A nil *RRVSOptions means defaults and omits RRVS=.
// Callers constructing a RRVSOptions literal must use keyed fields.
type RRVSOptions struct {
	// Timestamp is the RFC 7293 recipient-valid-since time.
	Timestamp string
	// Disposition is the optional RFC 7293 "C" or "R" suffix.
	Disposition string
	_           struct{}
}

// LegacyOptions configures implemented RFC 3865, RFC 3885, RFC 4405, and RFC
// 4141 legacy MAIL parameters.
// A nil *LegacyOptions means defaults and omits these parameters.
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

// RecipientLegacyOptions configures implemented RFC 4141 legacy RCPT
// parameters.
// A nil *RecipientLegacyOptions means defaults and omits these parameters.
// Callers constructing a RecipientLegacyOptions literal must use keyed fields.
type RecipientLegacyOptions struct {
	// ConNeg requests RFC 4141 CONNEG on RCPT TO.
	ConNeg bool
	_      struct{}
}
