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
	Size     *int64
	Body     BodyType
	SMTPUTF8 bool
	_        struct{}
}

// DeliveryOptions configures sender-level delivery-control extensions.
//
// Callers constructing a DeliveryOptions literal must use keyed fields.
type DeliveryOptions struct {
	DSN           *DSNMailOptions
	DeliverBy     *DeliverByOptions
	FutureRelease *FutureReleaseOptions
	MTPriority    MTPriority
	RRVS          *RRVSOptions
	RequireTLS    bool
	_             struct{}
}

// RecipientDeliveryOptions configures recipient-level delivery-control
// extensions.
//
// Callers constructing a RecipientDeliveryOptions literal must use keyed fields.
type RecipientDeliveryOptions struct {
	DSN  *DSNRcptOptions
	RRVS *RRVSOptions
	_    struct{}
}

// DSNMailOptions configures DSN sender parameters.
// Callers constructing a DSNMailOptions literal must use keyed fields.
type DSNMailOptions struct {
	Return     DSNReturn
	EnvelopeID string
	_          struct{}
}

// DSNRcptOptions configures DSN recipient parameters.
// Callers constructing a DSNRcptOptions literal must use keyed fields.
type DSNRcptOptions struct {
	Notify       []DSNNotify
	OriginalType string
	Original     string
	_            struct{}
}

// DeliverByOptions configures the DELIVERBY BY= parameter.
// Callers constructing a DeliverByOptions literal must use keyed fields.
type DeliverByOptions struct {
	Seconds int64
	Mode    string
	_       struct{}
}

// FutureReleaseOptions configures mutually exclusive HOLDFOR and HOLDUNTIL.
// Callers constructing a FutureReleaseOptions literal must use keyed fields.
type FutureReleaseOptions struct {
	HoldForSeconds int64
	HoldUntil      string
	_              struct{}
}

// RRVSOptions configures RRVS=.
// Callers constructing a RRVSOptions literal must use keyed fields.
type RRVSOptions struct {
	Timestamp   string
	Disposition string
	_           struct{}
}

// LegacyOptions configures the implemented legacy MAIL parameters.
// Callers constructing a LegacyOptions literal must use keyed fields.
type LegacyOptions struct {
	Solicit   string
	TransitID string
	Submitter string
	ConPerm   string
	ConNeg    string
	_         struct{}
}
