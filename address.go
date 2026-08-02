package smtp

// RFC 5321 §4.5.3.1 declares these as sizes an implementation MUST be able
// to accept for the reverse-path and forward-path grammar ("Path"): local
// part 64 octets, domain 255 octets, whole path 256 octets. They are
// minimums a receiving server has to support, not maximums this client
// enforces against a peer's data — a server or gateway that accepts a
// longer value is still RFC 5321 conformant, so rejecting its output
// locally would be a client-side interop bug, not a validation feature.
//
// SMTPUTF8 (RFC 6531) permits UTF-8 octets within these limits. Nothing in
// this package assumes an address is US-ASCII: reverse-path and
// forward-path values are plain Go strings, which are UTF-8 safe by
// construction, and no function here rejects non-ASCII bytes.
const (
	// MinLocalPartLength is the local-part size a server MUST accept
	// (RFC 5321 §4.5.3.1.1).
	MinLocalPartLength = 64
	// MinDomainLength is the domain size a server MUST accept (RFC 5321
	// §4.5.3.1.2).
	MinDomainLength = 255
	// MinPathLength is the reverse-path/forward-path size a server MUST
	// accept (RFC 5321 §4.5.3.1.3).
	MinPathLength = 256
)
