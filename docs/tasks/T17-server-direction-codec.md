# T17 — Server-direction codec

**Agent:** wire-protocol · **Milestone:** M6 · **Depends on:** T15 approved

**Owns:** `internal/smtpwire/**` (from T01), `internal/smtpsasl/**` (from T04).

**This task does not depend on `SERVER-DESIGN.md` §2.** It is the bulk of the
server work and can be specified, built and fuzzed while the backend abstraction
is still under review. It does depend on the document being *approved* — because
an unapproved design is not a commitment to build a server at all.

## File ownership exception, recorded deliberately

`internal/smtpwire/**` is T01's under `BOARD.md`; `internal/smtpsasl/**` is
T04's. That rule exists to make *concurrent* work safe — it is a lock, and both
tasks have finished. A completed task's lock passes to the task that supersedes
it.

The work is also internal-only by construction: if it changes an exported
signature it has done something wrong, and `api_surface_test.go`'s
`TestAPISurfaceNoInternalLeak` plus the `apidiff` gate both say so.

## Part 1 — the mirrors

Each row exists in one direction today. Build the other. Evidence and line
counts in `docs/SERVER-DESIGN.md` §0.

| Have | Build |
|---|---|
| `LineReader.ReadReply` | **command-line decoding**: verb, argument, trailing esmtp-params, with the same length cap and deadline discipline |
| `EncodeCommand`, `EncodeParam` | **reply encoding**: multiline `nnn-` / final `nnn ` framing, all lines sharing one code |
| `ParseEHLOReply` | **EHLO-family command parsing** (`EHLO`/`HELO`/`LHLO` plus the domain or address literal), and **advertisement encoding** |
| `ExtractEnhancedCode` | **enhanced-code formatting** onto reply text |
| `EncodeBDATCommand`, `CopyBDATChunk` | **`BDAT <n> [LAST]` parsing** and reading exactly `n` octets |

**`BDAT` is not a synchronisation point.** RFC 2920 §3.1 makes extension commands
sync points by default, but RFC 3030 §2 requires a server to handle *"additional
BDAT chunks already in the pipeline"*, which puts BDAT in that rule's "unless
otherwise specified" bucket. So the chunk reader must consume exactly `n` octets
and leave whatever follows for the command parser — reading one byte too many or
too few desynchronises the session, which is the same failure class as SMTP
smuggling. A short read is never end-of-chunk; it blocks until the deadline.

The accumulation of chunks into a message, and the failed-BDAT state, are **not
this task's** — they are the framework spool in `SERVER-DESIGN.md` §2a, owned by
T18. This task delivers the framing primitive only.
| `DotStuffWriter` | already covered — `DotUnstuffReader` exists and has no caller yet |

Two rules the reply encoder must enforce, both from RFC 2034 and both verified
against the RFC text in `SERVER-DESIGN.md`'s appendix:

- §4 — the enhanced code's class must agree with the reply code's first digit.
  A mismatch is a **framework error**, not a wire event; the encoder refuses to
  serialise it. What the server does instead is §3 of the design document, and is
  T18's.
- §3 — the code is **omitted** from the initial greeting and from `HELO`/`EHLO`
  replies. An encoder that prefixes unconditionally is wrong on exactly the two
  replies every session begins with.

## Part 2 — path parsing (net-new, both directions)

`address.go` in the root package is **constants only**. There is no parser,
because a client transmits the string it is handed. A server cannot.

Input is attacker-controlled and pre-authentication. Handle, and fuzz:

- the null reverse-path `<>`;
- source routes `<@a.example,@b.example:user@c.example>` — RFC 5321 §4.1.2
  requires a server to **accept** and **ignore** them, not reject them;
- `<Postmaster>` with no domain, and `<Postmaster@domain>` — distinct legal
  forms a naive `strings.Split(s, "@")` mishandles;
- quoted local parts, including embedded `@` and escaped quotes;
- address literals `[192.0.2.1]` and `[IPv6:2001:db8::1]`;
- UTF-8 local parts and domains **when `SMTPUTF8` was declared, and not when it
  was not** — the parser takes the session's SMTPUTF8 state as an input;
- esmtp-params trailing the path, separated correctly from it;
- the `address.go` size constants as **minimums to accept**, with the server's
  own configurable maximum enforced above them.

One accommodation, consistent with the project's standing rule that the library
accommodates conformant-in-practice peers: the strict grammar has no space
between `MAIL FROM:` and `<`, and real clients emit one. Accept both.

## Part 3 — `Received:` generation (net-new)

RFC 5321 §4.4 requires the receiving server to prepend one. This is the only
place the framework touches message content, which is why it is a codec concern
and not a backend one.

The `with` keyword comes from RFC 3848 (*ESMTP and LMTP Transmission Types
Registration*) and is a function of session state, never of configuration:

