# Delivery attempt layer — design

**Status: APPROVED, revision 2, 2026-08-12.**

This document is T14's deliverable. It designs `smtpdeliver`, the post-v1
package that decides which SMTP endpoint to contact and makes one bounded
delivery attempt. It does not implement a queue, schedule retries, generate
bounces, or turn `package smtp` into an MTA.

The five questions in `tasks/T14-delivery-design.md` have concrete answers:

1. DNS is injected as a **struct of typed function fields**, not an exported
   interface and not a dependency on a DNS library. Every result carries its
   aggregate DNSSEC state and canonical name, so a caller can adapt
   `miekg/dns` plus a validating resolver without this module importing either.
2. Only a **valid, non-expired** MTA-STS policy can block delivery. A failed
   refresh keeps an unexpired cached policy in force without extending its
   lifetime; an expired policy is never used as stale blocking state.
3. The package owns a connection for one destination attempt and sends one copy
   for all recipients sharing that destination. It does **not** pool or reuse
   sessions across calls or policy domains.
4. A result has two levels: one `DestinationResult` per input destination and
   one final `RecipientOutcome` per input recipient, plus the chronological MX
   and address attempts that explain how those final outcomes were reached.
5. `API-STABILITY.md` §9's three network reservations are sufficient: no
   breaking root-module change is required. One additive client signal is
   recommended so a lost final content-completion reply (`DATA`, `BDAT LAST`,
   or `BURL LAST`) can be distinguished from a failure known to precede
   acceptance. Until it exists, the safe implementation is deliberately
   conservative and reports such failures as indeterminate.

---

## 1. Boundary, layering, and versioning

`smtpdeliver` is `github.com/kiliant/go-smtp/smtpdeliver`, a package in the root
module. It imports `package smtp` and `smtpclient`; neither imports it. It uses
only the standard library.

Unlike `smtpserver`, this package is not a nested v0 module. The design-first
gate exists precisely because a package first released from the root v1 module
is stable immediately. Its first release is therefore a root-module minor
release, expected to be `v1.1.0`, and the complete exported surface receives an
`api-guardian` review before that tag.

The dependency direction is:

```text
smtpdeliver  ──→  smtpclient  ──→  package smtp
      └─────────────────────────→  package smtp
```

`smtpdeliver` never imports an internal package. DNS wire messages, SMTP wire
frames, and SASL state do not appear in its public API.

### In scope

- RFC 5321 §5 MX resolution, implicit MX, equal-preference randomisation, and
  multi-address attempts;
- RFC 7505 null MX;
- opportunistic STARTTLS, MTA-STS (RFC 8461), and optional DANE for SMTP
  (RFC 7672);
- replaying one message to independently routed destination groups;
- exact per-destination and per-recipient outcomes, including ambiguous final
  status;
- policy caching semantics and a caller-supplied cache boundary.

### Permanently out of scope

- durable message queues, retry calendars, backoff, queue ageing, and queue IDs;
- delivery-status notification generation or bounce content;
- mailbox storage, forwarding, spam policy, DKIM/ARC signing, and MIME creation;
- DNSSEC validation or a bundled recursive resolver;
- TLS reporting (RFC 8460), which remains out of scope in `RFC-COVERAGE.md`;
- a general policy plugin interface or an exported interface of any kind.

The package reports whether a later attempt is safe and potentially useful. It
does not decide *when* that attempt occurs or when a queue gives up.

---

## 2. Unit of work and public shape

The long-lived concrete value is a `Deliverer`. It owns policy-cache
coordination and immutable configuration, but no message queue and no SMTP
connection pool.

```go
type Deliverer struct { /* unexported */ }

func New(opts *Options) (*Deliverer, error)

func (d *Deliverer) Deliver(
    ctx context.Context,
    request *Request,
    opts *DeliverOptions,
) (Result, error)

func (d *Deliverer) RefreshPolicy(
    ctx context.Context,
    domain string,
    opts *RefreshPolicyOptions,
) (PolicyResult, error)
```

Every blocking entry point has `context.Context` first and an options struct.
Every caller-constructed struct has the `_ struct{}` guard and a keyed-literal
contract. `nil` options mean documented defaults.

Construction keeps transport, policy, and observation hooks additive:

