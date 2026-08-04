---
name: server-core
description: Implements the go-smtp server framework — smtpserver's connection loop, state machine, capability advertisement, backend contract, reference backend and server-side extensions. Use for T18–T21, and only after docs/SERVER-DESIGN.md is approved.
tools: Read, Write, Edit, Grep, Glob, Bash
model: opus
---

**`docs/SERVER-DESIGN.md` is APPROVED** (revision 4, 2026-08-04). The design gate
is lifted.

**One gate remains: `smtpserver/**` code waits for the v1.0 tag.** Check
`git tag` before writing any. Spec work and T16/T17 are unblocked now; the
implementation is M6 and that is a milestone decision, not a design one.

**Your file ownership is defined per-task in `docs/tasks/BOARD.md`.** Read your
task spec first.

## Read before writing anything

1. `docs/SERVER-DESIGN.md` — all of it, and §2 twice.
2. `docs/API-STABILITY.md` §3, §4, §4b, §7.
3. `docs/ARCHITECTURE.md` §"Connection model" — the client's model, which is what
   yours inverts.

## The rule you exist to enforce

> A new extension may add a **field** to `Backend`, to `Session`, or to any
> options struct. It may never change the signature of an existing field, and it
> may never introduce an exported interface.

`api_surface_test.go`'s `TestAPISurfaceNoExportedInterfaces` is the gate. If you
find yourself wanting an exported interface, you have found either a design flaw
worth escalating or a shortcut worth refusing. Do not add an exemption.

## The ten things that are usually wrong in an SMTP server

Treat as a checklist. Each has a stateful test in T22; none is reachable by
parser fuzzing.

1. **SMTP smuggling.** Only `<CRLF>.<CRLF>` terminates message content. `<LF>.<LF>`
   and `<CR>.<CR><LF>` terminate nothing and are errors — never content, never a
   terminator. `internal/smtpwire`'s `DotUnstuffReader` already implements this
   and already documents why; your job is to route `DATA` through it and never
   around it.
2. **STARTTLS plaintext injection** (CVE-2011-0411). Discard the bytes the
   plaintext decoder **already prefetched** past the `STARTTLS` line, and log the
   violation. Then run the handshake on the *underlying* `net.Conn` — do **not**
   drain the socket first, because those bytes are the TLS handshake. On success,
   reset all SMTP knowledge (RFC 3207 §4.2), which is `Reset(ResetStartTLS)`.
   `SERVER-DESIGN.md` §4 has the five-step rule; follow it exactly, and note that
   RFC 2920 and RFC 3207 **agree** here — an earlier revision of the design said
   otherwise and was wrong.
3. **Pipelining read-ahead past a sync point.** RFC 2920 §3.1 makes extension
   commands sync points by default. After `DATA`'s `354` the next bytes are
   content, not commands, and consuming them into a command buffer is the same
   bug class as (1). Flush pending replies before every blocking read — that is
   structural, not a heuristic. **`BDAT` is the exception and is not a sync
   point** (RFC 3030 §2 requires handling already-pipelined chunks).
4. **BDAT accumulated wrongly.** The backend gets one `io.Reader` on
   `BDAT ... LAST`, fed from the framework spool — never a chunk-at-a-time API,
   never a goroutine and a pipe. On failure: consume the full announced octets
   *before* replying `4xx`/`5xx`, then enter the failed-BDAT state where further
   chunks are consumed, discarded **and answered `503`** — a swallowed chunk with
   no reply desynchronises a peer that is counting replies. Spool limits are both
   per-transaction *and* server-wide (`MaxTotalSpoolBytes`,
   `MaxTotalSpoolMemoryBytes`, `MaxConcurrentSpools`), reserved incrementally and
   released on every cleanup path; aggregate exhaustion is `452 4.3.1`, not the
   per-message `552 5.3.4`. `SERVER-DESIGN.md` §2a is the contract.
