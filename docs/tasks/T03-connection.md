# T03 — Connection, EHLO & TLS

**Agent:** `client-core` · **Milestone:** M1 · **Depends on:** T01, T02

**Owns:** `smtpclient/{client,conn,state,ehlo,starttls,pipeline,reply}.go`, and
`smtpclient/fakeserver_test.go` — shared, append-only, see below

## Goal

The session layer: get connected, negotiate, know what the server can do, and
hold a command queue that pipelining and every later command builds on.

## Deliverables

### Construction and the delivery-layer reservations

Three things are API commitments, not conveniences. T14 is post-v1.0 and cannot
add them retroactively (`API-STABILITY.md` §9):

1. **Connection injection.** A constructor taking an established `net.Conn`.
2. **Dial address separate from TLS server identity.** Two fields, from the first
   commit. Under MX selection the certificate is validated against the MX
   hostname while the connection goes to an address derived from it; one string
   is a breaking change to fix.
3. **A dial hook** — `DialContext func(ctx, network, addr) (net.Conn, error)` in
   the options struct.

Equally binding, and equally part of this task: **do not** design a TLS-policy
interface, **do not** add MX or policy vocabulary, **do not** bring DNS anywhere
near this package. `*tls.Config` plus the address/identity split is the entire
reservation.

### Connect and greeting

- Read the `220` greeting under the RFC 5321 §4.5.3.2 timeout of 5 minutes.
- A `554` greeting means the server refuses service; surface it as `*smtp.Error`
  and do not proceed to EHLO.
- Implicit TLS (port 465, RFC 8314) and cleartext are both entry points.

### EHLO, with HELO fallback

- Send `EHLO` with a caller-supplied client identity, defaulting sensibly.
- On `500`/`502`, fall back to `HELO` and record that the session has no
  extensions at all. Do not fall back on other codes.
- Populate the extension table from T01's parser. Remember the first line is the
  greeting domain, not a keyword.
- Expose extensions through an accessor returning the keyword's parameters and a
  presence flag — never a struct of booleans (`API-STABILITY.md` §1a).

### STARTTLS (RFC 3207)

- `STARTTLS`, then the TLS handshake, then **re-issue `EHLO` and discard the
  cleartext extension list entirely**. RFC 3207 §4.2 requires this, and it is a
  security requirement, not bookkeeping: the cleartext list is attacker-supplied.
  T11 asserts it.
- TLS certificate verification is **on by default**. An option to disable it must
  be named so that it is obvious in review.
- Refuse to send credentials over an unencrypted connection unless the caller
  explicitly opts in — see T04.

### The command queue and pipelining (RFC 2920)

The core of this task, and the piece every later command depends on.

- A FIFO queue matching replies to issued commands **by counting**. RFC 2920 §3.1
  is normative: *"Command statuses MUST be coordinated with responses by counting
  each separate response and correlating that count with the number of commands
  known to have been issued"*, and *"Clients MUST NOT confuse responses to
  multiple commands with multiline responses."*
- **Sync points are structural.** These commands can only be last in a group and
  the queue must enforce it, not merely document it:
  `EHLO`, `DATA`, `VRFY`, `EXPN`, `TURN`, `QUIT`, `NOOP`.
- Bound how much is written before reading. A client that fills the server's TCP
  window while the server fills the client's deadlocks; RFC 2920 raises this
  directly.
- Pipelining engages only when the server advertises `PIPELINING`. The
  unpipelined path is **the same queue with depth one** — not a second code path.
  Two code paths means the rarely exercised one rots.
- `421` may arrive where any reply was expected. Treat it as
  connection-terminating, surface it, mark the connection unusable.

### Per-stage timeouts

RFC 5321 §4.5.3.2, as defaults, overridable:

| Stage | Minimum |
|---|---|
| Initial `220` | 5 min |
| `MAIL` | 5 min |
| `RCPT` | 5 min |
| `DATA` initiation (`354`) | 2 min |
| Data block | 3 min |
| `DATA` termination | 10 min |

A single connection-wide deadline is wrong at both ends. Every production read
observes one of these.

### State machine and shutdown

States: connected → greeted → (TLS) → authenticated → in-transaction → closed.
Invalid commands fail locally rather than on the wire. `QUIT` on close, with
`Close` remaining safe to call twice and matching `io.Closer`.

## Cancellation

SMTP has no command abort. Cancelling a command already on the wire invalidates
the connection: close it and report `context.Canceled`. Document this once, here,
and reference it from the commands in T05 rather than restating it.

## Two files that are yours because nobody else can own them

- **`pipeline.go`** — the command queue. It is the core of this task and five
  later tasks build on it, so it gets its own file rather than accreting inside
  `conn.go`.
- **`reply.go`** — assembly of `internal/smtpwire`'s wire primitives into
  `*smtp.Error` and `smtp.EnhancedCode`. T01 deliberately pushes this across the
  boundary (the codec must not import the root package), so it has to land
  somewhere owned, and this is it.

## Testing

- **`smtpclient/fakeserver_test.go`** — a scripted fake server (table of expected
  commands → replies). You own its structure; **T05, T07, T08, T09 and T10
  append cases to it**, and T08/T09 do so concurrently. Design it to be extended
  by other agents: table-driven, one case per entry, no shared mutable setup.
  Nobody deletes another task's cases.

  This is not the adversarial server — that is T11's, lives in
  `interop/harness/adversarial/**`, and is hostile rather than scripted.
- Pipelining tests that assert the sync-point list is enforced, and a regression
  test for multiline-reply-vs-multiple-replies confusion.
- HELO fallback, `554` greeting, `421` mid-session.
- Post-STARTTLS extension list is replaced, not merged.
- `smtpclient/conn_interop_test.go` once T06 lands.

## Done when

`go test -race` passes; the queue enforces sync points structurally; TLS
verification defaults on; the three reservations exist and `api-guardian` has
confirmed no policy vocabulary leaked in.