```go
type Options struct {
    Resolver       Resolver
    MTASTS         *MTASTSOptions
    DANE           *DANEOptions
    Dial           func(context.Context, *DialRequest) (net.Conn, error)
    HTTPClient     *http.Client
    TLSConfig      *tls.Config
    Identity       string
    LocalNames     []string
    LocalAddresses []netip.Addr
    Timeouts       Timeouts
    Trace          func(Event)
    _              struct{}
}

type MTASTSOptions struct {
    Cache PolicyCache
    _     struct{}
}

type DANEOptions struct {
    Mode DANEMode
    _    struct{}
}
```

`DialRequest`, `Timeouts`, and `Event` are guarded structs so a future transport
or observation need adds fields rather than parameters. `DANEMode` and event
kind are open strings. Empty DANE mode means opportunistic DANE; MTA-STS and
DANE are disabled when their option pointers are nil. `TLSConfig`, the HTTP
client's transport policy, and all caller slices are cloned or treated as
immutable; `New` never mutates caller state.

`Deliver` accepts multiple explicit routing destinations for one message:

```go
type Request struct {
    EnvelopeFrom string
    MailOptions  *smtp.MailOptions
    Message      MessageSource
    Destinations []Destination
    _            struct{}
}

type Destination struct {
    Domain     string
    Recipients []Recipient
    _          struct{}
}

type Recipient struct {
    Address string
    Options *smtp.RcptOptions
    _       struct{}
}

type MessageSource struct {
    Open func(
        ctx context.Context,
        opts *OpenMessageOptions,
    ) (io.ReadCloser, error)
    Size *int64
    _    struct{}
}
```

`Destination.Domain` is the ASCII DNS routing domain and MTA-STS Policy Domain,
not a value inferred from `Recipient.Address`. It is supplied explicitly for
four reasons:

- quoted local-parts can contain `@`, so splitting a mailbox is not parsing it;
- SMTP address literals have no MTA-STS policy association;
- internationalised mailbox spelling and the A-label used by DNS are distinct;
- explicit routes and smart hosts can make the next-hop Policy Domain differ
  from the visible recipient domain, as RFC 8461 §3.4 recognises.

The initial implementation accepts absolute, ASCII FQDNs and does not infer
search suffixes. Callers that accept Unicode domains supply the corresponding
A-label. This avoids silently inventing an IDNA profile and keeps the zero-
dependency rule intact. Address-literal routing is a later additive field on
`Destination`, not an overloaded `Domain` string.

Each domain appears at most once in a request, compared case-insensitively after
normalisation. Each recipient occurrence is independent: duplicates are
preserved and receive distinct outcomes in input order.

`MessageSource.Open` is called for each content-transfer attempt and must return
the same octets every time. A raw one-shot `io.Reader` is intentionally not the
API: MX failover after a temporary final reply requires replay, and buffering an
unbounded message inside this package would duplicate the server spool mistake
that `SERVER-DESIGN.md` explicitly avoids. `Size`, when present, is the RFC 1870
size declaration; it does not license the package to materialise the message.
The package clones `MailOptions` and supplies that value as `SIZE`. If both
`MessageSource.Size` and `MailOptions.Transport.Size` are present and differ,
request validation fails before DNS or wire I/O.

Destinations run sequentially in revision 1. This keeps `MessageSource.Open`,
cache callbacks, and caller observation deterministic and avoids creating an
implicit delivery scheduler. Parallel destination execution can be added later
as an options field without changing this shape.

---

## 3. Resolver boundary and DANE

### Why this is a function-field struct

Go's `net.Resolver` can resolve MX, IP, and TXT records, but it cannot return
TLSA records or trustworthy DNSSEC validation state. Importing a third-party
DNS library would violate the zero-dependency rule. Exporting a resolver
interface would make adding the next DNS lookup a breaking change.

The boundary is therefore an extensible struct of function fields. The sketch
below is normative in shape; exact names receive the normal API review.

