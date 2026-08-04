# T15 — Server framework design

**Agent:** — (human-led) · **Milestone:** M5 · **Depends on:** —

**Owns:** `docs/SERVER-DESIGN.md`.

## Status: DONE — approved 2026-08-04

Design drafted 2026-08-04, **revision 4** the same day after three review rounds,
and **approved**: [`docs/SERVER-DESIGN.md`](../SERVER-DESIGN.md).

| Section | State |
|---|---|
| §6 reference backend, §8 limits, §9 nested module | **approved** 2026-08-04 |
| §0 one options type for both directions, recommendation (a) | **approved** 2026-08-04, with a qualification |
| §2 two-level function-field structure, §2a spool strategy, §2b cardinality, §2c `Reset`, §2d session auth, §4 concurrency and STARTTLS | **approved** 2026-08-04 |
| §2a incomplete-reader defence, §2a aggregate budget, §2d verification point | **approved** 2026-08-04 |
| §2d `CommitAuth`, §2a per-entry early-return rule | **approved** 2026-08-04 |
| **§2 as a whole** | **approved 2026-08-04** |

Review 3 → revision 4. All three of revision 3's contract completions were
accepted, with one gap left and three stale statements:

1. **Successful authentication was never committed to backend session state.**
   Revision 3 forbade `SCRAMCredentials` from mutating the backend closure —
   correctly — without supplying a later safe moment. §2d adds **`CommitAuth`**,
   one uniform post-success transition for every mechanism, called after all
   proof and round trips and before the `235`, and states the general rule:
   verification callbacks validate, they do not commit.
2. **The early-return rule was phrased over a whole result**, leaving a mixed
   LMTP `DataResult` undefined. Now per entry: `2xx` → `451 4.3.0`, `4xx`/`5xx`
   preserved. Refinement noted there: a result with nothing replaced is
   authoritative and gets `ResetCompleted`, not `ResetFailed` — a backend that
   deliberately rejected every recipient without reading did nothing wrong.
3. **Two stale statements and one stale test obligation** — the aggregate-budget
   summary contradicted its own normative text, the spool manager's scope was
   described two ways (now **server-instance-wide**, by decision), and
   `backendtest` still carried revision 2's "`Data` consumes or discards its
   reader", which contradicts the contract it was meant to protect.

Review 2 → revision 3. The architecture was approved and §2 held for three
contract completions, all sustained:

1. **The framework must defend against an early return from `Data`.** "MUST
   consume or discard" is a backend obligation and cannot be the only defence: a
   socket-backed reader left unread means message bytes get parsed as commands.
   §2a gains a tracked reader and a mandatory drain. The outcome rule is
   *refined* from the review's — a success claim over an unread message is
   discarded, but a **rejection is honoured**, which makes deliberate early
   rejection a supported pattern rather than a defect.
2. **AUTH assumed every mechanism verifies at the end.** RFC 7628 §3.2.2–3.2.3
   requires OAUTHBEARER to emit a JSON error challenge and consume a dummy
   response *after* validation fails. §2d moves the backend call to the
   mechanism's verification point and adds `AuthFailure`/`OAuthError` — carried
   in the result, not as a second error type, so `API-STABILITY.md` §5 still
   holds. SCRAM gains `SCRAMKeys.Result`, applied only after proof verification,
   so a key lookup can no longer be mistaken for an authentication.
3. **The spool was bounded per connection, not per server.** §2a gains
   `MaxTotalSpoolBytes`, `MaxTotalSpoolMemoryBytes` and `MaxConcurrentSpools`,
   reserved incrementally, released on every cleanup path, `452 4.3.1` on
   exhaustion, and required at construction when CHUNKING is enabled.

Three smaller corrections: the result matrix now says "cardinality-valid" rather
than "non-empty"; `ResetCompleted` no longer depends on the final reply reaching
the peer, and `ResetSessionEnd` fires only when a transaction is still open; and
each discarded chunk in the failed-BDAT state is explicitly answered `503`.

One of those had a firmer answer than the review assumed: **`N == 0` is
unreachable, not a case to define.** RFC 2033 §4.2 requires `DATA` to fail `503`
when no `RCPT` succeeded, and RFC 5321 §3.3 permits the same for SMTP, so the
framework gates before data mode and a backend may rely on `N >= 1`.

