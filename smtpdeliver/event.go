package smtpdeliver

import "net/netip"

// Event is one diagnostic lifecycle event passed to Options.Trace: a DNS
// lookup, a policy evaluation (RFC 8461, RFC 7672), a connection, a TLS
// handshake or an SMTP attempt (RFC 5321). Results remain the authoritative
// record; events may be dropped by the caller.
//
// Events are produced by the Deliverer. Callers constructing one, for example
// in tests, must use keyed fields.
type Event struct {
	// Kind says what happened.
	Kind EventKind
	// Domain is the destination's routing domain, if the event has one.
	Domain string
	// MX is the MX host name involved, if any (RFC 5321 §5.1).
	MX string
	// Address is the IP address and port involved, if any.
	Address netip.AddrPort
	// Cause is the failure the event reports, or nil.
	Cause error

	_ struct{}
}

// EventKind classifies an Event by delivery stage (RFC 5321 §3, RFC 8461,
// RFC 7672). It is an open string type; later releases may add kinds, and a
// Trace callback must ignore kinds it does not know.
type EventKind string

// Event kinds (RFC 1035, RFC 3207, RFC 5321, RFC 7672, RFC 8461).
const (
	// EventLookup is a completed DNS lookup (RFC 1035).
	EventLookup EventKind = "lookup"
	// EventPolicy is a completed policy evaluation (RFC 8461, RFC 7672).
	EventPolicy EventKind = "policy"
	// EventConnect is a completed or failed connection (RFC 5321 §3.1).
	EventConnect EventKind = "connect"
	// EventTLS is a completed or failed STARTTLS handshake (RFC 3207).
	EventTLS EventKind = "tls"
	// EventAttempt is a finished SMTP attempt (RFC 5321 §3).
	EventAttempt EventKind = "attempt"
)
