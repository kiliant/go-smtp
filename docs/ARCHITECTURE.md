# Architecture

## Layering

```
github.com/kiliant/go-smtp          package smtp
    core vocabulary: reply codes, enhanced status codes, extension keywords,
    MAIL/RCPT parameters, addresses, DSN types, per-recipient results, *Error.
    NO I/O. NO imports of sibling packages.

    ├── internal/smtpwire    reply/command codec, dot-stuffing, BDAT framing
    ├── internal/smtpsasl    SASL mechanisms
    ├── internal/saslprep    SASLprep (RFC 4013) credential preparation
    ├── internal/unicodenorm NFC/NFKC normalisation, generated tables
    └── smtpclient           package smtpclient — the stable v1 client

github.com/kiliant/go-smtp/smtpserver   nested v0.x module
    └── package smtpserver — the server framework
        ├── memory      non-durable in-memory backend (supported for tests/dev)
        └── backendtest conformance suite third-party backends run

Dependency direction across the module boundary:

    smtpserver → package smtp → no sibling packages
```

The nested module imports the released root module and shares this repository's
`internal/smtpwire` and `internal/smtpsasl` implementations under Go's internal
visibility rule. The root module never imports `smtpserver`; releasing or
breaking the server's v0.x API therefore does not move the root module's
compatibility baseline. Development uses `go.work`, with no committed `replace`.

The server adds **no new internal package**. Its codec work — command decoding,
reply encoding, path parsing, `Received:` generation, the SASL responder half —
extends `internal/smtpwire` and `internal/smtpsasl` in place, because those
packages are grammar-level and their existing types are direction-free. This is a
deliberate difference from the sibling `go-imap`, whose semantic codec had to be
lifted out of the client package into a new one; here the semantic assembly that
would have needed lifting is small enough to live in the server.

`internal/unicodenorm` sits below `internal/saslprep` and imports nothing outside
the standard library, so the normalisation tables stay reusable by anything else
that needs them (SMTPUTF8 address comparison or the server framework) without
either package growing a dependency on the client.

Dependencies point downward only. The reason `package smtp` holds the vocabulary
and does no I/O is now demonstrated in both directions: `smtpclient` consumes
server replies and `smtpserver` produces them without importing the client. The
future delivery layer also reuses these types. A per-recipient result type that
lived in `smtpclient` would have required duplication or a backwards import.

## Decision: the vocabulary is bidirectional; the codec is not, and mostly need not be

**Status: settled with the approval of `docs/SERVER-DESIGN.md`, 2026-08-04.**

The layering above delivered what it promised for *types*: `package smtp` is
usable from a server essentially unchanged. `DataResult []RecipientResult` in
particular was shaped in M0 for LMTP's per-recipient replies, and that is exactly
what an LMTP *server* must produce — the decision pays off in a direction nobody
was testing.

It did not deliver it for *codecs*, and the distinction was invisible until the
server was scoped. Three tiers:

- **Direction-agnostic, reusable as-is.** `package smtp` itself; `LineReader`;
  `EncodeXtext`/`DecodeXtext`, which are already both directions; the
  `EnhancedCode` and `Param` value types; `internal/saslprep` and
  `internal/unicodenorm`.

  Including one better-than-expected case: `internal/smtpwire/dotstuff.go`
  contains **both** `DotStuffWriter` and `DotUnstuffReader`, and the latter has no
  caller anywhere in the module. The transparency layer — the highest-risk code
  in an SMTP server — is already complete in both directions, already fuzzed, and
  its `ErrBareLFTerminator` doc comment already takes the correct written
  position on SMTP smuggling.

- **One-directional, needs a mirror.** Reply decoding exists, command decoding
  does not. Command encoding exists, reply encoding does not. EHLO
  *advertisement* parsing exists, EHLO *command* parsing and advertisement
  encoding do not. `BDAT` writing exists, `BDAT` parsing does not. SASL is
  initiator-only. Roughly 700–900 lines, all inside existing `internal/`
  packages.

- **Absent entirely.** **Path parsing** — `address.go` is constants only, because
  a client transmits the string it is handed — and **`Received:` header
  generation** (RFC 5321 §4.4, with the RFC 3848 `with` keywords). A client
  needed neither.

Full analysis in `docs/SERVER-DESIGN.md` §0 and §5; work in T17.

## Decision: the client speaks to a caller-supplied endpoint

**Status: settled by the human, 2026-08-02. Do not revisit without approval.**

The v1.0 client never decides *which host to talk to*. It is handed an address —
or an already-connected `net.Conn` — and speaks ESMTP or LMTP to it. MX
resolution, MTA-STS, DANE and per-destination TLS policy are not in the core
client's API at any version.

This matches what every comparable library does. Go's `net/smtp` takes
`"host:port"` and is frozen; `emersion/go-smtp` is submission/relay-oriented with
no MX resolution; Nodemailer moved MX-based sending out of core into a separate
`nodemailer-direct-transport` package. MX selection needs retry and backoff state
that belongs to a queue rather than a connection, and DANE (RFC 7672) needs a
DNSSEC-validating resolver, which no Go stdlib API exposes trustworthily.

