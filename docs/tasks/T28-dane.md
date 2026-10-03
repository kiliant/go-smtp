# T28 — DANE: TLSA selection, certificate validation, policy precedence (D04)

**Agent:** client-core + api-guardian · **Milestone:** M7 ·
**Depends on:** T25, T27 (for the precedence tests; TLSA work can start against
T25's resolver types)

**Owns:** `smtpdeliver/dane_*.go`, `smtpdeliver/tls_*.go` (the per-attempt
`tls.Config` construction and the DANE/MTA-STS intersection), and colocated
tests.

This is work package D04 of `docs/DELIVERY-DESIGN.md` §11. Governing text:
design §3 ("DANE algorithm ownership") and §6 (the TLS decision table).

**No DNSSEC validation in-tree.** The caller's resolver reports DNSSEC state.
This task consumes that state and owns the RFC 7672 SMTP algorithm, nothing
more. This is the zero-dependency rule, and it is not open to renegotiation
(T14, CLAUDE.md).

## Deliverables

### 1. TLSA base domain and lookup (RFC 7672 §§2.1–2.2)

Derive candidates from the original MX name and the *securely* expanded MX name.
Query `_25._tcp.<candidate>`. Stop at the first **secure** TLSA RRset. A TLSA
lookup error for a securely identified MX makes that MX unreachable. It is never
permission to fall back.

### 2. Certificate validation (RFC 7672 §3)

- Usages: DANE-TA(2) and DANE-EE(3) only. PKIX-TA(0) and PKIX-EE(1) records, and
  unknown selector or matching-type combinations, are **unusable**, not errors.
  When every record is unusable, the result is "no usable TLSA records".
- Selectors: full certificate (0) and SubjectPublicKeyInfo (1). Matching types:
  exact (0), SHA-256 (1), SHA-512 (2).
- DANE-EE(3) does no PKIX name or expiry checks. DANE-TA(2) chains to the
  matched trust anchor and checks names against the TLSA base domain plus the
  identifiers RFC 7672 §3.2.2 requires.
- Install the check through `tls.Config.VerifyConnection` on a config cloned per
  attempt. Use only `crypto/x509`.

### 3. Modes and precedence

- Empty `DANEMode` means opportunistic. Mandatory mode turns "no usable TLSA" into
  a temporary failure. Audit mode (RFC 7672 §8.3) applies only on explicit
  request. All three are open string values.
- **A secure TLSA failure always blocks. MTA-STS never overrides it**, and an
  MTA-STS-valid certificate does not rescue a failed DANE match (RFC 8461 §2).
- Implement the design §6 table row for row, including
  `smtp.DeliveryOptions.RequireTLS`. `REQUIRETLS` is never added automatically.
- The caller's `TLSConfig` may strengthen the defaults. It cannot disable an
  applied DANE or MTA-STS requirement. Test this explicitly with an
  `InsecureSkipVerify: true` caller config.

The DANE/MTA-STS intersection is unexported. Rule 4 and §9 of
`API-STABILITY.md` forbid a `TLSPolicy` interface. If the work seems to need
one, stop and escalate.

## Testing

- TLSA vectors for **every** supported selector × matching-type pair, under both
  usages, from certificates generated in the test (`crypto/x509`).
- Unusable records: PKIX usages, an unknown selector, an unknown matching type,
  and a wrong association length.
- Every DNSSEC state on the TLSA lookup: secure, insecure, bogus, indeterminate,
  unvalidated, and an unknown string.
- One test per row of the design §6 table, against a local TLS listener.
- **Mutation checks**, recorded in `.state/progress/T28.md`: let MTA-STS rescue a
  DANE failure, and let a TLSA lookup error fall back to cleartext. Both must
  turn a test red.

## Done when

The tests pass under `-race`. Every selector and matching-type pair has a vector.
The mutation records exist. api-guardian has reviewed the `DANEMode` values and
any doc-comment promises about validation behaviour, because those promises are
API.
