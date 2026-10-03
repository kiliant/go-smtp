# T26 — MX routing, loop elimination, address order, dial seam (D02)

**Agent:** client-core · **Milestone:** M7 · **Depends on:** T25

**Owns:** `smtpdeliver/route_*.go` and colocated tests.

This is work package D02 of `docs/DELIVERY-DESIGN.md` §11. It runs in parallel
with T27. It turns one `Destination` into an ordered, finite list of
`(MX hostname, preference, address)` candidates, plus the dial request for each
candidate. It sends no SMTP. That is T29's job.

## Deliverables

The algorithm is design §4, steps 1–7. Implement it as written. The points most
likely to go wrong:

1. **NXDOMAIN versus empty MX RRset.** NXDOMAIN is a permanent routing failure.
   A successful empty RRset produces the implicit MX at preference 0, targeting
   the domain itself.
2. **Null MX (RFC 7505).** A single `0 .` fails every recipient permanently and
   **never** falls through to A/AAAA. Null MX mixed with other records is a
   temporary DNS-configuration outcome. Do not guess past it.
3. **Equal-preference randomisation.** Seed from `crypto/rand`. Expose an
   *unexported* deterministic seam for tests. There is no exported shuffle hook.
4. **Loop elimination** (RFC 5321 §5.1). A local MX found by `LocalNames` or
   `LocalAddresses` discards itself and every MX of equal or worse preference.
   An empty remainder is a permanent routing-loop outcome.
5. **Address order.** Keep the resolver's order. Try every address of one MX
   before the next preference, up to the address cap from T25.
6. **Identity split.** The dial target is the literal IP on port 25.
   `smtpclient.ClientOptions.TLSServerName` is the *unexpanded* MX hostname. The
   IP is never the certificate identity. This is the reason `API-STABILITY.md`
   §9 split the two fields.
7. **DNSSEC states.** During MX resolution, `bogus`, `indeterminate` and unknown
   states delay the whole destination. Address-lookup failures skip only the
   affected MX when others remain (RFC 7672 §2.1.2). Keep the security state of
   the MX and every alias on the candidate, because T28 consumes it.

Route errors are causes in `AttemptResult` and `DestinationResult`. They are not
new exported error types. Any sentinel this task needs is unexported unless
api-guardian approves an exported one.

## Testing

Use `fakeresolver_test.go` and append cases to it.

- One table row per case in design §4: NXDOMAIN, NODATA, implicit MX, null MX,
  null MX mixed with real records, CNAME and DNAME chains, equal-preference
  groups (with the deterministic seed), loop elimination at every position,
  empty remainder, mixed A/AAAA, the address cap, and the cap opt-in.
- Every DNSSEC state on the MX lookup and on the address lookup.
- **Mutation check:** remove the null-MX guard and show a test that sees a
  fallthrough to A/AAAA fail. Record it in `.state/progress/T26.md`.

## Done when

The tests pass under `-race`. Every row of the design §4 algorithm has a test.
The mutation record exists. The doc comments cite RFC 5321 §5.1 and RFC 7505 by
section, checked against the RFC text and not from memory.