```go
type Resolver struct {
    LookupMX   func(context.Context, *LookupMXRequest) (MXLookup, error)
    LookupIP   func(context.Context, *LookupIPRequest) (IPLookup, error)
    LookupTXT  func(context.Context, *LookupTXTRequest) (TXTLookup, error)
    LookupTLSA func(context.Context, *LookupTLSARequest) (TLSALookup, error)
    _          struct{}
}

type LookupState string

const (
    LookupFound    LookupState = "found"
    LookupNotFound LookupState = "not-found"
)

type DNSSECStatus string

const (
    DNSSECSecure        DNSSECStatus = "secure"
    DNSSECInsecure      DNSSECStatus = "insecure"
    DNSSECBogus         DNSSECStatus = "bogus"
    DNSSECIndeterminate DNSSECStatus = "indeterminate"
    DNSSECUnvalidated   DNSSECStatus = "unvalidated"
)
```

The named strings are open types, not closed enums. Unknown values are retained
in results and treated as unvalidated for enforcement.

Every lookup result contains:

- `State`, distinguishing NXDOMAIN (`not-found`) from a successful empty RRset
  (`found` with no records);
- the typed records (`MX`, `netip.Addr`, logical TXT strings, or `TLSA`);
- `CanonicalName`, after CNAME/DNAME processing;
- `Security`, covering the complete alias chain and terminal RRset.

The resolver adapter, not `smtpdeliver`, parses DNS packets and follows aliases.
For TXT, one returned string is one logical RR after its character-strings have
been concatenated. For TLSA, records use package-owned numeric fields and copied
association bytes:

```go
type TLSA struct {
    Usage        uint8
    Selector     uint8
    MatchingType uint8
    Association  []byte
    _            struct{}
}
```

This is sufficient for a caller to map `miekg/dns` records and validation
results into stable, dependency-free values. No type from that module crosses
the boundary.

### Defaults and validation

A zero resolver uses `net.DefaultResolver` for MX, IP, and TXT and reports
`DNSSECUnvalidated`. It has no TLSA implementation. MTA-STS works with that
default because it authenticates policies and MX certificates through Web PKI.

Enabling DANE requires the MX, IP, and TLSA callbacks to come from one
security-aware view. `New` rejects a DANE configuration with any of those three
missing. TXT is not a DANE trust input; when MTA-STS is also enabled it may use
the same resolver or the standard-library fallback. At runtime, `bogus`,
`indeterminate`, and unknown/unvalidated statuses are transient lookup failures;
`insecure` is a definitive non-DANE answer and does not itself block ordinary
delivery. This is RFC 7672 §2.1.1–2.1.2's distinction.

The package does not accept an `Authenticated bool`. A boolean cannot
distinguish an unsigned answer from a validation failure, and treating those as
the same is a downgrade vulnerability.

### DANE algorithm ownership

The caller validates DNSSEC; `smtpdeliver` owns the RFC 7672 SMTP algorithm:

1. retain the DNSSEC state of MX and every alias;
2. derive TLSA base-domain candidates from the original and securely expanded
   MX names;
3. query `_<port>._tcp.<candidate>` and stop at the first secure TLSA RRset;
4. treat TLSA lookup errors for a securely identified MX as making that MX
   unreachable, never as permission to fall back;
5. support the SMTP-applicable DANE-TA(2) and DANE-EE(3) usages and treat
   unsupported combinations as unusable records;
6. perform the usage-specific certificate and name checks with `crypto/x509`;
7. if any usable secure TLSA record exists, require STARTTLS and a match.

`DANE-EE(3)` does not perform PKIX name checks. `DANE-TA(2)` uses the TLSA base
domain as its primary reference identifier and the additional identifiers RFC
7672 §3.2.2 requires. These are library rules, not delegated policy callbacks.

DANE is opportunistic by default: no secure usable TLSA records falls back to
ordinary opportunistic TLS. A caller may select mandatory DANE through an open
string-backed option; in that mode, absence of usable TLSA records is temporary,
not permanent. An optional audit mode may record a failure and continue only
when the caller explicitly requests the RFC 7672 §8.3 exception.

When DANE and MTA-STS both apply, their requirements intersect. A secure TLSA
failure always blocks; MTA-STS must never override it. An MTA-STS-valid
certificate does not rescue a failed DANE match.

---

## 4. MX and address selection

For each destination, the routing algorithm is:

1. Resolve MX for the absolute domain.
2. NXDOMAIN is a permanent routing failure. A successful empty MX RRset creates
   the RFC 5321 implicit MX at preference 0, targeting the destination domain.
3. A single `0 .` record is null MX and permanently fails every recipient with
   the RFC 7505 meaning. It never falls through to A/AAAA. Null MX mixed with
   other MX records is a DNS configuration error recorded as temporary rather
   than guessed around.
