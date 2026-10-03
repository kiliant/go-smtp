# T27 — MTA-STS discovery, policy parser, cache state machine (D03)

**Agent:** client-core · **Milestone:** M7 · **Depends on:** T25

**Owns:** `smtpdeliver/mtasts_*.go`, the in-memory cache helper, the
`RefreshPolicy` implementation, and colocated tests. The parser's fuzz targets
are created here and owned by T30 afterwards, as with T11 for the client.

This is work package D03 of `docs/DELIVERY-DESIGN.md` §11. It runs in parallel
with T26. The governing text is design §5. This spec lists what must be true,
not a second design.

## Deliverables

### 1. Parsers

- The `_mta-sts.<domain>` TXT record (RFC 8461 §3.1): `v=STSv1`, `id`, and
  tolerance for unknown fields. Several TXT records starting with `v=STSv1`
  means no usable record.
- The policy file (RFC 8461 §3.2): `version`, `mode`, `mx` (repeated), and
  `max_age`, capped at 31557600. Only CRLF and LF line endings. Unknown keys
  ignored. Strict on the required ones.
- MX pattern matching (RFC 8461 §4.1). `*.example.com` matches
  `mail.example.com` but neither `example.com` nor `a.b.example.com`.

Parsers return errors and never panic. Every parser gets a fuzz target.

### 2. HTTPS fetch hardening

Fetch exactly `https://mta-sts.<domain>/.well-known/mta-sts.txt`, with:
- system roots unless the caller configured the client otherwise;
- SNI for the Policy Host;
- status 200 only and **no redirects**;
- no HTTP caching;
- a `text/plain` media type;
- a 64 KiB body limit;
- a one-minute upper bound inside the caller's context.

Failed fetches for one policy ID are rate-limited to at least five minutes.

The fetch must not let the caller's `HTTPClient` re-enable redirects or raise the
limits. Clone it and override exactly those settings. Document which settings
are kept and which are replaced.

### 3. Cache state machine

Implement the design §5 table **row for row**:

- A cache-load error is **not** a miss. It defers the destination.
- A store error after a live fetch is recorded, and the live policy still
  applies.
- Expiry is computed from `FetchedAt + max_age` only. **A failed refresh never
  extends expiry.**
- An expired policy is never applied. There is no stale-while-revalidate.
- `mode: none` is stored until it expires and blocks nothing.
- Loaded entries are revalidated. The cache is not a parser bypass.
- No background goroutine. A due refresh runs inline under `Deliver`'s context.
  `RefreshPolicy` lets the caller's own scheduler refresh ahead of time.

The in-memory cache helper is documented, in its doc comment, as **losing
downgrade protection across restarts**. It is not used by default (design §5).

### 4. Policy application interface for T28/T29

Return an unexported per-destination decision that T28 intersects with DANE and
T29 applies per candidate. The exported surface is `PolicyResult` from T25, and
nothing more.

## Testing

- A fake clock (an unexported seam) and fake TXT and HTTPS servers
  (`httptest.NewTLSServer`), all local.
- One test per state-table row, plus: a refresh failure near expiry (expiry
  unchanged), a cache-load error (destination deferred, not "no policy"), a
  redirect (refused), a body over 64 KiB (refused), a wrong content type, a
  non-200 status, and the five-minute rate limit.
- **Mutation checks**, recorded in `.state/progress/T27.md`: let an expired
  policy be applied, and let a failed refresh extend expiry. Both must turn a
  test red.

## Contracts fixed by T25's API review (2026-10-03)

These are binding. The exported shape is reviewed and settled; this task fills
it in.

- **Configuration validation in `New` may only be loosened after the tag,
  never tightened.** Rejecting an `Options` that used to be accepted breaks
  callers at runtime. Every `New`-time rejection this task needs must land
  before T31.
- `PolicyCacheEntry.Body` is authoritative. Store the policy file exactly as
  fetched, and **reparse `Body` on every Load**. `Policy` is an informational
  view and is ignored on Load. This preserves keys the parser does not know.
- `NewMemoryPolicyCache(*MemoryPolicyCacheOptions) PolicyCache` is declared,
  and T27 fills in `smtpdeliver/mtasts_memory.go`. Keep the restart caveat in
  its doc comment.
- Send `Cache-Control: no-cache` on every fetch, as `Options.HTTPClient`
  documents.
- An MX that fails the policy is an `AttemptResult` with
  `Stage: StageResolve`.
- A loaded entry whose `Body` is empty or does not parse is an **invalid
  cached policy**. For example, it may have been written by a store that
  predates `Body`. Decide between "defer" and "proceed as no policy" against
  the design §5 table, record the decision, and test it. Expiry comes only from
  the `max_age` parsed from `Body`, never from `Policy.MaxAge`.

## Done when

The tests pass under `-race`. Fuzz targets exist for the TXT parser, the policy
parser and the MX matcher. The mutation records exist. RFC 8461 section
citations are checked against the RFC text.
