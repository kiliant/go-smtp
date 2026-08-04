# go-smtp — agent working rules

Goal: a complete, correct, **stable** ESMTP and LMTP client library for Go.
Module path: `github.com/kiliant/go-smtp`.

## The one goal that shapes every decision

Go's standard library says it out loud: *"The smtp package is frozen and is not
accepting new features."* An SMTP client that cannot absorb a new extension has
only two endings — it freezes, or it breaks its callers. `net/smtp` chose the
first. The ecosystem's replacements mostly implement a handful of extensions
each and stay at v0.

Therefore the acceptance criterion on every public API decision is:

> **Can extension N+1 — an ESMTP service extension nobody has written yet — be
> added to this API without a breaking change?**

If the answer is no, the design is wrong. This outranks brevity, elegance, and
resemblance to other libraries. An agent that ships a working feature behind an
unextensible API has failed the task, not completed it.

See `docs/API-STABILITY.md` for the concrete rules. They are not style
preferences; they are the deliverable.

## Non-negotiable API rules

1. **Open-ended sets get open-ended types.** The three that grow: EHLO extension
   keywords, MAIL/RCPT `esmtp-param` keywords, and enhanced status codes. None
   may be a closed `enum`-style constant list callers `switch` on exhaustively.
   A caller must be able to send a parameter this library has never heard of,
   and a keyword or status code we do not model must be preserved verbatim
   rather than dropped.
2. **`context.Context` is the first parameter of every blocking call**, from the
   first commit. Retrofitting it is breaking and is the single most common reason
   Go network libraries never reach v1.
3. **Options go in structs, never in positional parameters.** A new RFC adds a
   field; a new parameter breaks every caller. Every command entry point takes an
   options struct from its first commit, **even when that struct is empty** —
   adding a parameter later breaks every call site regardless of whether `nil` is
   accepted for it.
4. **Exported interfaces are a last resort.** Adding a method to an exported
   interface is a breaking change. Prefer concrete types with unexported fields,
   or function-typed callbacks in an options struct.
5. **One error type.** All protocol failures surface as `*smtp.Error` carrying
   the three-digit reply code, the enhanced status code, and the reply text. Do
   not add per-extension error types; extensions add *codes*, which is a data
   change, not a type change.
6. **`internal/` stays internal.** The wire codec must never appear in an
   exported signature, not even as an opaque return value. Once it leaks, the
   parser can never be rewritten.
7. **No struct literals across the API boundary without `_ struct{}` guards or
   documented "always construct via constructor" contracts** — adding a field to
   a struct callers build with unkeyed literals is breaking.

Anything that violates these needs an explicit, written exception in
`docs/API-STABILITY.md` — approved by the human, not by an agent.

## Scope — settled, do not widen

**In scope for v1.0:** the ESMTP and LMTP protocol client, speaking to a
**caller-supplied endpoint**. Complete extension coverage, connection injection,
and a dial hook.

**Explicitly not in the core client, at any version:** MX resolution, MTA-STS,
DANE, or any other transport-policy concept. The v1.0 client never asks *which
host should I talk to* — the caller answers that.

**Deferred to a separate post-v1.0 package** (`smtpdeliver`, see
[T14](docs/tasks/T14-delivery-design.md)): MX selection, multi-address attempt
sequencing, MTA-STS, and optional DANE. It may return structured retryable
outcomes. Durable queues, retry scheduling, bounce generation and full MTA
behaviour are out of scope permanently.

Three consequences bind implementation work **now**, which is why this is here
and not only in the roadmap:

- **Preserve connection injection.** A caller must be able to hand the client an
  already-established `net.Conn`. The delivery layer is built on this.
- **Separate the dial address from the TLS server identity.** They are the same
  string today and different strings the moment MX selection exists. Two fields
  from the first commit, not one.
- **Provide a dial hook** in the options struct.

Equally binding, the things **not** to build in anticipation:

- Do **not** implement DNSSEC in-tree. It is the reason DANE is deferred.
- Do **not** design a broad TLS-policy interface before the delivery layer
  exists. `crypto/tls.Config` plus the identity/address split is the whole
  reservation. A speculative policy interface designed against no caller is how
  a v1.0 surface acquires a permanent mistake.
- Do **not** add MX or policy vocabulary to `package smtp`.

## Protocol baseline

- **RFC 5321** (SMTP) with the ESMTP extension mechanism, **RFC 2033** (LMTP),
  and **RFC 6409** (Message Submission) as the wire targets.
- The authoritative keyword→RFC mapping is `docs/RFC-COVERAGE.md`, taken from the
  IANA *SMTP Service Extensions* registry. **Do not invent RFC numbers from
  memory.** Several widely cited numbers are stale or wrong — 8BITMIME is RFC
  6152 (not 1652), `UTF8SMTP` (5336) is the *obsoleted* predecessor of `SMTPUTF8`
  (6531), and `HOLDFOR`/`HOLDUNTIL` are RFC 4865, which a scrape of the registry
  misattributed during this repo's setup. If a keyword is not in that file, check
  the registry and add it there first.