4. Sort by ascending preference and randomise hosts within each equal-
   preference group. Randomisation uses `crypto/rand`-seeded process state and
   has an unexported deterministic test seam.
5. Apply RFC 5321 loop elimination against configured local names and addresses:
   when a local MX is found, discard it and every MX with an equal or worse
   preference. An empty remainder is a permanent routing-loop outcome.
6. Resolve each remaining MX to addresses. Preserve the resolver's order and
   try every address for that MX before moving to the next preference, subject
   to an explicit configurable cap. A non-zero cap below two is rejected unless
   the caller explicitly opts into the RFC 5321 §5.1 trade-off.
7. Dial the literal address on TCP port 25 while setting
   `smtpclient.ClientOptions.TLSServerName` to the unexpanded MX hostname used by
   the applicable TLS policy. The IP address is never reused as the certificate
   identity by accident.

Local identities are explicit `Options` fields (`LocalNames` and
`LocalAddresses`). Inferring all names by which an MTA is known from
`os.Hostname` and interface addresses is not reliable enough for loop
elimination. `New` requires at least one local identity for Internet MX delivery;
tests and explicitly routed private deployments may opt out in an aptly named
option.

An address failure does not discard sibling addresses or MX hosts. DNSSEC
failure during MX resolution delays the whole destination; a later address or
TLSA lookup failure may skip only the affected MX when other MX hosts remain,
as RFC 7672 §2.1.2 permits.

---

## 5. MTA-STS discovery, caching, and failure

### Cache boundary

MTA-STS is enabled only with an explicit cache. Losing cached enforce policies
on every process restart reopens the downgrade window the cache exists to close,
so there is no hidden process-memory production default.

The cache is another function-field struct, not an interface:

```go
type PolicyCache struct {
    Load  func(context.Context, *PolicyCacheLoadRequest) (PolicyCacheEntry, bool, error)
    Store func(context.Context, *PolicyCacheStoreRequest) error
    _     struct{}
}

type PolicyCacheEntry struct {
    Domain    string
    ID        string
    Policy    MTASTSPolicy
    FetchedAt time.Time
    _         struct{}
}

type MTASTSPolicy struct {
    Version string
    Mode    MTASTSMode
    MX      []string
    MaxAge  time.Duration
    _       struct{}
}
```

The package owns parsing, validation, expiry, refresh timing, and the decision
to apply a policy. It revalidates every loaded entry; cache contents are not a
trusted parser bypass. The callback owns persistence only. A supported in-memory
implementation is provided for tests and short-lived tools and is prominently
documented as losing downgrade protection across restart. Production examples
show durable application storage through the callbacks; durable *message*
storage remains out of scope.

A cache-load error is not a cache miss. Delivery for that destination is
temporarily deferred, because proceeding would silently discard a policy that
may still be valid. A store error after a live policy fetch is recorded, while
the live policy is still applied to the current call; the package does not turn
a recipient's cache backend outage into permanent mail loss.

### Discovery and refresh rules

For Policy Domain `example.com`, discovery reads `_mta-sts.example.com` TXT and
fetches exactly:

```text
https://mta-sts.example.com/.well-known/mta-sts.txt
```

The HTTPS client uses system roots unless explicitly configured, sends SNI for
the Policy Host, accepts only status 200, follows no redirect, bypasses HTTP
caching, requires a text policy response, limits the body to 64 KiB, and applies
a one-minute upper bound inside the caller's context. Failed fetches for one
policy ID are rate-limited to at least five minutes.

The state table is the core decision:

| Cached policy | Live discovery | Action |
|---|---|---|
| valid, unexpired | refresh not due | apply cached policy |
| valid, unexpired | due refresh (same or new ID) succeeds | validate, store, apply refreshed policy |
| valid, unexpired | TXT/fetch/validation fails | apply cached policy; record refresh failure; do not extend expiry |
| expired | new policy succeeds | validate, store, apply new policy |
| expired or absent | no usable TXT or fetch fails | proceed as no MTA-STS policy; never apply expired policy |
| any | live valid `mode: none` | store until its expiry and apply no blocking policy |

