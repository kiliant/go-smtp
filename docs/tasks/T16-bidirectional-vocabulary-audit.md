# T16 — Bidirectional vocabulary audit of `package smtp`

**Agent:** api-guardian (review) + client-core (execution) · **Milestone:** M4 ·
**Depends on:** T15 (the design that found the defects)

**Owns:** `*.go` (root package), `smtpclient/ext_b_limits.go`,
`smtpclient/trace.go`, `docs/API-STABILITY.md` §1b and §8 amendments if any.

**This task blocks v1.0.** It is an M4 exit criterion, not an M6 nicety.

## Why this exists and why it cannot wait

Adding a type to `package smtp` after the freeze is additive and always allowed.
**Reshaping one is not.** A vocabulary that has only ever been exercised in the
client direction can contain a type a server can consume but cannot naturally
produce, and no client-side review finds it, because the client is the direction
that works.

`docs/SERVER-DESIGN.md` §0 ran that review from the other direction and found
three defects. This task fixes them. Everything here is free today and a v2
otherwise.

The good news first, because it shapes the scope: `package smtp` is in far better
shape than the sibling repository's equivalent was. `RecipientResult`,
`RcptResult`, `DataResult`, `Error`, `EnhancedCode`, `Extension`, `Param`, the
DSN types and the path-size constants are all already in the shared, I/O-free
package. `DataResult []RecipientResult` in particular was shaped in M0 for LMTP's
per-recipient replies, and that is precisely what an LMTP *server* must produce.
This audit is a handful of items, not a migration.

## Item 1 — `LIMITS` is in the wrong package

`smtpclient.Limits` and `smtpclient.ParseLimitsParam` implement RFC 9422. It is
an *advertisement*: the client parses it, a server **produces** it. A backend
cannot declare its limits without `smtpserver` importing `smtpclient`, which
inverts the dependency graph the layering exists to protect.

**Verdict: move to `package smtp`.** Leave behind:

```go
// in smtpclient
type Limits = smtp.Limits
func ParseLimitsParam(params string) (smtp.Limits, error) { return smtp.ParseLimitsParam(params) }
```

A type alias preserves type identity, so every caller and every keyed struct
literal keeps compiling. This is the technique the standard library used to
relocate `context.Context`.

**Verify, do not assume:** run `apidiff` against the previous tag and confirm it
reports the move as compatible. If it does not, that finding is the deliverable
and the approach changes.

`Client.Limits()` stays where it is — it reads negotiated session state and is
correctly client-side. Note it is listed in `api_surface_test.go`'s
`nonBlockingClientMethods`; the move must not disturb that.

## Item 2 — `AllowUnadvertisedParameters`, and the question behind it

`MailOptions.AllowUnadvertisedParameters` and `RcptOptions.AllowUnadvertisedParameters`
mean "permit these parameters even when **the server** did not advertise their
extension keyword". A server has no such concept — it is the advertiser. A
server-side parser filling a `*smtp.MailOptions` would leave one field
permanently meaningless.

That field is the symptom. The question behind it — whether one options struct
serves both directions at all — **has been decided; see below.** The table is
retained because it is what the decision rests on.

| | Client (send) | Server (receive) |
|---|---|---|
| `Extra []Param` | parameters we chose not to model | parameters we did not recognise — **must be preserved verbatim**, §1b |
| `AllowUnadvertisedParameters` | a local validation opt-out | meaningless |
| `Size *int64` | what we declare | what the peer declared, to check against our own limit |
| a syntactically invalid parameter value | cannot arise; we construct it | **must arise**, and must produce a `501` naming the parameter |

The last row is the deciding one: the receive side has a failure mode with no
field to express it.

**DECIDED 2026-08-04: recommendation (a), approved with a qualification.** This
task no longer chooses; it executes and records the precedent in
`docs/API-STABILITY.md` §1b.

- **Reuse `smtp.MailOptions` and `smtp.RcptOptions` in both directions.** The
  alternative — parallel `MailParams`/`RcptParams` receive-side types — pays a
  doubling cost on every one of the seventeen parameters already implemented and
  on every one still to come.
- **`AllowUnadvertisedParameters` moves out into a client-side validation options
  type.** Not deleted: the client keeps the behaviour. It is policy about what
  this client permits itself to transmit, not SMTP wire vocabulary, and it never
  belonged in a vocabulary struct in either direction.
- **The "invalid parameter value" objection does not survive.** A syntactically
  invalid parameter is a *parse failure*, reported as an error like every other
  malformed input in this library. It is not a value inside a successfully parsed
  struct, so it needs no field and argues for no second type.

Removing an exported field is breaking, which is the entire reason this task is
M4 and not M6.

### The qualification: three kinds of parameter, not two

The receive side must distinguish, and the third is the one a naive
implementation loses silently:

| | Handling |
|---|---|
| recognised, decoded | parsed into its typed field |
| unknown, syntactically valid | preserved verbatim in `Extra []Param` per §1b — handed to the backend, never rejected |
| **original spelling** | retained where round-trip fidelity matters: keyword case, and the exact `xtext` encoding of a value, which a `Received:` line or a forwarding decision may need to reproduce |

Decide whether the third is a field on `Param` or a parallel raw slice. Either is
additive to `Param`, whose doc comment already says its field set may grow — so
this specific item could in principle land after v1.0. Do it now anyway: the
receive-side parser is being designed against it, and discovering the need later
means a second pass over every parameter.

## Item 3 — shared observability shapes in a client package

`smtpclient.TraceEvent`, `smtpclient.TraceDirection` and `smtpclient.Recipient`.

A server wants a trace hook for the reasons `API-STABILITY.md` §4a gives, and
wants the same redaction guarantee. Two incompatible `TraceDirection` types in
one process is a usability tax on anyone running both halves.

**Verdict: same alias-preserving move for `TraceEvent` and `TraceDirection`.**
Lower stakes than item 1 — a server *could* define its own — but the cost of
moving now is one alias and the cost of not moving is permanent.

`Recipient` is a judgement call: it is a client call-shape (`RcptBatch`'s input),
not obviously shared vocabulary. Decide explicitly; "leave it" is an acceptable
answer if written down.

## Method

1. Enumerate the exported surface of `smtpclient` and classify every symbol as
   *call shape* (stays), *shared vocabulary* (moves), or *client-only data*
   (stays, and say why). Record the full table in `.state/progress/T16.md`, not
   only the items that move — the negative results are what makes this auditable.
2. For each mover: relocate, alias back, run `go test ./...`, run `apidiff`,
   confirm compatible.
3. For each field on a shared type: ask whether a server can produce it. Record
   the answer for every field, including "yes".
4. Update `docs/API-STABILITY.md` with whatever this establishes as precedent.
5. `api-guardian` reviews the whole diff. Its question is the standing one and it
   has authority to reject.

## Explicitly not in scope

Adding anything a server will need but the client does not. No server-side types
land here. This task **removes asymmetries from what already exists**; it does not
anticipate. Anticipating is how a v1.0 surface acquires a permanent mistake, and
the design document is emphatic about it in both directions.

## Done when

- Every item above has a recorded verdict, executed or explicitly declined.
- `apidiff` reports every move compatible, demonstrated, not asserted.
- `go test ./...` and the interop matrix are unchanged and green.
- The full classification table is in `.state/progress/T16.md`.
- `api-guardian` has approved.
- `docs/ROADMAP.md`'s M4 exit criterion for this task is satisfiable by pointing
  at the above.
