// Package smtpdeliver makes one bounded delivery attempt for a message: it
// decides which SMTP endpoint to contact for each destination domain, applies
// the transport policy that domain publishes, and reports an exact outcome for
// every recipient.
//
// It implements RFC 5321 §5 MX resolution and multi-address attempts, RFC
// 7505 null MX, MTA-STS (RFC 8461) and, optionally, DANE for SMTP (RFC 7672)
// through a caller-supplied DNSSEC-aware resolver. SMTP itself is spoken by
// package smtpclient over a connection this package dials and owns.
//
// This is a delivery attempt library, not a mail transfer agent. It has no
// message queue, no retry schedule, no bounce generation and no background
// goroutines. A Result says, per recipient, whether a later attempt is safe and
// potentially useful (see Disposition); deciding when that attempt happens,
// and when to give up, belongs to the caller.
//
// The package validates no DNSSEC itself. A caller that wants DANE supplies a
// Resolver whose lookups report a validated DNSSECStatus.
package smtpdeliver
