# T20 — Base command set and the extension floor

**Agent:** server-core · **Milestone:** M6 · **Depends on:** T19

**Owns:** `smtpserver/**` command files — `smtpserver/cmd_*.go` and the
floor-extension files that are not `ext_*.go` (which is T21's prefix).

**Implementation waits for the v1.0 tag.**

## Part 1 — the mandatory commands, HELO included

RFC 5321 §4.5.1 lists what a conforming server MUST support: `EHLO`, `HELO`,
`MAIL`, `RCPT`, `DATA`, `RSET`, `NOOP`, `QUIT`, `VRFY`. **`HELO` is on that
list.** A server that answers only `EHLO` is not a conforming SMTP server, and
treating `HELO` as legacy is the most commonly shipped conformance bug in modern
server frameworks.

`VRFY` must be **implemented** even when the implementation declines: a `252` is
explicitly permitted by RFC 5321 and is what nearly every deployed server does.
That is the default when `Session.Verify` is nil — not `502`, which would claim
the command is unimplemented.

`EXPN` and `HELP` are optional and default to `502`.

| Command | Framework behaviour |
|---|---|
| greeting | written after `Backend.NewSession` returns, so a backend can refuse a connection with an `*smtp.Error`. No enhanced code (RFC 2034 §3) |
| `EHLO` | computed advertisement from T18's descriptor table. No enhanced code |
| `HELO` | accepted, no advertisement, session restricted to base SMTP. No enhanced code |
| `LHLO` | LMTP listeners only; `500` on an SMTP listener |
| `MAIL` | path parsed by T17, `Reset(ResetNewMail)` first when a transaction is open, then `Session.Mail` |
| `RCPT` | one `Session.Rcpt` per recipient; a rejection is that recipient only and earlier acceptances stand; `452` above the configured cap, never below RFC 5321 §4.5.3.1's minimum of 100 |
| `DATA` | `354`, then transparency removal, then the §2a tracked reader |
| `RSET` | `Reset(ResetExplicit)`, clears the failed-BDAT state |
| `NOOP` | answered in every state, including failed-BDAT |
| `QUIT` | `221`, framework-answered — never delegated to `Session.Close` |
| `VRFY`/`EXPN`/`HELP`/`ETRN` | delegated when the field is non-nil, defaulted otherwise |

Unknown verbs get `500`; known verbs in the wrong state get `503`; syntactically
invalid parameters get `501` naming the parameter. That last one is the receive
direction of `API-STABILITY.md` §1b's three-kinds rule: a parameter that is
**unknown but syntactically valid** is preserved verbatim in
`smtp.MailOptions.Extra` and handed to the backend, never rejected. Only a
malformed one is a `501`.

## Part 2 — `DATA`, and the transparency layer

This is the highest-risk code in the server (§8). `internal/smtpwire`'s
`DotUnstuffReader` already exists and, before this task, has no caller — T20 is
that caller.

- The end-of-data indication is `<CRLF>.<CRLF>` and **nothing else**. The
  published smuggling vectors — `<LF>.<LF>`, `<CR>.<CR><LF>`, `<CR><LF>.<CR>` —
  terminate nothing. T22 owns the vector suite; this task owns being correct.
- The final reply follows receipt of the **complete** end-of-mail indication (RFC
  5321 §3.3), which is why T18's drain is not optional and why no reply may be
  written before framing is resolved.
- `SIZE` enforcement counts client octets and is independent of what the peer
  declared, because a peer may lie or omit `SIZE`.
- The framework prepends exactly one `Received:` header (T17's generator) and it
  is **not** counted against `SIZE`.

## Part 3 — `BDAT`, and the floor's hardest row

`CHUNKING` is in the floor *conditional on §2a*, and T18 owns the spool. T20 owns
the command: parse `BDAT <n> [LAST]` via T17's framer, read exactly `n` octets,
reply `250` per successful chunk (RFC 3030 §2 — *"A 250 response MUST be sent to
each successful BDAT data block"*), and call `Session.Data` once on `LAST`.

Three rules that are easy to get subtly wrong:

- **`BDAT` is not a sync point.** Bytes after exactly `n` octets are legally the
  next pipelined command. Reading one byte too many or too few desynchronises the
  session, which is the same failure class as smuggling.
- **A short read is never end-of-chunk.** It blocks until the data deadline, then
  fails.
- **A failure consumes the announced octets first**, then replies — RFC 3030 §2 —
  and then the session is in T18's failed-BDAT state.

## Part 4 — `N >= 1`, enforced before the backend

`DATA` and `BDAT` are refused with `503` when no `RCPT` has succeeded. RFC 2033
§4.2 requires it for LMTP — *"the DATA command MUST fail with a 503 reply code"* —
and RFC 5321 §3.3 permits it for SMTP; the framework takes the permission so both
modes behave alike. This is what makes §2b's `N >= 1` a guarantee rather than a
case every backend has to branch on.

## Part 5 — emitting the result

SMTP mode: one reply for the message, from the single-entry `DataResult`.

LMTP mode: **one reply per successful `RCPT`, in issue order, duplicates
included** (RFC 2033 §4.2). The framework emits exactly what the backend
returned — no collapsing, no reordering, no deduplication. `smtp.DataResult` is
the same type the client consumes, which is `API-STABILITY.md` §8 running
backwards, and T16's audit confirmed the shape needs nothing added for the server
direction.

## Part 6 — the extension floor

Eight rows, from §1. Each is a capability descriptor (T18) plus whatever command
or parameter handling it implies:

| Extension | RFC | This task owns |
|---|---|---|
| `PIPELINING` | 2920 | the read-ahead and sync-point behaviour is T18's; the advertisement and its interaction with each command's legality is here |
| `SIZE` | 1870 | advertised cap, per-transaction enforcement independent of the declaration, `552 5.3.4` on exceed |
| `8BITMIME` | 6152 | `BODY=8BITMIME` accepted and surfaced; refusing 8-bit content in 2026 breaks essentially all real mail |
| `ENHANCEDSTATUSCODES` | 2034 | placement and class-agreement are T18's invariants; the advertisement is here |
| `STARTTLS` | 3207 | the five-step rule is T18's; the command and its state legality are here |
| `AUTH` | 4954 | the command, mechanism negotiation, and RFC 4954's sequencing rules: refused after success, refused mid-transaction, mechanism list may differ before and after TLS |
| `SMTPUTF8` | 6531 | the declaration, and passing the session's SMTPUTF8 state into T17's path parser — UTF-8 paths are accepted **only** when declared |
| `CHUNKING` | 3030 | Part 3 |

## Part 7 — the submission profile

RFC 6409 submission differs from relay by **policy, not protocol**: require
authentication, require TLS, a submission-appropriate size limit, refuse relay
for unauthenticated senders. Provide these as options on the listener.

**Do not implement RFC 6409 §8's optional message fixups** — adding `Date:` or
`Message-ID:`, completing bare addresses. Rewriting message content is the
caller's business and is out of scope at every version, exactly as MIME
composition is for the client.

## Done when

- Every command in Part 1's table has a test, `HELO` included, and `VRFY` with a
  nil `Session.Verify` answers `252` rather than `502`.
- An unknown-but-valid `MAIL`/`RCPT` parameter reaches the backend in
  `Extra []smtp.Param`; a malformed one produces `501` naming it.
- The smuggling vectors terminate nothing (the suite itself is T22's; the
  behaviour is verified here).
- `BDAT` framing is exact: a test asserts that bytes following an exact chunk are
  parsed as the next command, and that a short read blocks rather than completing.
- `DATA`/`BDAT` with zero accepted recipients answers `503` and never calls
  `Session.Data`.
- LMTP emits N replies in RCPT order for N successful `RCPT`s including
  duplicates, verified against `smtpclient` over `net.Pipe` — the client's LMTP
  path already parses this, so the round trip checks both directions at once.
- All eight floor extensions advertise and function, and `docs/RFC-COVERAGE.md`
  rows record server-side status.
