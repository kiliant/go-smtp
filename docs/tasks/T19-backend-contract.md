# T19 — Backend contract, `memory`, `backendtest`

**Agent:** server-core · **Milestone:** M6 · **Depends on:** T18

**Owns:** `smtpserver/{backend,session}.go`, `smtpserver/memory/**`,
`smtpserver/backendtest/**`.

**Implementation waits for the v1.0 tag**, as T18's does.

This is the hardest API in the library, and the one the nested-module v0.x
decision (§9) exists to protect. Write it as though it will change — because it
will, and the module version is what makes that legal.

## Part 1 — the shapes

`SERVER-DESIGN.md` §2's code block is normative, not illustrative. Reproduce it:
`Backend` with `NewSession` as its **only** field, and `Session` with five
required handlers (`Mail`, `Rcpt`, `Data`, `Reset`, `Close`), four optional
authentication fields (`Authenticate`, `ChallengeResponse`, `SCRAMCredentials`,
`CommitAuth`), and four optional operations (`Verify`, `Expand`, `Help`, `ETRN`).

Non-negotiable properties, each of which is a rule from `API-STABILITY.md`
applied rather than a preference:

- **A struct of function fields, no exported interface** (§4). The rule this
  establishes, and which `TestAPISurfaceNoExportedInterfaces` enforces from T18:
  *a new extension may add a field to `Backend`, to `Session`, or to any options
  struct; it may never change the signature of an existing field, and it may never
  introduce an exported interface.*
- **Every handler takes an options struct with a `_ struct{}` guard from its first
  commit, even where empty today** (§3, §7).
- **`Mail` and `Rcpt` take two options parameters and they must not be merged.**
  `params *smtp.MailOptions` is the wire vocabulary the peer sent; `opts
  *MailOptions` is the framework's per-call options. They grow for different
  reasons. Note the direct inheritance from T16: `smtp.MailOptions` is
  direction-neutral *because* of this call, and `API-STABILITY.md` §10 is the
  record.
- **`ctx` first, everywhere** (§2).
- **Backends see parsed paths, never raw wire strings.** Angle brackets, source
  routes, quoting and address literals are resolved by T17's parser before the
  handler is called. Every backend written against a raw string re-derives RFC
  5321 §4.1.2 badly; centralising it means it is wrong in at most one place.
- **Backends see content through an `io.Reader`, never a `[]byte`** — a 200 MiB
  message must not buffer, and the reader yields identical bytes whether the peer
  used `DATA` or `BDAT`.

## Part 2 — the data outcome contract (§2b)

Cardinality is **exact and mode-dependent**, and the framework validates rather
than trusting:

```
SMTP mode: len(result) == 1
LMTP mode: len(result) == N, one entry per successful RCPT command,
           in original RCPT order, duplicates included
```

The LMTP rule is RFC 2033 §4.2 verbatim, including *"Even if there were multiple
successful RCPT commands giving the same forward-path, there must be one reply for
each successful RCPT command."* **Deduplicating identical forward-paths is a
protocol violation, not an optimisation**, and there is deliberately no collapsing
algorithm: a framework that merges differing per-recipient outcomes into one reply
is inventing a delivery decision the backend did not make.

`N >= 1` is a **guarantee**, not a case to handle. RFC 2033 §4.2 requires `DATA`
with no successful `RCPT` to fail `503`, RFC 5321 §3.3 permits the same for SMTP,
and the framework takes it in both modes — so `Session.Data` is never called with
zero accepted recipients and no backend needs a branch for a state it cannot
observe.

`DataResult` and `error` answer different questions and are mutually exclusive:

| Return | Meaning |
|---|---|
| cardinality-valid `DataResult`, nil `error` | authoritative outcome; goes on the wire as-is |
| empty `DataResult`, non-nil `error` | no authoritative outcome — internal failure. `451 4.3.0` once in SMTP mode, once per recipient in LMTP mode |
| both non-empty and non-nil | **invalid.** Framework error: the transaction fails safely, the defect is reported |
| wrong cardinality for the mode | **invalid**, same handling |