This follows RFC 8461 §3.3: a failed fetch with no non-expired cache proceeds as
though the domain has not implemented MTA-STS, while a valid non-expired cached
policy must still be applied. “Stale while revalidate” is deliberately rejected:
an expired policy that blocks every current MX is a self-inflicted outage, not a
security feature the RFC authorises.

The cache records `FetchedAt` and computes expiry only from policy `max_age`
(maximum 31557600 seconds). Refresh attempts begin before expiry, normally once
per day, but no package-owned background goroutine is created. `Deliver` performs
a due refresh inline under its context; `RefreshPolicy` exists so a caller's
existing scheduler can refresh proactively without coupling policy lifecycle to
a message attempt.

### Policy application

- `enforce`: a non-matching MX, absent STARTTLS, failed handshake, failed Web
  PKI chain, or MX-hostname mismatch makes that candidate temporarily
  unreachable. Try the next candidate. Exhaustion is temporary and never a
  permanent recipient failure until policy discovery has been rechecked.
- `testing`: record the same validation failure but allow delivery under the
  ordinary opportunistic-TLS rules. TLSRPT generation is not added implicitly.
- `none`: no active policy.

MX patterns obey RFC 8461 §4.1 exactly: `*.example.com` matches
`mail.example.com`, but neither `example.com` nor `a.b.example.com`. Invalid MX
candidates are traversed in ordinary MX order and treated as unreachable; they
are not removed in a separate pre-filtering pass.

---

## 6. TLS decision table

Direct SMTP starts in cleartext and attempts STARTTLS when advertised. The
package never retries a failed advertised STARTTLS handshake in cleartext on the
same address.

| Applicable policy | STARTTLS absent or handshake fails | Authentication |
|---|---|---|
| none | skip candidate after a failed advertised handshake; cleartext is allowed only when STARTTLS was not advertised | none |
| MTA-STS `testing` | record failure; ordinary opportunistic behaviour applies | Web PKI when TLS succeeds |
| MTA-STS `enforce` | candidate unreachable | Web PKI against MX hostname |
| usable secure DANE TLSA | candidate unreachable | TLSA rules; MTA-STS cannot override failure |
| caller-mandatory DANE | candidate unreachable, including no usable TLSA | TLSA rules |
| `smtp.DeliveryOptions.RequireTLS` | candidate unreachable | whichever stricter DANE/MTA-STS/local rule applies |

`REQUIRETLS` is never added automatically. It is an envelope request for onward
relay, not a synonym for local transport policy. When the caller supplies it,
the existing `smtpclient` check remains the final guard that it is sent only on
a TLS-protected session.

The TLS configuration is cloned per attempt. The dial address is the selected
IP, SNI and the normal certificate identity are the MX hostname, and DANE's
usage-specific verifier is installed through `tls.Config.VerifyConnection`.
Caller TLS configuration may strengthen defaults but cannot disable an applied
DANE or MTA-STS requirement.

---

## 7. Transaction and connection ownership

The package owns each connection from dial through `QUIT`/close. It does not
accept an already-negotiated `*smtpclient.Client`: doing so would make it
impossible to prove which address, MX identity, DNS result, and TLS policy
created the session.

Connection injection and the dial hook remain essential implementation seams:
the selected address is passed through the caller's dial callback, and the
resulting `net.Conn` is handed to `smtpclient.NewClient` with a distinct TLS
identity.

For one destination:

1. Begin one `MAIL` transaction on a usable MX address.
2. Send one `RCPT` per still-pending recipient using `RcptBatch`.
3. Make permanent RCPT rejections final for those recipients. Keep transient
   rejections pending for a later candidate.
4. If at least one recipient was accepted, open a fresh message reader and send
   exactly one copy for all accepted recipients, as RFC 5321 recommends.
5. Apply an authoritative final content-completion result to those recipients.
6. Continue to another candidate only for recipients still safely pending.

A permanent `MAIL` reply is final for all still-pending recipients in that
destination attempt. Connection, greeting, DNS, TLS-policy, and 4yz failures are
candidate failures and may advance to another address or MX. A permanent RCPT
reply applies only to that recipient. A permanent final DATA reply applies to
every recipient accepted in that SMTP transaction.

The engine never retries a recipient after an authoritative success. This is
what allows mixed RCPT outcomes to move to another MX without duplicating the
copy already accepted for other recipients.

### No cross-call connection reuse

