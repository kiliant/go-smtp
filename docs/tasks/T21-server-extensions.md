# T21 — Server extensions beyond the floor, including `ATRN`

**Agent:** server-core · **Milestone:** M6 · **Depends on:** T20

**Owns:** `smtpserver/ext_*.go`.

**Implementation waits for the v1.0 tag.**

The floor is T20's. This task is everything above it, and the shape is the same
for every row: a capability descriptor (T18), parameter syntax validation in the
framework, and the **decision** delegated to the backend through a receive-side
options field.

## The division of labour, once, for all of them

§3's table settles it: for `REQUIRETLS`, `DSN`, `MT-PRIORITY` and the rest, *the
framework parses and validates syntax, the backend decides*. That means:

- The framework never invents a policy. It does not decide whether a `DSN`
  `NOTIFY=` request is honoured, whether `MT-PRIORITY=` is respected, or whether
  a `DELIVERBY` deadline is achievable. It parses, validates against the RFC's
  grammar, rejects malformed input with `501`, and hands a typed field to the
  backend.
- **A field is only ever populated for an advertised extension** (T18's
  pairing gate). A client sending `RET=HDRS` with no `DSN` advertised gets `501`,
  and the backend never sees a field it cannot know is meaningless.
- The typed field is the **existing** `smtp.MailOptions` / `smtp.RcptOptions`
  field. T16 made those structs direction-neutral for exactly this reason
  (`API-STABILITY.md` §10), so this task adds no direct fields to either one.
  T21's receive-side audit did activate three additions T16 deliberately left
  for the first server producer: exact-xtext companions on the guarded nested
  option structs, `DeliverByOptions.Trace`, and the open `Limits.Extra` registry
  escape hatch. Those are additive completions of already-shared vocabulary,
  not a second receive-side model.

## The rows

| Extension | RFC | Receive-side field | Notes |
|---|---|---|---|
| `DSN` | 3461 | `DeliveryOptions.DSN`, `RecipientDeliveryOptions.DSN` | `RET=`, `ENVID=`, `NOTIFY=`, `ORCPT=`. `ENVID` and `ORCPT` are xtext — see the spelling rule below |
| `LIMITS` | 9422 | — (advertisement only) | **Cooperative**: the backend declares, the framework formats and advertises. `smtp.Limits` is where the type lives, moved there by T16 precisely so a backend could reach it |
| `REQUIRETLS` | 8689 | `DeliveryOptions.RequireTLS` | §2/§5's receiving-server obligations are real: a server generating a bounce for a REQUIRETLS message must prefer `RET=HDRS` over `RET=FULL` and tag the bounce. The framework does not generate bounces, so surface the flag and document the obligation as the backend's |
| `MT-PRIORITY` | 6710 | `DeliveryOptions.MTPriority` | string-backed and open; a value outside any range we validate still round-trips |
| `DELIVERBY` | 2852 | `DeliveryOptions.DeliverBy` | `by-mode` is mandatory in the grammar; a missing one is `501`, not a guessed default |
| `FUTURERELEASE` | 4865 | `DeliveryOptions.FutureRelease` | `HOLDFOR`/`HOLDUNTIL` are mutually exclusive. RFC 4865, **not** 6729 — a registry scrape got this wrong during this repo's setup |
| `RRVS` | 7293 | `RecipientDeliveryOptions.RRVS` | `RCPT` only. T16 removed the sender-level field and `TestAPISurfaceNoSenderLevelRRVS` keeps it removed; a server-side parser must never look for one on `MAIL` |
| `BINARYMIME` | 3030 | `TransportOptions.Body` | requires `CHUNKING`; T18's `NewServer` refuses to start otherwise |
| group C | 3865, 3885, 4405, 4141 | `LegacyOptions`, `RecipientLegacyOptions` | `SOLICIT=`, `MTRK=`, `SUBMITTER=`, `CONPERM`, `CONNEG` |

`docs/RFC-COVERAGE.md` is the authoritative keyword→RFC mapping and **the source
for every number above**. Do not add a row from memory; if a keyword is missing,
check the IANA registry, add it there first, then implement.

## The xtext spelling rule — inherited, binding

`API-STABILITY.md` §1b, as amended by T16, splits the receive side into three
kinds of parameter, and the third is this task's problem:

- **unknown, syntactically valid** → preserved verbatim in `Extra []smtp.Param`.
  T17 owns the `Param` field that carries the original spelling; T16's decision
  binds that shape.
- **recognised and decoded** → the typed field holds the *decoded* value, and the
  peer's exact xtext encoding is **not recoverable from it**: `EncodeXtext` passes
  bytes 33–126 other than `+` and `=` through unchanged, so a peer may send `+41`
  where this library emits `A` and both decode to `"A"`. `ENVID=`, `ORCPT=` and
  `SUBMITTER=` are all in this class. T16 explicitly left the shape for this case
  to **T17** rather than binding it; this task consumes whatever T17 chose and
  must not invent a second mechanism.

Where round-trip fidelity matters — a `Received:` line, a forwarding decision —
use that mechanism. Do not re-encode a decoded value and assume it matches what
arrived.

## `ATRN` (RFC 2645)

The one extension whose **natural home is the server**, and the reason it is
recorded rather than dropped: T10 deferred it from the client because role
reversal does not fit a client session model, and `smtpclient` refuses it
outright with `ErrATRNRoleReversal`. The server is the party that hands the
connection back.

It is also the only row here that needs design before code:

- **Write the design note first**, in this repository, against the RFC text and
  not from memory: what `ATRN` does to the session state machine, who owns the
  connection after the role reverses, what the framework hands the caller, and
  what happens on `RSET`, `QUIT` and disconnect in the reversed state.
- The framework almost certainly does **not** implement the reversed direction
  itself — that would make `smtpserver` an SMTP *client*, which is
  `smtpclient`'s job and would invert the layering. The plausible shape is that
  the framework validates the request, consults the backend, and hands the
  established `net.Conn` back to the caller, who may then drive it with
  `smtpclient` via connection injection — which is one of the three delivery-layer
  reservations `API-STABILITY.md` §9 required from T03, and this is a second
  caller for it.
- If the design concludes `ATRN` cannot be supported without an exported
  interface or a reshaped core type, **stop and record it.** A deferred extension
  is a smaller cost than a permanent mistake, and the client already demonstrates
  that declining a role reversal is a legitimate answer.

## Done when

- Every row above advertises only when its backend prerequisite is present, and a
  test drives the unadvertised case to `501` for each one.
- No field is populated for an unadvertised extension — T18's declaration test
  covers the mechanism; this task's job is to have no exceptions.
- `smtp.MailOptions` / `smtp.RcptOptions` gained **nothing** directly. The
  additive guarded companion fields described above are reviewed with
  `apidiff`; the deliverable is zero incompatible root-module changes.
- Malformed values are `501` naming the parameter, with a fuzz target per parser
  (landed here, owned by T11/T22 afterwards, per `BOARD.md`'s created-by/owned-by
  table).
- `docs/RFC-COVERAGE.md` records server-side status for every row, sourced from
  the registry.
- `ATRN` either has a written design note **and** an implementation, or a written
  record of why it is deferred again. Silence is not an outcome.