A backend rejecting the message says so **in** the `DataResult`, with a `5xx` and
an enhanced code. `error` is not a rejection channel.

## Part 3 — the transaction lifecycle (§2c)

`Reset` is **required** and cannot fail. It returns nothing: a backend whose
cleanup failed has nothing useful to tell the peer, and giving it an error return
would create a path where a cleanup failure changes a delivery outcome.

`ResetReason` has six values — `ResetExplicit`, `ResetNewMail`,
`ResetCompleted`, `ResetFailed`, `ResetStartTLS`, `ResetSessionEnd` — and three
rules that are observable and therefore testable:

1. **`Reset` runs *before* the handler that caused it.** A `MAIL` on an open
   transaction calls `Reset(ResetNewMail)` and *then* `Mail`, so a backend never
   sees two overlapping transactions.
2. **`ResetCompleted` does not depend on the reply reaching the peer.** The
   definition, and it matters when the peer disconnects mid-write after delivery
   committed: *`ResetCompleted` is called after an authoritative delivery result
   has been obtained and after the framework has attempted to emit it. Write
   success is not a prerequisite.* Not `ResetFailed`, which would ask a backend to
   roll back something it already committed; not `ResetSessionEnd`, which would
   lose the outcome.
3. **`ResetSessionEnd` fires only when a transaction is still open at teardown.**
   Otherwise a backend using `Reset` for accounting double-counts.

`Close` is required, idempotent, called exactly once after the final `Reset`, and
is resource release — **not** protocol `QUIT`, which the framework answers.

## Part 4 — authentication (§2d)

Authentication lives on `Session`, not `Backend`, because the authenticated
principal is per-connection and must be visible to `Mail`, `Rcpt`, `Data`,
per-user limits and `Received:` generation.

Types: `Credentials` (carrying all **three** identities), `Challenge`,
`AuthResult`, `AuthFailure`, `OAuthError`, `SCRAMKeys`. `AuthFailure` is
deliberately **not** an error type — `API-STABILITY.md` §5 permits exactly one,
`*smtp.Error`, and this carries mechanism-specific data a reply has nowhere to
put.

**Three identities, and conflating them is a privilege-escalation bug:**
authentication identity (SASL `authcid`), authorization identity (`authzid`, when
supplied), transport identity (TLS client certificate). The framework **never**
decides whether an `authzid` is permitted — that is authorization, it belongs to
the backend, and a framework that silently accepted a mismatched `authzid` would
let any authenticated user act as any other.

