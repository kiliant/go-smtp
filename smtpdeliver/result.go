package smtpdeliver

import (
	"net/netip"
	"time"

	smtp "github.com/kiliant/go-smtp"
)

// Result is the outcome of one Deliver call: one DestinationResult per
// requested destination, in request order (RFC 5321 §3.3).
//
// Results are produced by Deliver. Callers constructing one, for example in
// tests, must use keyed fields.
type Result struct {
	// Destinations holds one entry per Request.Destinations entry, in order.
	Destinations []DestinationResult

	_ struct{}
}

// DestinationResult is the outcome for one routing domain (RFC 5321 §5).
//
// Results are produced by Deliver. Callers constructing one, for example in
// tests, must use keyed fields.
type DestinationResult struct {
	// Domain is the normalised routing domain: lower case, no trailing dot.
	Domain string
	// Policy is the RFC 8461 MTA-STS evaluation for the domain. Its Kind is
	// empty when MTA-STS is disabled.
	Policy PolicyResult
	// Attempts are the MX and address attempts in the order they were made
	// (RFC 5321 §5.1). They explain the outcomes; they are not outcomes.
	Attempts []AttemptResult
	// Recipients holds exactly one outcome per requested recipient, in
	// request order, duplicates included.
	Recipients []RecipientOutcome

	_ struct{}
}

// RecipientOutcome is the final outcome for one requested recipient
// (RFC 5321 §4.1.1.3).
//
// Outcomes are produced by Deliver. Callers constructing one, for example in
// tests, must use keyed fields.
type RecipientOutcome struct {
	// Address is the requested forward-path, as given.
	Address string
	// Disposition says what happened and whether a later attempt is safe.
	Disposition Disposition
	// Reply is the RFC 5321 reply that decided the outcome, if one did.
	Reply *smtp.Error
	// Cause is the non-reply failure that decided the outcome, such as a DNS,
	// policy, TLS or transport error, with its original chain for errors.Is
	// and errors.As.
	Cause error
	// Attempt indexes DestinationResult.Attempts for the attempt that made
	// the outcome final, or is -1 when no attempt did, such as for a routing
	// failure before any connection (RFC 5321 §5.1).
	Attempt int

	_ struct{}
}

// AttemptResult records one connection attempt to one address of one MX host
// (RFC 5321 §5.1).
//
// Results are produced by Deliver. Callers constructing one, for example in
// tests, must use keyed fields.
type AttemptResult struct {
	// MX is the MX host name the address was selected for.
	MX string
	// Preference is the MX preference (RFC 5321 §5.1); 0 for an implicit MX.
	Preference uint16
	// Address is the IP address dialled.
	Address netip.Addr
	// Stage is the furthest stage the attempt reached.
	Stage AttemptStage
	// Policies are the transport policies (RFC 8461, RFC 7672) applied to the
	// attempt and how each fared.
	Policies []PolicyResult
	// Cause is why the attempt ended early, or nil if it completed its
	// transaction.
	Cause error

	_ struct{}
}

// PolicyResult describes one transport policy evaluation: RFC 8461 MTA-STS or
// RFC 7672 DANE.
//
// Results are produced by Deliver and RefreshPolicy. Callers constructing one,
// for example in tests, must use keyed fields.
type PolicyResult struct {
	// Kind is the policy mechanism. Empty means no policy was evaluated.
	Kind PolicyKind
	// Mode is the policy's mode within its mechanism, such as an MTASTSMode
	// or DANEMode value, as a string.
	Mode string
	// Source is where the policy came from.
	Source PolicySource
	// Applied reports whether the policy constrained the attempt. A testing
	// or expired policy, or none at all, is not applied.
	Applied bool
	// ValidUntil is when the policy expires (RFC 8461 §5.1); zero if not
	// applicable.
	ValidUntil time.Time
	// Cause records a policy failure, such as a failed refresh or a failed
	// certificate match, with its original error chain.
	Cause error

	_ struct{}
}

// Disposition is a recipient's final state after one Deliver call, in terms a
// caller's queue can act on (RFC 5321 §4.2.5, §6.1). It is an open string type:
// a caller must treat an unknown value as DispositionIndeterminate.
type Disposition string

// Recipient dispositions (RFC 5321 §4.2.1, §4.2.5).
const (
	// DispositionDelivered means an authoritative 2yz final reply transferred
	// responsibility for the message (RFC 5321 §4.2.5).
	DispositionDelivered Disposition = "delivered"
	// DispositionTemporary means no delivery occurred and an unchanged retry
	// later is safe and may succeed (RFC 5321 §4.2.1 4yz class).
	DispositionTemporary Disposition = "temporary-failure"
	// DispositionPermanent means no delivery occurred and an unchanged retry
	// should not be automatic (RFC 5321 §4.2.1 5yz class).
	DispositionPermanent Disposition = "permanent-failure"
	// DispositionIndeterminate means the server may have accepted the message
	// (RFC 5321 §4.2.5); an automatic retry risks a duplicate.
	DispositionIndeterminate Disposition = "indeterminate"
	// DispositionNotAttempted means the call ended before this recipient
	// reached an RFC 5321 protocol decision, normally because the context or
	// the message source failed.
	DispositionNotAttempted Disposition = "not-attempted"
)

// AttemptStage is the furthest stage an attempt reached (RFC 5321 §3). It is
// an open string type; later releases may add stages.
type AttemptStage string

// Attempt stages, in RFC 5321 §3 protocol order.
const (
	// StageConnect is establishing the TCP connection (RFC 5321 §3.1).
	StageConnect AttemptStage = "connect"
	// StageGreeting is reading the 220 greeting (RFC 5321 §3.1).
	StageGreeting AttemptStage = "greeting"
	// StageHello is EHLO negotiation (RFC 5321 §4.1.1.1).
	StageHello AttemptStage = "hello"
	// StageTLS is STARTTLS and certificate validation (RFC 3207).
	StageTLS AttemptStage = "tls"
	// StageMail is MAIL FROM (RFC 5321 §4.1.1.2).
	StageMail AttemptStage = "mail"
	// StageRcpt is RCPT TO (RFC 5321 §4.1.1.3).
	StageRcpt AttemptStage = "rcpt"
	// StageContent is transferring and completing the content (RFC 5321
	// §4.1.1.4).
	StageContent AttemptStage = "content"
	// StageComplete means the transaction finished, whatever its replies
	// (RFC 5321 §3.3).
	StageComplete AttemptStage = "complete"
)

// PolicyKind names a transport policy mechanism, such as RFC 8461 MTA-STS or
// RFC 7672 DANE. It is an open string type; later releases may add
// mechanisms.
type PolicyKind string

// Transport policy mechanisms (RFC 8461, RFC 7672).
const (
	// PolicyMTASTS is RFC 8461 MTA-STS.
	PolicyMTASTS PolicyKind = "mta-sts"
	// PolicyDANE is RFC 7672 DANE for SMTP.
	PolicyDANE PolicyKind = "dane"
)

// PolicySource says where a policy (RFC 8461, RFC 7672) came from. It is an
// open string type.
type PolicySource string

// Policy sources (RFC 8461 §3.3, RFC 7672).
const (
	// PolicySourceCache is an unexpired policy loaded from the PolicyCache
	// (RFC 8461 §5.1).
	PolicySourceCache PolicySource = "cache"
	// PolicySourceFetched is a policy fetched over HTTPS during this call
	// (RFC 8461 §3.3).
	PolicySourceFetched PolicySource = "fetched"
	// PolicySourceDNS is a policy published directly in DNS, such as RFC 7672
	// TLSA records.
	PolicySourceDNS PolicySource = "dns"
)