Deferred to `smtpdeliver`, a separate post-v1.0 package (see
[T14](tasks/T14-delivery-design.md)): MX selection, multi-address attempt
sequencing, MTA-STS (RFC 8461), optional DANE (RFC 7672), and structured
retryable outcomes. Permanently out of scope: durable queues, retry scheduling,
bounce generation, full MTA behaviour.

### What that reserves, and what it must not reserve

The reservation is deliberately three concrete things, because a delivery layer
that cannot be built on the client is a broken promise, and a speculative
abstraction designed against no caller is a permanent mistake on a frozen
surface. Both failure modes are real; this is the line between them.

Required from the first commit:

1. **Connection injection.** `net.Conn` in, client out. The delivery layer dials
   its own connections after it has chosen a host, so it needs this door.
2. **Dial address separate from TLS server identity.** They are the same string
   for submission and different strings under MX selection — the certificate is
   validated against the MX hostname, not the connected IP. One field now is one
   breaking change later. Two fields cost nothing.
3. **A dial hook** in the options struct, so a caller can supply its own dialer.

Prohibited until the delivery layer actually exists:

- No DNSSEC in-tree. This is *why* DANE is deferred, not an oversight.
- No general TLS-policy interface. `*tls.Config` plus the address/identity split
  is the entire reservation. Designing `TLSPolicy` against zero callers is how a
  v1.0 surface acquires something it can never remove.
- No MX or policy vocabulary in `package smtp`.

## Decision: no external dependencies

Standard library only, including test code. Rationale: a v1.0 stability promise
cannot be stronger than the weakest dependency's. Everything needed is reachable
with stdlib — `crypto/tls`, `crypto/hmac` + `crypto/sha256` (SCRAM),
`encoding/base64`, `mime` and `net/textproto`-style line handling.

The one case where the stdlib genuinely lacks the primitive is Unicode
normalisation, which SASLprep (RFC 4013) requires and which normally means
`golang.org/x/text`. Resolved by generating the NFC/NFKC tables into
`internal/unicodenorm` as Go source: generated code committed to the tree is not
a dependency, so the rule holds without an exception. The generators live in
`internal/{unicodenorm,saslprep}/gen/` and are run by hand, not at build time —
`go generate` reaching the network during a build would reintroduce exactly the
fragility the rule exists to prevent.

These packages duplicate near-identical code in the sibling `go-imap` repository.
That is intended: the alternative is a `go.sum` entry or reaching into another
module's `internal/`, and the zero-dependency rule outranks DRY across module
boundaries. See CLAUDE.md.

The interop harness shells out to `podman` rather than using a container SDK.

## Connection model

**SMTP is lockstep, so there is no demultiplexer.** This is the sharpest
departure from the sibling IMAP client, and copying that design here would be
wrong: SMTP has no unsolicited server data to route, and every reply belongs to
exactly one issued command, in order.

What the model must absorb instead is three things that are all breaking to
retrofit:

### 1. Pipelining (RFC 2920)

The connection owns a FIFO queue of issued commands and matches replies to it in
order. RFC 2920 §3.1 is normative and quoted here because implementations get it
wrong in exactly these two ways:

> Client SMTP implementations that employ pipelining MUST check ALL statuses
> associated with each command in a group.

> Command statuses MUST be coordinated with responses by counting each separate
> response and correlating that count with the number of commands known to have
> been issued.

> Clients MUST NOT confuse responses to multiple commands with multiline
> responses.

And the sync points — commands that end a pipelined group:

> The EHLO, DATA, VRFY, EXPN, TURN, QUIT, and NOOP commands can only appear as
> the last command in a group since their success or failure produces a change
> of state which the client SMTP must accommodate.

The queue enforces that list structurally. A command marked as a sync point
cannot have another command written behind it until its reply is read. Deadlock
avoidance also requires bounding how much is written before reading: a client
that fills the server's TCP window while the server fills the client's
deadlocks, which RFC 2920 raises directly.

Pipelining is off unless the server advertises `PIPELINING`. The unpipelined path
is the same queue with a depth of one, not a separate code path — two code paths
means the rarely used one rots.

### 2. DATA is two-phase; BDAT is not

`DATA` produces an intermediate `354` reply, then the client streams
dot-stuffed content, then a *second* final reply after `CRLF.CRLF`. Every other
SMTP command produces one reply. `BDAT` (RFC 3030) produces one reply per chunk
and needs no dot-stuffing or transparency, and `BDAT LAST` ends the message.

These are two genuinely different data paths, and both are v1.0 scope. The
transaction API must not be shaped around `DATA` such that `BDAT` becomes a
bolt-on.

### 3. LMTP returns N replies for one DATA

RFC 2033: after the final `.`, an LMTP server sends **one reply per recipient
that was accepted**, not one reply for the message. This is why the transaction
result type is a per-recipient collection from the first commit even though the
LMTP command surface (`LHLO`) lands later in [T07](tasks/T07-lmtp.md).

Any type shaped as "the reply to DATA" is wrong and cannot be fixed after v1.0.
An SMTP `DATA` is modelled as the one-recipient-collection case, not the other
way round. This is the single most important shape decision in T01 and T05.

