# go-smtp

An ESMTP and LMTP client library for Go, built so that complete extension
coverage and a stable v1.0 are compatible goals rather than competing ones.

```
import "github.com/kiliant/go-smtp/smtpclient"
```

> **Status: pre-v1 release-candidate ready.** The ESMTP/LMTP client,
> authentication, transaction API, extension groups A–C, API freeze review,
> fuzz targets, examples, release gates, and default seven-server
> interoperability harness are implemented. Local CI-equivalent checks and
> representative Postfix, Dovecot, and GreenMail interop runs pass. The new
> GitHub PR and nightly workflows still need their first hosted runs before
> those external checks can be claimed as operational evidence. LMTP and
> authenticated submission also need a second independent server before their
> coverage can be marked verified; see `docs/RFC-COVERAGE.md`.

## The design constraint

Go's standard library says the quiet part out loud: *"The smtp package is frozen
and is not accepting new features."* That is the honest end state for an SMTP
client that models extensions as a closed set — freeze, or break your callers
with every new RFC. The ecosystem's replacements mostly implement a handful of
extensions each and stay at v0.

So this library measures every public API decision against one question, written
down in `docs/API-STABILITY.md`:

> Can an ESMTP extension nobody has written yet be added without a breaking
> change?

In practice that means the sets that grow get types that grow with them — and
they grow in *different directions*, which is the part most designs get wrong:

- **EHLO keywords** flow server→client, so an unknown one must be **preserved**,
  parameters included. `SIZE`, `AUTH` and `LIMITS` carry their whole payload
  there; a `bool` field discards it.
- **MAIL/RCPT parameters** flow client→server, so an unmodelled one must be
  **expressible**. Adding a struct field is already non-breaking; the real gap is
  the caller who needs a parameter we have not implemented yet.
- **Enhanced status codes** flow server→client and must never be **flattened**.
  A caller matching `5.7.x` policy codes has to see a detail value we have never
  heard of.

```go
// Works today, before the library models the extension.
opts := &smtp.MailOptions{
    Extra: []smtp.Param{{Keyword: "FUTURE-EXT", Value: "1"}},
}
```

The peer must advertise `FUTURE-EXT` before `Mail` writes it. Set
`AllowUnadvertisedParameters` on `smtpclient.MailSendOptions` — the second,
client-side options struct `Mail` and `Rcpt` take — only when independent
knowledge of the peer makes that local check inappropriate. Values using RFC 3461
xtext can be prepared with `smtp.EncodeXtext`; see `examples/extra-parameter`.

That split is itself one of the shapes above: `smtp.MailOptions` is what goes on
the wire and is shared with the server direction, so a flag that only means
something when *sending* lives beside it rather than in it.

When that RFC is implemented later, it adds a typed field. Nothing that already
compiles stops compiling. `context.Context` is the first parameter of every
blocking call, options travel in structs, and protocol failures all surface as
one `*smtp.Error` carrying both the three-digit reply code and the enhanced
code — for the same reason.

One more shape decision belongs in this list because it is invisible until it is
too late: **LMTP returns one reply per recipient where SMTP returns one for the
message.** So the result of submitting content is a per-recipient collection from
the first commit, with SMTP as the single-element case. A type shaped like "the
reply to DATA" cannot be fixed after a freeze.

## Goals

- **Complete.** Every keyword in the IANA SMTP Service Extensions registry,
  tracked in `docs/RFC-COVERAGE.md`.
- **Stable.** v1.0 with a real compatibility promise, enforced in CI by `apidiff`.
- **LMTP as a first-class mode**, not an afterthought — RFC 2033 shapes the
  transaction API from the start.
- **Verified against real servers.** Postfix, Exim, Stalwart, maddy, Mailpit,
  Dovecot LMTP, GreenMail and Apache James under podman — not just against a
  mock. See `docs/INTEROP.md`.
- **Zero dependencies.** Standard library only, test code included. SASL,
  SASLprep and Unicode normalisation are all reachable from the standard library
  plus generated tables, and a `go.sum` entry is a stability liability this
  project does not control.
- **Safe against hostile servers.** Every parser is fuzzed; malformed input
  returns an error, never a panic.
- **Diagnosable without leaking credentials.** An optional `Trace` hook reports
  every command sent and reply received. SASL payloads — the AUTH initial
  response, every continuation, and the server's challenges — are redacted
  before the hook sees them, and that cannot be switched off. Message content
  never passes through it.

## Non-goals

- **MX resolution, MTA-STS, DANE.** The v1.0 client speaks to a caller-supplied
  endpoint and never decides which host to talk to. These are deferred to a
  separate post-v1.0 package (`smtpdeliver`), which the v1.0 API already reserves
  room for — see `docs/ARCHITECTURE.md`. Durable queues, retry scheduling and
  bounce generation are out of scope permanently.
- **MIME composition, DKIM/ARC signing, charset transcoding.** The client
  transmits what it is given. Use dedicated libraries.
- **Mailbox storage, queueing, spam filtering, DKIM verification.** The planned
  server framework (below) defines a backend surface; it does not implement one
  for production.
- **IMAP, POP3, JMAP.**

## Not yet, but scoped

A server framework, milestone M6, after v1.0 of the client. The core types were
split into a shared I/O-free package from the first commit precisely so this can
be added without an API break, and that has held up — `smtp.DataResult` was
shaped in M0 for LMTP's per-recipient replies, and it turns out to be exactly
what an LMTP *server* must produce.

Its design is scoped now rather than after the tag, for one reason: adding types
to the shared package after v1.0 is additive and always allowed, but *reshaping*
one is not — and a vocabulary that has only ever been exercised in the client
direction can contain a type a server can consume but cannot naturally produce.
No client-side review finds that. So the design runs before the freeze, the
implementation after it, and a bidirectional review of the shared vocabulary is a
v1.0 exit criterion.

See `docs/SERVER-DESIGN.md` — **approved**, four revisions, with every RFC
claim it rests on quoted and sourced. No server code exists yet; the
implementation follows the v1.0 tag.

## Documentation

| Document | Contents |
|---|---|
| `docs/tasks/BOARD.md` | **Start here to implement** — task specs, dependency order, file ownership |
| `docs/ARCHITECTURE.md` | Layering, connection model, settled design decisions |
| `docs/API-STABILITY.md` | The rules that make v1.0 possible |
| `docs/RFC-COVERAGE.md` | Keyword → RFC → status, from the IANA registry |
| `docs/INTEROP.md` | Server matrix and how to run it |
| `docs/ROADMAP.md` | Milestones and exit criteria |
| `docs/SERVER-DESIGN.md` | Server framework design — approved, implementation post-v1.0 |
| `CLAUDE.md` | Working rules for AI agents contributing here |

## Testing

The runnable programs under `examples/` cover STARTTLS submission, implicit
TLS, partial recipient rejection, streaming DATA and BDAT, LMTP, DSN, and the
unmodelled-parameter escape hatch. They use placeholder endpoints and
credentials and are compiled by `go test ./...`.

```bash
go test ./...                                          # unit, no network
go test -count=1 -race -tags=interop ./smtpclient      # production client, needs podman
go test -count=1 -race -tags=interop ./interop/...     # harness packages, run after smtpclient
go test -fuzz='^FuzzReplyReader$' ./internal/smtpwire  # parser robustness
```

## License

MIT
