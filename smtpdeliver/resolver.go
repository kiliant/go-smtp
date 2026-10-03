package smtpdeliver

import (
	"context"
	"net/netip"
)

// Resolver supplies the DNS lookups delivery needs: MX (RFC 5321 §5.1),
// addresses, TXT (RFC 8461 §3.1) and TLSA (RFC 6698, RFC 7672). It is a
// struct of function fields rather than an interface, so a lookup added later
// is a new field rather than a breaking change, and no DNS library type
// crosses this package's boundary.
//
// A lookup returns a nil error with State LookupNotFound for NXDOMAIN and
// State LookupFound with no records for an empty answer (NODATA). A non-nil
// error means a temporary failure, such as SERVFAIL or a timeout. A resolver
// that cannot tell NXDOMAIN from NODATA must say so in its documentation; the
// standard-library fallback reports both as not found for addresses and TXT,
// and as an empty answer for MX (see Options.Resolver).
//
// Callers constructing a Resolver literal must use keyed fields.
type Resolver struct {
	// LookupMX resolves MX records (RFC 5321 §5.1). nil selects the standard
	// library.
	LookupMX func(ctx context.Context, req *LookupMXRequest) (MXLookup, error)
	// LookupIP resolves A and AAAA records (RFC 1035, RFC 3596). nil selects
	// the standard library.
	LookupIP func(ctx context.Context, req *LookupIPRequest) (IPLookup, error)
	// LookupTXT resolves TXT records, used for RFC 8461 §3.1 discovery. nil
	// selects the standard library.
	LookupTXT func(ctx context.Context, req *LookupTXTRequest) (TXTLookup, error)
	// LookupTLSA resolves RFC 6698 TLSA records. There is no standard-library
	// fallback; RFC 7672 DANE requires it.
	LookupTLSA func(ctx context.Context, req *LookupTLSARequest) (TLSALookup, error)

	_ struct{}
}

// LookupMXRequest asks for the MX records of one name (RFC 5321 §5.1).
//
// Callers constructing a LookupMXRequest literal, for example in tests, must
// use keyed fields.
type LookupMXRequest struct {
	// Name is the absolute, lower-case name to query, without a trailing dot.
	Name string

	_ struct{}
}

// LookupIPRequest asks for the A and AAAA records of one name (RFC 1035,
// RFC 3596).
//
// Callers constructing a LookupIPRequest literal, for example in tests, must
// use keyed fields.
type LookupIPRequest struct {
	// Name is the absolute, lower-case name to query, without a trailing dot.
	Name string

	_ struct{}
}

// LookupTXTRequest asks for the TXT records of one name (RFC 1035 §3.3.14).
//
// Callers constructing a LookupTXTRequest literal, for example in tests, must
// use keyed fields.
type LookupTXTRequest struct {
	// Name is the absolute, lower-case name to query, without a trailing dot.
	Name string

	_ struct{}
}

// LookupTLSARequest asks for the TLSA records of one name (RFC 6698 §3), such
// as "_25._tcp.mx.example.com".
//
// Callers constructing a LookupTLSARequest literal, for example in tests, must
// use keyed fields.
type LookupTLSARequest struct {
	// Name is the absolute, lower-case name to query, without a trailing dot.
	Name string

	_ struct{}
}

// MXLookup is the answer to an MX query (RFC 5321 §5.1).
//
// Callers constructing an MXLookup literal must use keyed fields.
type MXLookup struct {
	// State distinguishes NXDOMAIN from an answer (RFC 2308).
	State LookupState
	// Records are the MX records, in any order.
	Records []MX
	// CanonicalName is the name after CNAME and DNAME processing (RFC 6672),
	// without a trailing dot. Empty means the resolver did not report it.
	CanonicalName string
	// Security is the RFC 4035 validation state of the whole alias chain and
	// the final RRset.
	Security DNSSECStatus

	_ struct{}
}

// MX is one MX record (RFC 1035 §3.3.9).
//
// Callers constructing an MX literal must use keyed fields.
type MX struct {
	// Host is the exchange host name, without a trailing dot. "." with
	// Preference 0 as the only record is RFC 7505 null MX; return it as "."
	// or as the empty string.
	Host string
	// Preference orders exchanges; lower is preferred (RFC 5321 §5.1).
	Preference uint16

	_ struct{}
}

