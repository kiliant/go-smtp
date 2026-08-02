# T15 — Server framework design

**Agent:** — · **Milestone:** M5 · **Depends on:** v1.0 tagged

**Owns:** `docs/SERVER-DESIGN.md`

**Design document first. No code until the document is approved by the human.**

## Why this is post-v1.0

The same reason as T14: `package smtp` already holds the vocabulary — reply
codes, enhanced status codes, extension keywords, parameters, per-recipient
results — with no I/O and no dependency on `smtpclient`. That split was made in
M0 specifically so a server framework is additive rather than a rewrite.

So there is no cost to waiting, and a real cost to designing a server surface
while the client surface is still moving.

## In scope for the document

- The session model. The client's connection layer inverts, but the reply
  framing, transparency layer and `BDAT` framing in `internal/smtpwire` are
  direction-agnostic and should be reused rather than reimplemented. Confirm that
  claim against the code before relying on it — if the codec turns out to be
  client-shaped, that is a finding worth recording.
- The backend abstraction. `API-STABILITY.md` §4 forbids exported interfaces
  except marker interfaces and stdlib ones, and a mail server backend is the
  classic place that rule gets broken. A struct of function fields is the
  starting position; if it genuinely cannot work, that needs a written exception,
  not a quiet interface.
- Which extensions a server framework must implement to be useful at all, versus
  which are optional. `PIPELINING`, `SIZE`, `8BITMIME`, `STARTTLS`, `AUTH` and
  `ENHANCEDSTATUSCODES` are the plausible floor.
- LMTP server mode, which is a smaller and more tractable target than SMTP
  server mode and may be worth shipping first — Dovecot-style local delivery is a
  real use case with a bounded surface.
- ATRN (RFC 2645), deferred from T10 because the role reversal does not fit the
  client's session model. If it is ever supported, it belongs here.

## Explicitly not in scope

Queue management, spam filtering, mailbox storage, DKIM verification. A server
*framework* provides the protocol and hands decisions to the caller. Everything
in that list is the caller's.

## Questions the document must answer

1. Does the existing `internal/smtpwire` codec serve both directions, or does
   the server need its own? Answer with evidence from the code.
2. What is the backend surface, expressed without an exported interface?
3. How does a server-side extension get added without breaking implementors —
   the mirror of the client's §1 problem, and harder, because the caller supplies
   behaviour rather than consuming data.
4. Does any of this require a change to `package smtp`? If so, it must land
   before v1.0 or wait for v2. **Check this first** — it is the only question
   whose answer has a deadline.

## Done when

`docs/SERVER-DESIGN.md` answers all four, the human has approved it, and it has
its own task breakdown. Only then does implementation start.