| Session | `with` |
|---|---|
| HELO, no TLS | `SMTP` |
| EHLO, no TLS, unauthenticated | `ESMTP` |
| EHLO, no TLS, authenticated | `ESMTPA` |
| EHLO, TLS, unauthenticated | `ESMTPS` |
| EHLO, TLS, authenticated | `ESMTPSA` |
| LMTP mode | `LMTP` / `LMTPA` / `LMTPS` / `LMTPSA` correspondingly |

Getting this wrong misreports whether a hop was encrypted or authenticated, which
downstream policy tooling reads.

The `FOR` clause is emitted **only** when the transaction has exactly one
recipient. Emitting it with several discloses one recipient's address to another.
That is a privacy requirement, not a formatting preference.

Add `docs/RFC-COVERAGE.md` rows for RFC 3848 and for RFC 5321 §4.4 trace-field
generation. RFC 3848 is not currently in that file — add it from the IANA
registry, per the standing rule, not from this spec.

## Part 4 — the SASL responder half

`internal/smtpsasl.Mechanism` implements the **initiator** only: `Start()` and
`Next(challenge)`. Build the responder half for PLAIN, LOGIN, CRAM-MD5,
EXTERNAL, OAUTHBEARER, XOAUTH2 and SCRAM-\*.

Not a rewrite — the message formats are shared, the state machine flips
direction. Two things do not flip:

- **SCRAM needs stored key material** (salt, iteration count, StoredKey,
  ServerKey), which only a backend has. The responder takes it as an input; it
  never derives it from a password it does not have.
- **CRAM-MD5 needs the shared secret**, which a backend may hold without being
  able to hand it over. The responder must therefore support *verification by the
  backend* — issue the challenge, surface it and the client's response — not only
  *verification by the framework*. `SERVER-DESIGN.md` §2d calls this
  `Session.ChallengeResponse`; this task provides the mechanism half.
- **Channel binding (`-PLUS`)** stays framework-side, via
  `tls.ConnectionState.ExportKeyingMaterial`.
- **The responder half is not "run the exchange, then ask the backend".** Each
  mechanism has a *credential-verification point*, and for some of them the
  exchange continues afterwards. OAUTHBEARER is the case: RFC 7628 §3.2.2–3.2.3
  require the server to emit a JSON error challenge and then consume a dummy
  `%x01` client response before failing, because SASL forbids carrying
  diagnostics in an unsuccessful outcome. The responder must expose the
  verification point as a distinct step from exchange completion, and must be
  able to carry mechanism-specific failure data into the challenge it emits.
  `SERVER-DESIGN.md` §2d is the normative version.
- **SCRAM's proof verification is a separate step from the key lookup**, and the
  responder must not report success at lookup time. See §2d.
- **The responder must expose the authorization identity separately from the
  authentication identity.** PLAIN and SCRAM both carry an optional `authzid`,
  and collapsing the two into one string is a privilege-escalation bug. The
  framework never decides whether an `authzid` is permitted — it surfaces both
  and the backend decides (§2d).

`internal/saslprep` and `internal/unicodenorm` are direction-free and are reused
unchanged. Do not fork them.

## Limits

`smtpwire.Limits` is client-shaped: three of its four fields
(`MaxReplyLineLength`, `MaxReplyLines`, `MaxReplySize`) bound *reading a reply*,
which a server never does. Add command-side bounds. The package is `internal/`,
so this breaks nobody.

Decide and record whether the type is renamed or split by direction — the name
already collides with `smtpclient.Limits` (RFC 9422), and T16's move of the
latter into `package smtp` makes the collision more visible, not less.

## Fuzzing

Every new entry point gets a `Fuzz*` target, per the standing rule. These parsers
face **unauthenticated remote clients**, which is a larger and more exposed
surface than the client's hostile-server case. The bar is unchanged: no panic, no
hang, no unbounded allocation.

Seed the corpus from real client traffic captured against the interop matrix and
from the published SMTP-smuggling vectors. Not from invention.

Ownership note: `**/*_fuzz_test.go` is T11's under `BOARD.md`. This task **lands**
the targets — a parser committed without one is not finished — and T11 owns them
afterwards, exactly as the board's "created by one task, owned by another" table
already specifies.

## Done when

- Every mirror in Part 1 exists, with tests round-tripping against its
  counterpart: encode a reply, parse it with `ReadReply`, get the same values.
  The round-trip is the strongest available check that the two directions agree.
- The path parser passes a fuzz campaign clean and handles every listed form.
- `Received:` generation is table-driven from session state, with a test per row.
- The SASL responder half passes against the initiator half for every mechanism.
- `internal/` still never appears in an exported signature
  (`TestAPISurfaceNoInternalLeak`).
- `go test ./...` stays fast — no network, no containers.
