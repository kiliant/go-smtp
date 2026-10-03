package smtpdeliver

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// Options configures a Deliverer: its DNS resolver, the RFC 8461 MTA-STS and
// RFC 7672 DANE policies it applies, how it dials RFC 5321 SMTP servers, and
// the local identities it uses for loop elimination. A nil *Options means
// defaults, which require a local identity (see LocalNames); New rejects
// defaults that cannot deliver safely rather than guessing.
//
// Callers constructing an Options literal must use keyed fields.
type Options struct {
	// Resolver supplies DNS lookups. A nil field falls back to the standard
	// library for MX, IP and TXT lookups, reporting DNSSECUnvalidated; there
	// is no standard-library TLSA lookup (RFC 6698). The standard library
	// cannot tell NXDOMAIN from an empty answer: the fallback reports both as
	// an empty MX answer, so the RFC 5321 §5.1 implicit MX applies and a
	// nonexistent domain then fails at its address lookup, and as not found
	// for addresses and TXT.
	Resolver Resolver
	// MTASTS enables RFC 8461 MTA-STS. nil disables it; there is no default
	// policy cache, because a cache that forgets enforce policies on restart
	// reopens the downgrade window MTA-STS closes.
	MTASTS *MTASTSOptions
	// DANE enables RFC 7672 DANE for SMTP. nil disables it. Enabling it
	// requires Resolver.LookupMX, LookupIP and LookupTLSA from one
	// DNSSEC-aware view.
	DANE *DANEOptions
	// Dial establishes the TCP connection for one attempt to the RFC 5321
	// SMTP port of a selected address. Its ctx carries the Timeouts.Connect
	// deadline. nil selects a net.Dialer.
	Dial func(ctx context.Context, req *DialRequest) (net.Conn, error)
	// HTTPClient fetches RFC 8461 policies. nil selects a client using the
	// system roots. It is copied and its Transport treated as immutable. This
	// package refuses redirects, enforces the policy size and time limits, and
	// sends "Cache-Control: no-cache" whatever the client's own settings; a
	// caching Transport that ignores that header is the caller's
	// responsibility.
	HTTPClient *http.Client
	// TLSConfig supplies additional STARTTLS (RFC 3207) configuration. It is
	// cloned per attempt. Without an applicable policy the default is
	// unauthenticated opportunistic TLS (RFC 7435); MTA-STS and DANE add
	// authentication. The configuration may strengthen these defaults but
	// cannot disable an applied MTA-STS or DANE requirement.
	TLSConfig *tls.Config
	// Identity is the client name sent in EHLO (RFC 5321 §4.1.1.1). Empty
	// selects smtpclient's default.
	Identity string
	// LocalNames are the host names by which this sender is known, used for
	// RFC 5321 §5.1 loop elimination. New requires at least one entry in
	// LocalNames or LocalAddresses unless DisableLoopElimination is set.
	LocalNames []string
	// LocalAddresses are this sender's own addresses, used for RFC 5321 §5.1
	// loop elimination alongside LocalNames.
	LocalAddresses []netip.Addr
	// DisableLoopElimination turns off RFC 5321 §5.1 loop elimination. It is
	// meant for tests and explicitly routed private deployments; Internet MX
	// delivery should declare its local identities instead.
	DisableLoopElimination bool
	// MaxAddresses caps the addresses tried for one destination across all its
	// MX hosts. Zero means no cap: RFC 5321 §5.1 has a client try every
	// relevant address. A cap of one forgoes failover and is rejected unless
	// AllowSingleAddress is set.
	MaxAddresses int
	// AllowSingleAddress accepts MaxAddresses == 1, explicitly choosing the
	// RFC 5321 §5.1 trade-off of a single attempt per destination.
	AllowSingleAddress bool
	// MaxDestinations caps the destinations in one Request, protecting against
	// accidental unbounded work. It is a local limit, not an RFC 5321 one.
	// Zero selects 100.
	MaxDestinations int
	// Timeouts bounds the DNS, connect and policy-fetch stages. SMTP command
	// stages use smtpclient's RFC 5321 §4.5.3.2 defaults.
	Timeouts Timeouts
	// Trace, when non-nil, receives lifecycle events for DNS lookups, policy
	// evaluation, connections, TLS and SMTP attempts (RFC 5321). It is called
	// synchronously, must not block, and must not call back into the same
	// Deliverer. It never receives message content, credentials, policy bodies
	// or private keys.
	Trace func(Event)

	_ struct{}
}

