# T05 — Mail transaction

**Agent:** `client-core` · **Milestone:** M1 · **Depends on:** T03

**Owns:** `smtpclient/{mail,rcpt,data,txn,verify}.go`

## Goal

The transaction: `MAIL FROM`, `RCPT TO`, `DATA`, and the housekeeping commands.
This is the surface callers actually use, so it is the surface the v1.0 freeze
most constrains.

## Deliverables

### The transaction shape

`MAIL` opens a transaction, `RCPT` adds recipients, content submission closes it.
`RSET` abandons it.

Every entry point takes `ctx` first and an options struct, **even where the
struct is empty today** (`API-STABILITY.md` §3). This is where the sibling repo's
28 violations came from; the gate in `api_surface_test.go` is already live.

### The result type — inherited from T02, not invented here

The result of submitting content is a **per-recipient collection**
(`API-STABILITY.md` §8). SMTP is the single-element case; LMTP (T07) is the N-element
case. Do not introduce a "reply to DATA" type, and do not add one later as an
LMTP special case — that is a breaking change to the most-called method in the
library.

Recipient-level failures are `*smtp.Error` values inside that collection, not a
new error type. A `RCPT` rejected with `550` while others succeed is normal
operation, not a transaction failure — the caller decides whether to proceed.

### `MAIL FROM` and `RCPT TO`

- Reverse-path and forward-path encoding, including the null reverse-path `<>`
  for bounces, and source routes tolerated on parse but never generated.
- esmtp-params via typed `MailOptions`/`RcptOptions` fields plus the `Extra
  []Param` escape hatch. T08 and T09 add the typed fields; this task builds the
  mechanism and the `Extra` path.
- **Validate parameters locally against the advertised extension set before
  writing**, including entries in `Extra`, with a documented caller opt-out. A
  `501` from a strict server is a worse diagnostic than a local error naming the
  missing extension.
- `SMTPUTF8` addresses must survive: no ASCII assumption anywhere in this path.

### `DATA` — the two-phase command

- Send `DATA`, read the `354` under the 2-minute timeout, stream dot-stuffed
  content through T01's filter under the 3-minute per-block timeout, write
  `CRLF.CRLF`, read the final reply under the 10-minute timeout.
- Content is an `io.Reader` or an exposed `io.WriteCloser`. Never a `[]byte`
  parameter — a 200 MiB message must not buffer, and a `[]byte` signature can
  never be widened.
- `DATA` is a pipelining sync point; the queue enforces that.
- Cancellation mid-content closes the connection. `RSET` recovers an aborted
  transaction, **not** a half-written `DATA`. Reference the central statement in
  T03 rather than restating the rule.

### Housekeeping commands

`RSET`, `NOOP`, `QUIT`, `VRFY`, `EXPN`, `HELP` (all RFC 5321). `VRFY`, `EXPN`,
`NOOP` and `QUIT` are pipelining sync points.

`VRFY` and `EXPN` are disabled or deliberately uninformative on most modern
servers; the doc comments say so, so callers do not build on them. Cite RFC 5321
for these, **not** `draft-ietf-emailcore-rfc5321bis` — the IANA registry cites the
draft for `VRFY`, but it is not an RFC.

## Testing

- Scripted fake server covering: all recipients accepted; some rejected; all
  rejected; `354` refused; server drops after `354`; `421` mid-transaction.
- Dot-stuffing fixtures from `docs/INTEROP.md` round-tripped through a real
  server once T06 lands — a body line of exactly `.` is the canonical case and
  cannot be proven by unit test alone.
- Memory-bound test on a large message; peak allocation stays flat.
- `smtpclient/data_interop_test.go`.

## Done when

`go test -race` passes; the per-recipient result shape is in place and
`api-guardian` has approved it; local parameter validation works with a
documented opt-out; every entry point has an options struct and the gate is
green.