No connection is pooled across `Deliver` calls, and no connection is shared
across Policy Domains even when DNS points both at the same MX. Reuse keys would
need to include at least MX identity, address, DNSSEC result, TLSA base domain,
MTA-STS policy revision, TLS configuration, and SMTP session state. Designing a
pool before a queue supplies its workload is the same speculative-interface
mistake §9 prevented in the client.

This does not forbid future reuse. A concrete cache callback or a new method can
be added later. It does prevent revision 1 from hiding delivery state in a pool
whose lifetime and invalidation rules no caller can observe.

---

## 8. Outcomes and retry safety

The disposition is an open string type:

```go
type Disposition string

const (
    DispositionDelivered     Disposition = "delivered"
    DispositionTemporary     Disposition = "temporary-failure"
    DispositionPermanent     Disposition = "permanent-failure"
    DispositionIndeterminate Disposition = "indeterminate"
    DispositionNotAttempted  Disposition = "not-attempted"
)
```

Its meanings are behavioural:

- `delivered`: an authoritative 2yz final reply transferred responsibility;
- `temporary-failure`: no delivery occurred, and retrying unchanged later is
  safe and may succeed;
- `permanent-failure`: no delivery occurred, and retrying unchanged should not
  be automatic;
- `indeterminate`: the server may have accepted the message, so automatic retry
  risks a duplicate;
- `not-attempted`: the call ended before this recipient reached a protocol
  decision, normally because the context or message source failed.

The package constructs, and callers inspect, this hierarchy:

```go
type Result struct {
    Destinations []DestinationResult
    _            struct{}
}

type DestinationResult struct {
    Domain     string
    Policy     PolicyResult
    Attempts   []AttemptResult
    Recipients []RecipientOutcome
    _          struct{}
}

type RecipientOutcome struct {
    Address     string
    Disposition Disposition
    Reply       *smtp.Error
    Cause       error
    Attempt     int
    _           struct{}
}

type AttemptResult struct {
    MX         string
    Preference uint16
    Address    netip.Addr
    Stage      AttemptStage
    Policies   []PolicyResult
    Cause      error
    _          struct{}
}

type PolicyResult struct {
    Kind       PolicyKind
    Mode       string
    Source     PolicySource
    Applied    bool
    ValidUntil time.Time
    Cause      error
    _          struct{}
}
```

`AttemptStage`, `PolicyKind`, `PolicySource`, and policy mode values are open
strings. Attempt records are chronological evidence, not a second final result.
The `Attempt` index on a recipient points to the attempt that made its outcome
final. SMTP replies stay as `*smtp.Error`; DNS, policy, TLS, and transport causes
use their original error chains so `errors.Is`/`errors.As` keep working.

Every valid input recipient appears exactly once in final results, in input
order and including duplicates. A destination can therefore contain delivered,
temporary, permanent, and indeterminate recipients simultaneously.

Normal remote delivery failures return a populated `Result` and nil call-level
error. Invalid requests return no result and an error. Context cancellation or a
local message-source failure returns the partial result plus that error, with
untouched recipients marked `not-attempted`. This separates “the remote declined
mail” from “the requested operation itself did not complete”.

### The final-reply ambiguity

RFC 5321 transfers responsibility only when the client receives the positive
completion reply after the end-of-data indication. If the terminator was sent
but the reply was lost, the client cannot know whether the server accepted the
message. Trying another MX may duplicate it.

The current `smtpclient.Data`, BDAT, and BURL paths return `*smtp.Error` for
transport failures but do not expose whether the failure occurred before or
after a content-completion operation may have reached the peer. The safe
delivery implementation can classify any transport failure after content
transfer starts as `indeterminate`; that is correct but unnecessarily broad.

Before the attempt engine lands, add an exported sentinel such as
`smtpclient.ErrFinalStatusUnknown`. The public error remains `*smtp.Error`; the
sentinel is wrapped underneath it through `smtp.Error.Err` only when a final
content-completion operation may have reached the peer but no authoritative
final status was obtained. This rule covers at least:

- DATA's final terminator;
- a `BDAT ... LAST` frame; and
- a `BURL ... LAST` command.

A failure while sending ordinary DATA content, a non-LAST BDAT chunk, or a
non-LAST BURL does not carry this classification: none of those operations can
complete the transaction. A received positive or negative final reply is
authoritative and likewise does not carry it.