// Timeouts bounds delivery stages. Today it covers the stages this package
// owns; SMTP command stages follow smtpclient's RFC 5321 §4.5.3.2 defaults, and
// a field added later may override one of them. A zero field selects its
// documented default; the caller's context always bounds every stage as well.
//
// Callers constructing a Timeouts literal must use keyed fields.
type Timeouts struct {
	// DNS bounds each DNS lookup (RFC 1035). Zero selects 30 seconds.
	DNS time.Duration
	// Connect bounds establishing each TCP connection to an RFC 5321 server.
	// Zero selects 30 seconds.
	Connect time.Duration
	// PolicyFetch bounds one RFC 8461 HTTPS policy fetch. Zero selects one
	// minute, which is also the maximum: a larger value is rejected.
	PolicyFetch time.Duration

	_ struct{}
}

// DialRequest describes one connection a Deliverer needs to an RFC 5321 SMTP
// server. The Deliverer constructs it and always passes a non-nil value; a
// Dial callback reads it.
//
// Callers constructing a DialRequest literal, for example in tests, must use
// keyed fields.
type DialRequest struct {
	// Network is the network to dial, "tcp".
	Network string
	// Address is the selected IP address and the RFC 5321 SMTP port, 25.
	Address netip.AddrPort
	// MX is the MX host name the address was selected for (RFC 5321 §5.1).
	// When set it is the TLS identity of the attempt, not a name to resolve
	// again. A later routing mode that selects no MX leaves it empty.
	MX string
	// Domain is the destination's routing domain and RFC 8461 Policy Domain.
	Domain string

	_ struct{}
}

// MTASTSOptions configures RFC 8461 MTA-STS. A nil *MTASTSOptions on Options
// disables MTA-STS; there are no defaults, because Cache is required.
//
// Callers constructing an MTASTSOptions literal must use keyed fields.
type MTASTSOptions struct {
	// Cache persists RFC 8461 §5.1 policies between calls and, for durable
	// implementations, between process restarts. Both callbacks are required.
	Cache PolicyCache

	_ struct{}
}

// DANEOptions configures RFC 7672 DANE for SMTP. A nil *DANEOptions on Options
// disables DANE; a non-nil value with zero fields selects the defaults
// (opportunistic DANE).
//
// Callers constructing a DANEOptions literal must use keyed fields.
type DANEOptions struct {
	// Mode selects how absent or unusable TLSA records (RFC 7672 §2.2) are
	// treated. Empty means DANEOpportunistic.
	Mode DANEMode

	_ struct{}
}

// DANEMode selects how RFC 7672 DANE treats a destination without usable
// secure TLSA records. It is an open string type: New rejects a mode it does
// not implement rather than guessing at its meaning.
type DANEMode string

// RFC 7672 DANE modes.
const (
	// DANEOpportunistic uses DANE where secure usable TLSA records exist and
	// otherwise falls back to ordinary opportunistic TLS (RFC 7672 §2.2).
	DANEOpportunistic DANEMode = "opportunistic"
	// DANEMandatory treats the absence of secure usable TLSA records as a
	// temporary failure. It is local policy layered on RFC 7672, which itself
	// is opportunistic.
	DANEMandatory DANEMode = "mandatory"
	// DANEAudit records DANE validation failures without blocking delivery,
	// the explicit exception of RFC 7672 §8.3.
	DANEAudit DANEMode = "audit"
)

