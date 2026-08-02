# T02 — Core types & errors

**Agent:** `client-core` (with `api-guardian` review) · **Milestone:** M0 ·
**Depends on:** nothing

**Owns:** `*.go` in the root package, plus `api_surface_test.go` until T12 takes
it over.

Runs in parallel with T01. Both must land before anything else starts.

## Goal

The shared vocabulary. Every signature in `smtpclient`, in the future
`smtpdeliver`, and in the future server framework is written in these types — so
they are the most expensive things in the repository to change, and they get the
most scrutiny.

**No I/O. No imports of sibling packages, including `internal/smtpwire`.**

## Deliverables

### The three open sets (`API-STABILITY.md` §1)

Each has a different direction of flow and therefore a different remedy. Getting
the remedy backwards is the failure this task exists to prevent.

- **`Extension`** — a `string`-backed named type for EHLO keywords, with
  constants for known ones. Unknown keywords and their parameters reach the
  caller intact.
- **`Param`** — `struct{ Keyword, Value string }`, the raw esmtp-param escape
  hatch, plus the typed parameter-value types: `BodyType`, `DSNNotify`,
  `DSNReturn`, `MTPriority` and friends, each `string`-backed, **never an enum**.
  `BODY=BINARYMIME` arriving after `BODY=8BITMIME` is exactly the event that
  breaks an exhaustive `switch`.
- **`EnhancedCode`** — `struct{ Class, Subject, Detail int; Raw string }`. RFC
  3463 defines the structure, RFC 5248 the registry; cite both in the doc
  comment. An unparseable code keeps `Raw` and must not be flattened.

### `MailOptions` and `RcptOptions` (`options.go`)

The transaction options structs live **here**, in the root package, not in
`smtpclient` — T08 and T09 both add typed fields to them and neither owns the
root package, so this is where the shared surface has to be.

Each carries typed fields for what is modelled plus **`Extra []Param`** from the
first commit. `Extra` is the §1b escape hatch: a caller needing a parameter this
library has not implemented yet must still be able to send it, and that door
cannot be added retroactively without every intervening caller having been stuck.

Ship them now even though `smtpclient` does not exist yet and both structs are
nearly empty. That is rule 3 operating exactly as intended — a command entry
point that ships without its options struct cannot gain one without breaking
every call site, so the struct must precede the method that takes it.

T08 and T09 add typed fields (`Size`, `Body`, `RequireTLS`, `Notify`, `ORCPT`,
…). Do not attempt to model those here; define the structs, the `Extra` field,
and the keyed-literal contract.

### The error type (`error.go`)

One type, per `API-STABILITY.md` §5: reply code, enhanced code, text, the
provoking command, and an optional wrapped cause. Convenience predicates
(`IsTransient`, `IsPermanent`) are methods on it, not a separate classification
type.

Per-recipient failures are a *collection of these*, not a new type.

### The per-recipient result (`result.go`) — the shape that cannot be changed later

`API-STABILITY.md` §8, and the single most important decision in this task.

LMTP (RFC 2033) returns one reply per accepted recipient after the final `.`;
SMTP returns one reply for the message. The result of submitting content is
therefore a **per-recipient collection from the first commit**, with SMTP
modelled as the single-element case.

Anything named or shaped like "the reply to DATA" is wrong *now*, while LMTP is
unimplemented, because fixing it later changes the return type of the most
frequently called method in the library. T07 adds `LHLO`; it must not be the task
that discovers this.

### Addresses and paths

Reverse-path and forward-path handling, with the RFC 5321 §4.5.3.1 figures
available as documented constants (local-part 64, domain 255, path 256). Note the
direction: these are minimums a *server* must accept, so they are not caps to
enforce against a peer.

`SMTPUTF8` (RFC 6531) permits UTF-8 in addresses. The type must not assume ASCII.

### `api_surface_test.go` — the mechanical gates, from M0

**This is not a hardening-milestone deliverable.** The sibling `go-imap`
repository shipped its options-struct rule as prose and found 28 violating
methods at the v1.0 freeze audit — a class of mistake that cannot be repaired
after a freeze. The gates exist here from the start, and grow as the surface
does:

1. **Context-first** — every blocking exported method takes
   `ctx context.Context` first.
2. **Options struct** — every command entry point takes a `*...Options`
   parameter, even where the struct is empty today.
3. **No `internal/` leak** — no exported signature references `smtpwire`,
   `smtpsasl`, `saslprep` or `unicodenorm`, including as embedded fields or
   opaque returns.
4. **Keyed-literal doc note** — exported structs callers construct carry the
   documented contract.

Two exemption maps, `optionsExemptClientMethods` and `nonBlockingClientMethods`,
both empty or minimal at the start. Adding an entry is an API decision requiring a
written exception in `API-STABILITY.md`, not a test fix. T12 takes ownership of
this file at M4; until then it is yours to extend as the surface grows.

## Hard requirements

1. `package smtp` imports nothing from this module. Enforced by a test.
2. No exported interface without an unexported marker method, except stdlib ones.
3. Every exported symbol has a doc comment naming the RFC it comes from.
4. No MX, DNS, or transport-policy vocabulary. That is T14, post-v1.0
   (`API-STABILITY.md` §9).

## Testing

- Round-trip and table tests for every parsed/formatted type.
- `FuzzParseEnhancedCode` and any other target for a type that parses text.
- The four gates above, green.

## Done when

`api-guardian` has signed off the three open sets, the error type and the
per-recipient result shape, each against a **named** future extension that
stresses it; the import-boundary test passes; the gates pass.