Review 1 → revision 2. The review sustained the function-field backend
*direction* — it credited the design for counting where SMTP extension pressure
actually lands instead of importing the IMAP answer by analogy — and rejected the
revision 1 §2 *contract* on five blockers, all at the boundaries where SMTP stops
being simple: **BDAT, AUTH, and transaction teardown.**

1. **BDAT did not fit the execution model.** One goroutine per connection, one
   continuous `io.Reader` for the backend, and a reply per chunk cannot all hold.
   §2a is new: a framework-owned bounded spool, with the full lifecycle contract,
   the failed-BDAT state RFC 3030 §2 requires, and the two rejected alternatives
   recorded.
2. **Prompt disconnect cancellation was not implementable.** A goroutine inside a
   backend handler is not reading the socket. §4 withdraws the promise and states
   the three signals that do hold: per-command deadline, immediate shutdown
   cancellation, best-effort disconnect detection at the next network operation.
3. **Authentication was on `Backend`, which is shared; the principal is not.**
   §2d moves it to `Session`, adds `AuthResult`, the three-identity model
   (authn / authz / transport), and `ChallengeResponse` — without which the
   revision 1 sketch could not serve CRAM-MD5 at all.
4. **`Reset` could not safely be optional.** §2c makes it required, adds
   `ResetReason` with seven paths, and fixes the ordering rules.
5. **`DataResult` was ambiguous for SMTP.** §2b makes cardinality exact per mode
   and abolishes the idea of a collapsing algorithm.

Two corrections beyond the blockers: §3's enhanced-code repair kept `4xx`, which
turned permanent rejections into retryable ones — now the primary class is
preserved (`550` + `4.7.1` → `550 5.0.0`); and §4's RFC 2920 / RFC 3207 framing
was wrong, since RFC 2920 §3.1 already makes extension commands synchronisation
points by default. That correction also caught a defect in this document's own
text: **`BDAT` is not a synchronisation point**, because RFC 3030 §2 requires a
server to handle already-pipelined chunks.

## Why this task moved earlier

It used to say "do not start before v1.0 is tagged". That was changed
deliberately on 2026-08-04, and the reason is not impatience.

Waiting was justified on the grounds that the architecture had already paid for
the server: `package smtp` holds the vocabulary and does no I/O, so the server
can be added without touching an existing signature. That is still true, and it
is still the reason the *implementation* waits.

What it does not cover is this: adding types to `package smtp` after v1.0 is
additive and always allowed, but **reshaping an existing type is not** — and a
vocabulary exercised in only one direction can contain a type the server can
consume but cannot naturally produce. No client-side review finds that, because
the client is the direction that works.

`SERVER-DESIGN.md` §0 confirms the concern is concrete rather than theoretical.
Three findings, all in the shared surface, all free to fix now and impossible
after the freeze:

1. `smtpclient.Limits` and `ParseLimitsParam` implement the RFC 9422 `LIMITS`
   advertisement. A client parses it; a server **produces** it. In `smtpclient`,
   no backend can declare its limits without `smtpserver` importing the client
   package and inverting the dependency graph.
2. `MailOptions.AllowUnadvertisedParameters` and its `RcptOptions` twin are
   client-only fields — their documented meaning is "permit a parameter the
   *server* did not advertise", which is meaningless to the party doing the
   advertising. Behind that field sits the real question: whether one options
   struct can serve both directions at all, given that the receive side has a
   failure mode (a syntactically invalid parameter value) the send side cannot
   have. **This is the one item with no safe default.**
3. `TraceEvent`, `TraceDirection` and `Recipient` are shared shapes sitting in a
   direction-specific package.

Design before the freeze; implementation after it. The design is what tells us
whether the freeze is safe.

## What the design document answers

The four questions this spec originally posed, all answered:

1. **Does `internal/smtpwire` serve both directions, or does the server need its
   own?** (§0) Answered with evidence from the code. Partly, and better than
   expected in the place that matters most: `DotUnstuffReader` already exists,
   has no caller in the module, and its `ErrBareLFTerminator` doc comment already
   takes the correct written position on SMTP smuggling. The mirrors that are
   missing — command decoding, reply encoding, EHLO advertisement encoding,
   `BDAT` parsing, enhanced-code formatting — are ~700–900 lines, all internal,
   and none of them depends on the backend abstraction.

   Two subsystems are absent in *both* directions and are net-new: **path
   parsing** (`address.go` is constants only — there is no parser, because a
   client transmits what it is handed) and **`Received:` header generation**.

2. **What is the backend surface, expressed without an exported interface?**
   (§2) A struct of function fields in two levels — `Backend` → `Session` — with
   five required fields and everything else nil-means-unadvertised. This
   **applies** `API-STABILITY.md` §4 rather than amending it, and therefore needs
   no exception.

   This is a deliberate divergence from the sibling `go-imap`, which amends its
   equivalent rule to permit interfaces. The divergence is argued in §2 rather
   than assumed: IMAP's extension pressure lands on the backend as method groups
   (nine published RFCs, ~60 nilable fields if flattened), while SMTP's lands on
   `MAIL`/`RCPT` parameter structs that already exist and are already open-ended.
   Counted against `RFC-COVERAGE.md`: fifteen implemented extensions need zero
   new backend operations, five need one function each.

3. **How does a server-side extension get added without breaking implementors?**
   (§2, §3) A new field on `Backend`, on `Session`, or on an options struct.
   Never a changed signature, never an exported interface — enforced by extending
   `api_surface_test.go` to scan `smtpserver/`, which is a data change to an
   existing gate rather than a new mechanism. `TestAPISurfaceNoExportedInterfaces`
   becomes the standing enforcement.

4. **Does any of this require a change to `package smtp`?** (§0, §9) **Yes** —
   the three findings above. This is the question with a deadline, and it is why
   [T16](T16-bidirectional-vocabulary-audit.md) is an M4 exit criterion that
   blocks v1.0.

Plus four the original spec did not ask for:

5. **Protocol baseline** (§1) — HELO is mandatory, not legacy (RFC 5321 §4.5.1);
   LMTP is a listener mode, not an extension, and the framework refuses to start
   an LMTP listener on port 25 (RFC 2033 §5) rather than warning; submission is a
   policy profile with no message fixups; the extension floor is eight keywords.
6. **The session model** (§4) — one goroutine per connection, and an explicit
   statement of why none of the sibling's event-loop, update-batch, revision-chain
   and overflow machinery has any cause here. Two complications only: pipelining
   read-ahead with structural sync points, and the STARTTLS discard, which
   contradicts RFC 2920 §3.2 requirement 9 and must — the reconciliation is
   written down so a reviewer finds it rather than assuming it was missed.
7. **Resource limits** (§8) — the threat model inverts; the server faces hostile
   *unauthenticated* clients on the internet's most-scanned port.
   `smtpwire.Limits` is client-shaped (three of its four fields bound reading a
   reply) and must grow.
8. **What this costs v1.0** (§9) — the T16 exit criterion, and `smtpserver` as a
   nested v0.x module, which needs a written exception to `API-STABILITY.md`.

## Done when — all met

- ~~The versioning question in §9 is decided~~ — **done.** Nested v0.x module,
  recorded in `docs/API-STABILITY.md` as a real exception.
- ~~The options-struct direction question in §0 is decided~~ — **done.**
  Recommendation (a) with the three-way parameter qualification. T16 unblocked.
- ~~§2's contract is approved by the human, explicitly, in writing~~ — **done,
  2026-08-04, revision 4.**

## What approval does and does not lift

**Lifted.** T18–T23 specs may be written against the approved abstraction. T16
and T17 proceed per the dependency graph. T18's spec in particular must scope
§2a's spool as a lifecycle contract with its own failure modes, aggregate
resource accounting and security tests — not as a buffer.

**Not lifted.** `smtpserver/**` code still waits for the v1.0 tag. That was always
the second of two conditions, it is a milestone decision rather than a design
one, and `ROADMAP.md` places the implementation at M6 for reasons approval does
not touch. Removing it is a separate call.