// PolicyCache persists RFC 8461 MTA-STS policies. It is a struct of function
// fields rather than an interface so that a future cache operation is an
// added field, not a breaking change to every implementation.
//
// The callbacks own persistence only. The Deliverer owns parsing, validation,
// expiry and refresh, and revalidates every loaded entry.
//
// Callers constructing a PolicyCache literal must use keyed fields.
type PolicyCache struct {
	// Load returns the stored entry for a Policy Domain (RFC 8461 §5.1) and
	// whether one exists. An error is not a miss: it defers delivery to that
	// domain, because proceeding could silently drop a policy still in force.
	Load func(ctx context.Context, req *PolicyCacheLoadRequest) (PolicyCacheEntry, bool, error)
	// Store saves an entry after a successful RFC 8461 policy fetch, replacing
	// any earlier entry for the same domain. An error is recorded in the
	// result; the freshly fetched policy is still applied.
	Store func(ctx context.Context, req *PolicyCacheStoreRequest) error

	_ struct{}
}

// PolicyCacheLoadRequest asks a PolicyCache for one RFC 8461 policy. The
// Deliverer always passes a non-nil value.
//
// Callers constructing a PolicyCacheLoadRequest literal, for example in tests,
// must use keyed fields.
type PolicyCacheLoadRequest struct {
	// Domain is the lower-case RFC 8461 Policy Domain, without a trailing dot.
	Domain string

	_ struct{}
}

// PolicyCacheStoreRequest asks a PolicyCache to save one RFC 8461 policy. The
// Deliverer always passes a non-nil value.
//
// Callers constructing a PolicyCacheStoreRequest literal, for example in
// tests, must use keyed fields.
type PolicyCacheStoreRequest struct {
	// Entry is the policy to store, keyed by Entry.Domain.
	Entry PolicyCacheEntry

	_ struct{}
}

// PolicyCacheEntry is one cached RFC 8461 MTA-STS policy. Body is the
// authoritative form: a cache must persist it unchanged, and loaded entries are
// reparsed from it, so policy keys this release does not understand survive a
// round trip through the caller's storage.
//
// Callers constructing a PolicyCacheEntry literal must use keyed fields.
type PolicyCacheEntry struct {
	// Domain is the lower-case RFC 8461 Policy Domain, without a trailing dot.
	Domain string
	// ID is the policy's "id" from the RFC 8461 §3.1 TXT record.
	ID string
	// Body is the policy file exactly as fetched (RFC 8461 §3.2), at most
	// 64 KiB.
	Body []byte
	// Policy is the parsed view of Body, for the caller's inspection. It is
	// ignored on Load; the Deliverer reparses Body instead.
	Policy MTASTSPolicy
	// FetchedAt is when the policy was fetched. Expiry is FetchedAt plus the
	// max_age parsed from Body (RFC 8461 §5.1), never Policy.MaxAge, and is
	// never extended by a failed refresh.
	FetchedAt time.Time

	_ struct{}
}

// MTASTSPolicy is a parsed RFC 8461 §3.2 policy file.
//
// Callers constructing an MTASTSPolicy literal must use keyed fields.
type MTASTSPolicy struct {
	// Version is the policy "version" field, "STSv1" (RFC 8461 §3.2).
	Version string
	// Mode is the policy "mode" field (RFC 8461 §5).
	Mode MTASTSMode
	// MX holds the "mx" patterns, in policy order (RFC 8461 §4.1).
	MX []string
	// MaxAge is the "max_age" field, at most 31557600 seconds (RFC 8461 §3.2).
	MaxAge time.Duration

	_ struct{}
}

// MTASTSMode is the RFC 8461 §5 policy mode. It is an open string type: a
// cached or fetched policy with an unknown mode is invalid and is not applied.
type MTASTSMode string

// RFC 8461 §5 policy modes.
const (
	// MTASTSEnforce requires an authenticated TLS session with a policy MX.
	MTASTSEnforce MTASTSMode = "enforce"
	// MTASTSTesting reports policy failures without blocking delivery.
	MTASTSTesting MTASTSMode = "testing"
	// MTASTSNone declares that the domain has no active policy.
	MTASTSNone MTASTSMode = "none"
)