### Cancellation and failure

SMTP has no command abort. Cancelling an in-flight command therefore poisons the
connection rather than desynchronising the stream; the client closes it and
reports `context.Canceled`. `RSET` recovers an *aborted transaction*, but not a
half-written `DATA` — once content is on the wire the only clean exits are
finishing it or closing.

`421` may arrive at any point, including where a reply to a pipelined command was
expected. The reader treats it as a connection-terminating condition, surfaces it
as a `*smtp.Error`, and marks the connection unusable.

### Timeouts

RFC 5321 §4.5.3.2 specifies per-stage minimums, and the client honours them as
defaults because a single connection-wide deadline is wrong at both ends — too
short for `DATA` termination, absurdly long for `RCPT`:

| Stage | Minimum |
|---|---|
| Initial `220` greeting | 5 minutes |
| `MAIL` | 5 minutes |
| `RCPT` | 5 minutes |
| `DATA` initiation (`354`) | 2 minutes |
| Data block | 3 minutes |
| `DATA` termination (after `.`) | 10 minutes |

Every production network read observes a deadline. A server that accepts `DATA`
and then stalls must time out.

## Parser

Hand-written, over a byte-level line reader. Not `net/textproto`: its
`ReadResponse` collapses a multiline reply into a single string and discards the
per-line structure, and it has no notion of enhanced status codes, `BDAT`
framing, or the dot-stuffing transparency layer. The grammar is small enough that
owning it costs little and buys total control over malformed input.

Requirements:

- **Reply framing.** `nnn-text` continuation lines followed by one `nnn text`
  final line. All lines in one reply must carry the same code. The multiline form
  is where "responses to multiple commands" get confused with one reply — see
  RFC 2920 above.
- **Enhanced status codes** (RFC 3463 structure, RFC 5248 registry) parsed off
  the front of reply text when `ENHANCEDSTATUSCODES` is in play, and preserved
  raw when unparseable rather than discarded.
- **Transparency.** Dot-stuffing on send and un-stuffing on receive per RFC 5321
  §4.5.2, including the boundary cases: a line that is exactly `.`, a message not
  ending in CRLF, and a `.` split across a write boundary.
- **Streaming.** A 200 MiB message must not buffer. Message content is written
  through an `io.Writer` and read through an `io.Reader`; the transparency layer
  is a streaming filter, not a transform over a `[]byte`.
- **Total.** Any byte sequence a hostile server can send returns an error without
  a panic; the public client boundary surfaces protocol failures as `*smtp.Error`.
  Enforced by fuzzing ([T11](tasks/T11-fuzzing-hardening.md)).

### Limits

RFC 5321 §4.5.3.1 states the figures below. Read them in the right direction:
they are minimums a *server* must accept, so the client must tolerate servers
that exceed them, while enforcing its own configurable caps before allocating.

| Element | RFC 5321 figure |
|---|---|
| Local-part | 64 octets |
| Domain | 255 octets |
| Path (reverse-path / forward-path) | 256 octets |
| Command line, including CRLF | 512 octets |
| Reply line, including CRLF | 512 octets |
| Text line, including CRLF | 1000 octets |
| Message content | theoretically unlimited |
| Recipients | theoretically unlimited; `452` when exceeded |

Real servers exceed the 512-octet reply line routinely — a long `EHLO` keyword
list is the common case. The client's own reply-line cap therefore defaults well
above 512 and is configurable; rejecting a conformant-in-practice server is a bug,
and accepting an unbounded line is a denial-of-service.

## Address and content encoding

- `SMTPUTF8` (RFC 6531) permits UTF-8 in `MAIL FROM`/`RCPT TO` addresses and
  changes what may appear on the wire. Handled in the codec, not by callers.
  `UTF8SMTP` (RFC 5336) is its obsoleted predecessor and is recognised for
  compatibility only.
- `8BITMIME` (RFC 6152) and `BINARYMIME` (RFC 3030) select the `BODY=` value; the
  client validates the combination it is asked for against what the server
  advertises rather than silently downgrading.
- The library does **not** compose MIME messages, transcode content, or sign with
  DKIM/ARC. It transmits what it is given. See README non-goals.

## Milestones

| Milestone | Content | Exit criterion |
|---|---|---|
| M0 | wire codec, core types | fuzz targets green, no network/session layer |
| M1 | connection, EHLO/TLS, auth, mail transaction, interop harness | Postfix interop green |
| M2 | LMTP, extension groups A+B | M2 acceptance matrix green |
| M3 | extension group C — full IANA coverage | no `planned` rows outside the deferred set |
| M4 | fuzzing, API review, docs, release engineering, bidirectional vocabulary audit (T16) | apidiff gate active; `package smtp` reviewed from the server direction |
| M5 | server *design* (T15) — runs before the tag | `docs/SERVER-DESIGN.md` approved |
| **v1.0** | **API freeze** | |
| M6 | delivery layer (T14), server framework implementation (T17–T23) | reference server joins the interop matrix; real MTAs relay through it |
