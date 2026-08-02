# T07 — LMTP

**Agent:** `client-core` · **Milestone:** M2 · **Depends on:** T05

**Owns:** `smtpclient/lmtp.go`

## Goal

LMTP (RFC 2033) as a first-class mode of the same client.

## What this task is *not*

**The per-recipient result shape is not yours.** It is a T01/T05 deliverable and
an `API-STABILITY.md` §8 rule, because it must exist before LMTP does. If you
arrive here and find the transaction returns a single "reply to DATA", stop and
escalate to `api-guardian` — do not add an LMTP-shaped alternative return path
alongside it. Two result types for one operation is the breaking change this
sequencing exists to prevent.

Verify that first. It is a five-minute check and it determines whether this task
is small or impossible.

## Deliverables

### `LHLO` (RFC 2033 §4)

LMTP replaces `EHLO` with `LHLO`. A server speaking LMTP **must not** accept
`HELO` or `EHLO`, and there is no HELO fallback — LMTP has no pre-extension
ancestor. Reuse T03's EHLO reply parser; only the verb differs.

Mode is selected at construction, not sniffed. A client configured for LMTP that
finds an SMTP server should fail clearly rather than silently degrade.

### Per-recipient `DATA` replies

The reason this task exists. After the final `.`, the server sends **one reply
per recipient that was accepted by `RCPT`** — in the order the recipients were
accepted, not the order they were sent.

Consequences to implement and test:

- Zero accepted recipients means zero replies, not one.
- A recipient accepted at `RCPT` may still fail at delivery; both codes matter
  and both belong in the result.
- The reply count is derived from accepted-`RCPT` bookkeeping. Getting it wrong
  desynchronises the connection for every subsequent transaction, which is a
  correctness *and* a security bug — see T11's invariants.

### What LMTP does not have

No relaying, so no `MAIL FROM` source routes and no queue semantics. Extensions
are advertised through `LHLO` exactly as through `EHLO`; `PIPELINING` and
`ENHANCEDSTATUSCODES` are common, and `SIZE` and `8BITMIME` appear on Dovecot.

## Testing

- Scripted fake LMTP server: all recipients accepted; a mix of accepted and
  rejected at `RCPT`; a recipient accepted at `RCPT` then failed at delivery;
  zero accepted recipients; more replies than accepted recipients (hostile);
  fewer replies than accepted recipients (truncation).
- The last two are desynchronisation cases and belong in the corpus T11 owns.
- Interop against Dovecot LMTP on port 24 — the only real implementation in the
  matrix, which is why it is tier 1 in `docs/INTEROP.md`.

## Done when

Per-recipient results are verified against Dovecot LMTP; the reply-count
mismatch cases produce an error and mark the connection unusable rather than
desynchronising; `go test -race` passes; `docs/RFC-COVERAGE.md` LMTP row updated.