Which field serves which mechanism (§2d's table): `Authenticate` for PLAIN,
LOGIN, EXTERNAL, OAUTHBEARER and XOAUTH2; `ChallengeResponse` for CRAM-MD5,
where the backend holds the shared secret and the framework cannot verify;
`SCRAMCredentials` for SCRAM-\* and SCRAM-\*-PLUS. Advertisement follows from the
fields — advertising `AUTH=CRAM-MD5` against a backend that cannot verify a
challenge is a lie the client discovers only after failing.

Two rules that exist because a plausible implementation gets them wrong:

- **Verification handlers validate; they must not commit.** A verification
  callback runs at the mechanism's *credential-verification point*, which is not
  the instant of successful protocol completion. `SCRAMCredentials` returning keys
  is **not** an authentication event: a backend closure that flipped itself to
  authenticated on lookup would accept anyone who knows a valid username, since
  the client has proved nothing yet. `SCRAMKeys.Result` travels with the keys and
  is applied by the framework only after the proof verifies.
- **`CommitAuth` is the one commit point, uniform across mechanisms.** Required
  whenever any verification field is non-nil; called after every mechanism-
  specific proof and round trip; called **before** the `235`, so a backend never
  observes an authenticated session on the wire before being told; never called
  for refusal, abort, malformed exchange or internal failure; cannot fail.

`error` keeps §2b's meaning here, and the symmetry is the point: a refused
credential is an *outcome* travelling in `AuthResult.Failure`, while a non-nil
`error` means no authoritative outcome exists — token service unreachable, user
database down — and becomes `454 4.7.0`, RFC 4954's temporary authentication
failure, telling the peer to retry rather than to fix its credentials.

The framework completes the mechanism-specific exchange after the handler
returns, in both directions. OAUTHBEARER is why: RFC 7628 §3.2.2–3.2.3 require
the server to emit a JSON error challenge and then consume a dummy `%x01`
response before failing, because SASL forbids diagnostics in an unsuccessful
outcome. `AuthFailure.OAuth` is what lets the framework build that challenge
without the backend reaching the wire.

## Part 5 — `smtpserver/memory`

**A supported package, not test-internal.** Four reasons, and the fourth is the
one that makes it load-bearing rather than a demo: it is the only thing putting
real pressure on §2 before it stabilises; it gives the conformance suite a target
drivable to any state; a framework nobody can run without first writing a storage
layer gets no users and therefore no bug reports; and **an in-process SMTP sink
that accepts everything and exposes the delivered messages is exactly what a test
suite for an application using `smtpclient` needs** — today those users reach for
a container or a third-party mock.

Constraints: documented as not durable and not for production; a constructor, an
options struct, and the handler fields; it implements every optional handler **the
release itself claims to support**, not every one forever; pathological-state
manipulation lives in `backendtest`, never on the supported surface.

Permanently out of scope, and this is settled: maildir or SQL storage, a user
database, queueing, forwarding, spam filtering, DKIM. A framework provides the
protocol and hands decisions to the caller.

## Part 6 — `smtpserver/backendtest`

A conformance suite a third-party backend runs against itself, and the mitigation
for §2's runtime-nil-field cost. It checks the contracts the framework relies on
and never re-derives, because breaking one of these corrupts a transaction
instead of failing visibly. §7's list, in full:

- **The incomplete-reader path, six checks:** unread `DATA` is drained before any
  reply; an early rejection survives the drain unchanged; an early `2xx` becomes
  `451 4.3.0`; a drain failure closes the connection with no final reply; unread
  `BDAT` spool content cannot desynchronise the socket but still invalidates a
  success claim; and **no command parsing resumes until framing is resolved** —
  the check that actually catches the smuggling variant.
- `DataResult` cardinality exact for the mode, duplicates included.
- `DataResult` and `error` never both non-empty.
- A rejected `Rcpt` returns an `*smtp.Error` whose enhanced class agrees with its
  three-digit code, so the framework's repair path stays a backstop rather than
  the normal case.
- `Reset` on all six reasons across all seven paths, with `Reset(ResetNewMail)`
  preceding the replacing `Mail`.
- `Close` exactly once, after the final `Reset`, idempotent.
- **A backend holding transaction state releases it on every `Reset`** — drive
  all seven paths and assert the backend's own accounting returns to zero. This
  is the check that would have caught revision 1's optional-`Reset` defect.
- **`SCRAMCredentials` does not authenticate:** a lookup followed by a *failed*
  proof, asserting `CommitAuth` was never called and the backend does not consider
  the session authenticated.
- **`CommitAuth` exactly once, on success only, before the `235`** — across all
  five mechanism shapes, plus the refusal, abort and internal-failure paths where
  it must not fire at all.

Several of these test the **framework** more than the backend. They belong here
anyway: they are what a third-party backend author would otherwise discover in
production.

## Done when

- Every field in §2's code block exists with the documented semantics, and
  `api_surface_test.go`'s gates pass over `smtpserver` including the
  no-exported-interfaces rule.
- Cardinality validation rejects every invalid combination in Part 2's table with
  a framework error, and a test covers the LMTP duplicate-recipient case
  explicitly.
- All six `ResetReason` values are reachable in a test, and the ordering rule
  (`Reset` before the causing handler) is asserted rather than assumed.
- `memory` serves a full transaction driven by `smtpclient` over `net.Pipe`, in
  both SMTP and LMTP modes.
- `backendtest` passes against `memory`, and **fails** against a deliberately
  broken backend for each contract it checks — a conformance suite that has never
  failed is not known to check anything.
- `api-guardian` has reviewed the `Backend`/`Session` surface. Its question is the
  standing one, and the nested module's v0.x status is not a licence to skip it.