LMTP applies the same rule per recipient. After the DATA terminator, the client
reads one final reply for each accepted recipient in RCPT order. If that stream
fails after `k` replies, `Data` returns the `k` authoritative
`smtp.RecipientResult` values as a prefix plus an outer `*smtp.Error` wrapping
`ErrFinalStatusUnknown`. The remaining accepted recipients, which are absent
from the partial result, are indeterminate. It does not synthesise code-zero
recipient replies or discard statuses already received. Failure before the
first LMTP final reply therefore returns an empty result with the same
classification. D00 updates the `DataResult` contract to document this partial-
result-on-error case and mutation-tests both the known prefix and unknown
suffix.

`errors.Is(err, smtpclient.ErrFinalStatusUnknown)` then supplies exact retry
safety without a second error type or a changed method signature. It is an
additive post-v1 change, preserves the one-error rule, and needs an
`api-guardian` review plus mutation tests around short writes and lost replies.
The three §9 reservations therefore still suffice; this is additional outcome
data, not a breaking repair.

---

## 9. Limits, cancellation, and observability

Attempt work is bounded independently at DNS, policy fetch, connect, greeting,
SMTP command, and message-transfer stages. SMTP command defaults inherit
`smtpclient`'s RFC 5321 limits; DNS, HTTPS, and connect limits belong to
`smtpdeliver.Options`. A caller context is always the outer bound.

`MaxAddresses` limits work within one destination while retaining the RFC 5321
default of trying all relevant addresses. `MaxDestinations` protects accidental
unbounded requests. Neither is retry scheduling.

Cancellation before content is sent is safely temporary/not-attempted.
Cancellation once final acceptance may be ambiguous follows §8 and never gets
silently converted to an ordinary retryable result.

Observation is a callback taking one guarded event struct. It reports DNS,
policy, address, TLS, and SMTP-attempt lifecycle without message content,
credentials, policy HTTPS bodies, or certificate private material. The event
kind is an open string. It is synchronous and must not call back into the same
`Deliverer`. Results remain the authoritative record; tracing is diagnostic and
may be dropped by callers.

---

## 10. Rejected shapes

| Rejected | Why |
|---|---|
| exported `Resolver` or `PolicyCache` interface | adding a future lookup or cache operation breaks every implementation |
| importing `miekg/dns` | violates zero dependencies and leaks its stability into ours |
| `Authenticated bool` on DNS results | conflates insecure unsigned data with bogus/indeterminate lookup failure |
| accepting one `io.Reader` | cannot replay after safe MX failover without unbounded buffering |
| deriving routing domains by splitting recipient strings | mishandles quoting, SMTPUTF8, address literals, and explicit routing |
| one flat result slice | loses which policy domain and candidate produced a recipient outcome |
| retrying every failed content-completion operation on another MX | duplicates messages when the final reply was lost |
| using an expired MTA-STS policy while refreshing | lets stale policy cause indefinite recipient-controlled outage |
| silently using an in-memory MTA-STS cache | loses downgrade resistance at every restart |
| pooling SMTP sessions in revision 1 | invents queue lifetime and policy invalidation without a caller |
| a general `TLSPolicy` interface | repeats the speculative abstraction explicitly prohibited by §9 |

---

## 11. Implementation task breakdown

The `Dxx` identifiers below are deliberately local design work-package labels,
not mutable T14 status and not task-board IDs. T14 is complete when this design
is approved. Before implementation begins, each work package must receive a
normal `Txx` task spec and an ownership row in `BOARD.md`; the next available
range is T24–T31.

| ID | Work | Depends on | Principal ownership |
|---|---|---|---|
| D00 | Add and mutation-test the additive `ErrFinalStatusUnknown` client signal for DATA, BDAT LAST, BURL LAST, and partial LMTP final replies; API review | T14 | `smtpclient` + api-guardian |
| D01 | Package skeleton, guarded public request/result/config types, API-surface gates, stdlib resolver adapter | approval | `smtpdeliver/**` |
| D02 | MX/implicit/null routing, local-loop elimination, address ordering and dial seam | D01 | `smtpdeliver` routing files |
| D03 | MTA-STS TXT/policy parsers, cache contract, refresh state machine, HTTPS fetch hardening | D01 | `smtpdeliver` policy files |
| D04 | DNSSEC-aware resolver adapter boundary, TLSA selection, DANE-TA/EE certificate validation, MTA-STS/DANE precedence | D01, D03 | `smtpdeliver` TLS files |
| D05 | Multi-destination attempt engine, message replay, SMTP transaction mapping, exact outcome classification | D00, D02, D04 | `smtpdeliver` delivery files |
| D06 | Fuzzing, adversarial DNS/policy fixtures, fake-clock cache tests, SMTP interop, mutation checks, leak checks | D02–D05 | fuzz-hardening + interop-harness |
| D07 | Full API review, docs, examples, RFC coverage, CHANGELOG, CI and root-module minor release | D06 | docs-release + api-guardian |