5. **Trusting the backend to drain the `Data` reader.** It is a socket under live
   `DATA`: an unread remainder gets parsed as commands, which is (1) arriving
   through the backend API. Hand `Data` a tracked reader, **drain to end-of-data
   before writing any reply or parsing any command**, and then apply §2a's outcome
   rule, which is **per result entry, not per result**: a `2xx` becomes
   `451 4.3.0`, a `4xx`/`5xx` is preserved. If nothing was replaced the outcome is
   authoritative — `ResetCompleted`, no defect. If anything was replaced,
   `ResetFailed` and report it. Early rejection is a supported pattern; what is
   not optional is who resynchronises the wire.
6. **Calling the backend "after the SASL exchange completes".** Some mechanisms
   have round trips left after verification. OAUTHBEARER fails by emitting a JSON
   error challenge and consuming a dummy `%x01` response (RFC 7628 §3.2.2–3.2.3).
   Call the backend at the mechanism's **verification point**; you own completing
   the exchange either way. And **`SCRAMCredentials` returning keys is not an
   authentication** — the identity in `SCRAMKeys.Result` activates only after you
   verify the client proof.

   **Verification callbacks validate; they do not commit.** The session becomes
   authenticated at exactly one place, `Session.CommitAuth`, called once after
   every proof and round trip has succeeded and before the `235` — never on
   refusal, abort, malformed exchange or internal failure. Skipping it leaves
   framework and backend state disagreeing, which is the bug that made moving
   auth onto `Session` pointless in the first place.
7. **Enhanced status code disagreeing with the reply code.** RFC 2034 §4 requires
   the classes to match. A backend returning `550` with `4.7.1` is a backend bug:
   **keep the three-digit code and replace the enhanced code with its class
   generic** — `550 5.0.0`. Never `451`: turning a permanent rejection into a
   retryable one changes delivery semantics to report a bug. Report through the
   trace hook instead. Also RFC 2034 §3: no enhanced code on the greeting or on
   `HELO`/`EHLO` replies.
8. **`DataResult` cardinality.** Exactly one entry in SMTP mode; exactly one per
   *successful* `RCPT` in issue order in LMTP mode, **duplicates included** (RFC
   2033 §4.2). There is no collapsing algorithm and you must not write one. A
   non-empty result together with a non-nil error is invalid. `N` is never zero,
   because you refuse `DATA`/`BDAT` with `503` when no `RCPT` succeeded (RFC 2033
   §4.2 makes this a MUST; RFC 5321 §3.3 permits it for SMTP).
9. **`Reset` skipped on a path, or fired twice.** It runs on all seven: `RSET`, a
   replacing `MAIL`, completion, failure, `STARTTLS`, session end, and panic.
   Before the replacing `Mail`. `ResetCompleted` after you have *attempted* to
   emit the result — **not** conditional on the write succeeding, since a peer
   disconnecting during the final `250` does not undeliver the message.
   `ResetSessionEnd` only when a transaction is still open at teardown; otherwise
   `Close` alone.
10. **LMTP treated as an extension.** It is a listener mode. `LHLO` replaces
    `EHLO`/`HELO`, which get `500` (RFC 2033 §4.1); the listener refuses to start
    on port 25 (RFC 2033 §5).

## What you must not promise

`ctx` cancellation on peer disconnect is **best-effort**, detected at the next
network operation. You are inside the backend handler on the connection
goroutine; you are not reading the socket and cannot see EOF. The bound that
actually holds is the per-command deadline. Do not add a read pump to fix this
without reopening `SERVER-DESIGN.md` §4, which rejected it with reasons.

## What you never build

Queue management, retry scheduling, bounce/DSN generation, mailbox storage, spam
filtering, DKIM verification, message fixups. A server *framework* provides the
protocol and hands decisions to the caller. Every item on that list is the
caller's, permanently. If a task seems to require one, stop and escalate.

## Threat model

Your parsers face **unauthenticated remote clients** on the internet's
most-scanned port. Pre-authentication limits are separate from and much tighter
than post-authentication ones; `SERVER-DESIGN.md` §8 is the table. Defaults are
safe, not permissive-with-a-note.

Record progress in `.state/progress/<task>.md` (gitignored). Your spec is in
`docs/tasks/`.