// IPLookup is the answer to an address query (RFC 1035, RFC 3596).
//
// Callers constructing an IPLookup literal must use keyed fields.
type IPLookup struct {
	// State distinguishes NXDOMAIN from an answer (RFC 2308).
	State LookupState
	// Addresses are the A and AAAA results in the resolver's preferred order,
	// which RFC 5321 §5.1 has the client keep.
	Addresses []netip.Addr
	// CanonicalName is the name after CNAME and DNAME processing (RFC 6672),
	// without a trailing dot. Empty means the resolver did not report it.
	CanonicalName string
	// Security is the RFC 4035 validation state of the whole alias chain and
	// the final RRsets.
	Security DNSSECStatus

	_ struct{}
}

// TXTLookup is the answer to a TXT query (RFC 1035 §3.3.14).
//
// Callers constructing a TXTLookup literal must use keyed fields.
type TXTLookup struct {
	// State distinguishes NXDOMAIN from an answer (RFC 2308).
	State LookupState
	// Records holds one string per TXT record, its character-strings
	// concatenated (RFC 8461 §3.1).
	Records []string
	// CanonicalName is the name after CNAME and DNAME processing (RFC 6672),
	// without a trailing dot. Empty means the resolver did not report it.
	CanonicalName string
	// Security is the RFC 4035 validation state of the whole alias chain and
	// the final RRset.
	Security DNSSECStatus

	_ struct{}
}

// TLSALookup is the answer to a TLSA query (RFC 6698 §2).
//
// Callers constructing a TLSALookup literal must use keyed fields.
type TLSALookup struct {
	// State distinguishes NXDOMAIN from an answer (RFC 2308).
	State LookupState
	// Records are the TLSA records.
	Records []TLSA
	// CanonicalName is the name after CNAME and DNAME processing (RFC 6672),
	// without a trailing dot. Empty means the resolver did not report it.
	CanonicalName string
	// Security is the RFC 4035 validation state of the whole alias chain and
	// the final RRset.
	Security DNSSECStatus

	_ struct{}
}

// TLSA is one RFC 6698 §2.1 TLSA record.
//
// Callers constructing a TLSA literal must use keyed fields.
type TLSA struct {
	// Usage is the certificate usage field (RFC 6698 §2.1.1). RFC 7672 §3.1
	// uses DANE-TA(2) and DANE-EE(3).
	Usage uint8
	// Selector is the selector field (RFC 6698 §2.1.2).
	Selector uint8
	// MatchingType is the matching type field (RFC 6698 §2.1.3).
	MatchingType uint8
	// Association is the certificate association data (RFC 6698 §2.1.4). It
	// is copied before use.
	Association []byte

	_ struct{}
}

// LookupState says whether a queried name exists (RFC 2308). It is an open
// string type; an unknown value is treated as a temporary lookup failure.
type LookupState string

// RFC 2308 lookup states.
const (
	// LookupFound means the name exists; the records may still be empty
	// (NODATA).
	LookupFound LookupState = "found"
	// LookupNotFound means the name does not exist (NXDOMAIN).
	LookupNotFound LookupState = "not-found"
)

// DNSSECStatus is the RFC 4035 §4.3 validation state of a lookup, as reported
// by a validating resolver. It is an open string type; an unknown value is
// retained in results and treated as DNSSECUnvalidated for enforcement.
type DNSSECStatus string

// RFC 4035 §4.3 validation states, plus DNSSECUnvalidated for a resolver that
// does not validate.
const (
	// DNSSECSecure means the answer validated.
	DNSSECSecure DNSSECStatus = "secure"
	// DNSSECInsecure means the answer is provably unsigned. RFC 7672 §2.1.1
	// treats it as a definitive non-DANE answer.
	DNSSECInsecure DNSSECStatus = "insecure"
	// DNSSECBogus means validation failed. RFC 7672 §2.1.1 treats it as a
	// lookup failure.
	DNSSECBogus DNSSECStatus = "bogus"
	// DNSSECIndeterminate means validation could not be completed. RFC 7672
	// §2.1.1 treats it as a lookup failure.
	DNSSECIndeterminate DNSSECStatus = "indeterminate"
	// DNSSECUnvalidated means the resolver did not validate, as for the
	// standard-library fallback. It can never enable RFC 7672 DANE.
	DNSSECUnvalidated DNSSECStatus = "unvalidated"
)