D02 and D03 can run in parallel after D01. D04 can begin against D01's resolver
types while D03 finishes, but their policy-precedence tests land together. D05
waits for the final-status signal because retry correctness is its central
contract, not a later hardening enhancement.

### Acceptance evidence

- table tests for every row in the MX, MTA-STS, TLS, and disposition tables;
- a fake resolver capable of secure, insecure, bogus, indeterminate, NXDOMAIN,
  NODATA, CNAME/DNAME, null MX, and mixed address answers;
- MTA-STS fake-clock tests proving expiry is never extended on refresh failure;
- TLSA vectors for every supported selector/matching pair and for unusable
  records, with DANE failures never rescued by MTA-STS;
- scripted SMTP and LMTP peers proving mixed RCPT outcomes, failover without
  duplicate delivery, indeterminate final-reply loss for DATA/BDAT LAST/BURL
  LAST, and preservation of an LMTP authoritative-result prefix;
- fuzz targets for MTA-STS TXT/policy parsing, resolver-result validation, TLSA
  matching inputs, and outcome assembly;
- mutation records showing the null-MX, expired-policy, DANE-failure, and lost-
  final-reply tests fail when each guard is removed;
- `go test ./...`, race, vet, staticcheck, gofmt, zero-dependency, apidiff, and
  interop evidence on the release tree.

---

## Appendix: RFC claims checked for this revision

| Claim | Source |
|---|---|
| no MX means an implicit preference-0 MX; NXDOMAIN is an error; temporary DNS failure is retryable | [RFC 5321 §5.1](https://www.rfc-editor.org/rfc/rfc5321.html#section-5.1) |
| equal-preference MX hosts are randomised; multihomed addresses are tried in resolver order | [RFC 5321 §5.1](https://www.rfc-editor.org/rfc/rfc5321.html#section-5.1) |
| one copy should serve multiple recipients at the same destination | [RFC 5321 §4.5.4.1](https://www.rfc-editor.org/rfc/rfc5321.html#section-4.5.4.1) |
| responsibility transfers on the positive completion reply after end of data | [RFC 5321 §4.2.5](https://www.rfc-editor.org/rfc/rfc5321.html#section-4.2.5) |
| `0 .` is null MX and must not fall through to address records | [RFC 7505 §§3–4](https://www.rfc-editor.org/rfc/rfc7505.html#section-3) |
| only non-expired cached MTA-STS policy is applied; failed fetch without one proceeds as no policy | [RFC 8461 §§3.3, 5.1](https://www.rfc-editor.org/rfc/rfc8461.html#section-3.3) |
| MTA-STS enforce failures are temporary and require a policy-ID recheck before permanent failure | [RFC 8461 §5](https://www.rfc-editor.org/rfc/rfc8461.html#section-5) |
| MTA-STS must not override failing DANE validation | [RFC 8461 §2](https://www.rfc-editor.org/rfc/rfc8461.html#section-2) |
| DANE needs secure/insecure/bogus/indeterminate DNSSEC states; bogus and indeterminate are lookup failures | [RFC 7672 §2.1](https://www.rfc-editor.org/rfc/rfc7672.html#section-2.1) |
| secure usable TLSA records require authenticated TLS; lookup failure makes that MX unreachable | [RFC 7672 §§2.1.2, 3.2](https://www.rfc-editor.org/rfc/rfc7672.html#section-2.1.2) |
| SMTP uses DANE-TA(2)/DANE-EE(3), with different name-check rules | [RFC 7672 §§3.1–3.2](https://www.rfc-editor.org/rfc/rfc7672.html#section-3.1) |
