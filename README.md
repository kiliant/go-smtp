# go-smtp

ESMTP and LMTP client and server libraries for Go, built so that complete
extension coverage and stable public APIs are compatible goals rather than
competing ones.

```
import "github.com/kiliant/go-smtp/smtpclient"
import "github.com/kiliant/go-smtp/smtpserver"
```

> **Status:** the client module is stable v1.x and its compatibility promise is
> enforced with `apidiff`. The independently versioned server module is v0.x:
> `smtpserver/v0.1.0` is its first release, so its backend contract may still
> change before v1 and every such change will be called out in its changelog.
> The reference server is part of the interoperability matrix; Postfix and Exim
> relay through it, including Postfix LMTP, and the combined 36-target fuzz
> campaign is clean. See `docs/RFC-COVERAGE.md` for capability-level status.

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
- **Mailbox storage, durable queueing, spam filtering, DKIM verification.** The
  server framework defines a backend surface; its `memory` backend is a
  non-durable test/development sink and must not be used in production.
- **IMAP, POP3, JMAP.**

## Server module

`github.com/kiliant/go-smtp/smtpserver` serves RFC 5321 SMTP and RFC 2033 LMTP
from a caller-supplied `net.Listener`. Its backend is a concrete struct of
context-aware function fields, so a future extension adds a field instead of
breaking every external implementation of an interface. `smtpserver/backendtest`
checks third-party backends, while `smtpserver/memory` provides a supported,
non-durable sink for development and application tests.

The server is a nested module on purpose. The root client module is frozen at
v1; the server's harder backend contract starts at v0.1.0 and can mature without
weakening that client promise. Both directions reuse the I/O-free root `smtp`
vocabulary, including LMTP's per-recipient `smtp.DataResult`, while the nested
module depends downward on a released root v1 version. See
`docs/SERVER-DESIGN.md` and `docs/API-STABILITY.md` §4b and §9.

## Documentation

| Document | Contents |
|---|---|
| `docs/tasks/BOARD.md` | **Start here to implement** — task specs, dependency order, file ownership |
| `docs/ARCHITECTURE.md` | Layering, connection model, settled design decisions |
| `docs/API-STABILITY.md` | The rules that make v1.0 possible |
| `docs/RFC-COVERAGE.md` | Keyword → RFC → status, from the IANA registry |
| `docs/INTEROP.md` | Server matrix and how to run it |
| `docs/ROADMAP.md` | Milestones and exit criteria |
| `docs/SERVER-DESIGN.md` | Approved server framework design implemented by `smtpserver` |
| `CLAUDE.md` | Working rules for AI agents contributing here |

## Testing

The runnable client programs under `examples/` cover STARTTLS submission,
implicit TLS, partial recipient rejection, streaming DATA and BDAT, LMTP, DSN,
and the unmodelled-parameter escape hatch. `smtpserver/examples/` adds a minimal
sink, a submission listener, LMTP result construction, a custom five-handler
backend, and a self-contained smtpclient test double. Both sets compile in CI.

```bash
go test ./...                                          # unit, no network
(cd smtpserver && go test ./...)                       # server module + examples
go test -count=1 -race -tags=interop ./smtpclient      # production client, needs podman
go test -count=1 -race -tags=interop ./interop/...     # harness packages, run after smtpclient
go test -fuzz='^FuzzReplyReader$' ./internal/smtpwire  # parser robustness
```

## License

MIT