- `draft-ietf-emailcore-rfc5321bis` is at revision 44 and sits in the RFC Editor
  queue in state *Blocked*. It is **not** an RFC. Do not implement against it or
  cite it as one; when it publishes, base-RFC citations change and that is a
  documentation change, not an API change. Re-check at each milestone.

## Layering

```
github.com/kiliant/go-smtp            package smtp        core types, errors (no I/O)
        ├── internal/smtpwire         reply/command codec, dot-stuffing — NEVER exported
        ├── internal/smtpsasl         SASL mechanisms
        ├── internal/saslprep         SASLprep (RFC 4013) credential preparation
        ├── internal/unicodenorm      NFC/NFKC normalisation, generated tables
        └── smtpclient                package smtpclient  the client
```

Dependencies point downward only. `package smtp` must not import `smtpclient`,
and must not perform I/O — it is the shared vocabulary, which is what lets the
future server framework and the delivery layer reuse it without an API break.

The server framework is **designed and approved**: `docs/SERVER-DESIGN.md`
(revision 4, approved 2026-08-04) adds `smtpserver` to the tree above and extends
`internal/smtpwire` and `internal/smtpsasl` in place. **`smtpserver` code still
waits for the v1.0 tag** — that is a milestone condition, separate from design
approval. One consequence lands before the tag:
[T16](docs/tasks/T16-bidirectional-vocabulary-audit.md), a bidirectional audit of
`package smtp`, is a v1.0 exit criterion, because reshaping a type after the
freeze is not additive.

Two decisions in that document diverge from the sibling `go-imap` and are argued
rather than assumed: the backend is a **struct of function fields**, which
applies rule 4 rather than amending it (SMTP's extension pressure lands on
`MAIL`/`RCPT` parameter structs, not on backend methods); and the session model
is **one goroutine per connection**, with none of the event-loop and update-queue
machinery an IMAP server needs, because SMTP has no unsolicited server data.

## Zero external dependencies

The standard library only. A `go.sum` entry is a stability liability we do not
control. This applies to SASL and Unicode normalisation, both of which are
reachable with stdlib plus generated tables. Test-only dependencies are also
disallowed; the interop harness shells out to `podman`.

This rule outranks DRY **across module boundaries**. `internal/saslprep`,
`internal/unicodenorm` and the SASL mechanisms overlap heavily with the sibling
`go-imap` repository. They are still vendored here as independent copies,
generators included. Importing them would mean either a `go.sum` entry or
reaching into another module's `internal/`, and neither is available. Do not
"fix" this duplication.

## Testing

- `go test ./...` — unit tests, no network, must stay fast.
- `go test -count=1 -race -tags=interop ./smtpclient`, then separately
  `go test -count=1 -race -tags=interop ./interop/...` — drives real servers
  under podman. The commands must remain sequential: separate package test
  processes own independent harness lifecycles and would otherwise collide on
  host resources. See `docs/INTEROP.md`. Requires a running podman machine.
- Interop tests **skip** on absent server extensions, never fail. A permanently
  red matrix is a matrix nobody reads. But a server failing to advertise a
  keyword its profile *claims* is a **failure** — otherwise the suite silently
  degrades to all-skip and looks green.
- Every parser gets a fuzz target. Malformed input from a hostile server must not
  panic; internal codecs return an error and the public client boundary returns
  a `*smtp.Error`.

Host note: development is on darwin/arm64 with podman. Postfix, Exim, Stalwart,
maddy, Mailpit, Dovecot LMTP and GreenMail are arm64-native; Apache James is
amd64-only and runs emulated behind `-tags=interop_emulated`.

## Plan vs state

Everything lives in this repository. All paths are repo-relative — nothing here
refers to an absolute path on one machine.

| | Where | In git |
|---|---|---|
| Task specs, dependency graph, file ownership | `docs/tasks/` | yes — it is documentation |
| Current status, progress notes, scratch | `.state/` | no — mutable coordination state |

`.state/` holds a `.gitignore` containing `*` and `!.gitignore`, so it ignores its
own contents while the protection itself stays tracked — meaning it survives a
fresh clone. `git add -A` cannot stage anything in there.

Do not create `TASKS.md`, `PROGRESS.md` or similar elsewhere in the tree —
mutable state goes in `.state/`, and nowhere else.

The dependency is one-directional by design: `docs/tasks/` never refers to
`.state/` contents, so a fresh clone with no `.state/` is complete and readable.

**Start at `docs/tasks/BOARD.md`.** It defines dependency order and which files
each task owns. **Only edit files your task owns** — that table is what makes
parallel agents safe. Write your working notes to `.state/progress/<task>.md`.

## Commit conventions

Conventional Commits (`feat:`, `fix:`, `docs:`, `refactor:`, `test:`).
Scope by package: `feat(smtpclient): add CHUNKING support`.
Any commit changing an exported symbol must say so in the body and update
`docs/API-STABILITY.md` if it sets a precedent.
