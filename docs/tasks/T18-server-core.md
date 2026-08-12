# T18 — Server core: loop, state machine, capabilities, TLS, spool

**Agent:** server-core · **Milestone:** M6 · **Depends on:** T17,
`docs/SERVER-DESIGN.md` §2 approved (it is — revision 4, 2026-08-04)

**Owns:** `smtpserver/**` except the files T19 and T21 own by name
(`smtpserver/{backend,session}.go`, `smtpserver/memory/**`,
`smtpserver/backendtest/**`, `smtpserver/ext_*.go`), plus the
`smtpserver/go.mod` that makes it a module.

**Implementation waits for the v1.0 tag.** That is a milestone condition on the
root module, separate from design approval and not lifted by it — `git tag`
before starting. The spec exists now because writing it is an M5 exit criterion.

This task carries more weight than its title suggests: revision 2 of the design
moved the §2a spool here, and the spool is a lifecycle contract with its own
failure modes, resource bounds and security tests rather than a buffer. §2a's
contract table is the acceptance criterion, not a guideline.

## Part 0 — the nested module

`SERVER-DESIGN.md` §9, approved 2026-08-04: `smtpserver` is a **nested module
with its own `go.mod`, versioned v0.x while the root module is v1.x.** The first
commit of this task creates it. Do not defer this to T23 — a package that spends
its development inside the root module inherits the v1 freeze the moment the root
is tagged, which is the entire failure the exception exists to prevent.

- `smtpserver/go.mod`, module path `github.com/kiliant/go-smtp/smtpserver`,
  requiring the root module at its released version.
- A `go.work` at the repository root for development, **not** `replace`
  directives in `go.mod`. Workspaces exist for exactly this; a committed
  `replace` ships a module nobody outside the repo can build.
- The zero-dependency rule is unchanged in substance: a `go.sum` entry for **our
  own module** is one we control entirely, which is the distinction `CLAUDE.md`'s
  rule rests on. No third-party entry appears, and
  `.github/scripts/check-no-dependencies.sh` must be taught the nested module
  rather than exempted from it. That script is T13's — record the request in
  `.state/progress/T18.md`.

## Part 1 — `Server`, and the construction-time gate

The gate is what makes §2's nil-field design safe, so it is not optional and it
is not a lint. `NewServer` returns an error — the process refuses to start —
naming **every** problem it found, not the first:

| Refuses to start when | Because |
|---|---|
| a required `Backend` or `Session` field is nil | §2's "three objections, answered": the check runs once at startup, so the failure mode is a process that will not boot rather than a request that panics |
| CHUNKING is enabled and any aggregate spool limit is unset | §2a: the alternative is an unbounded default discovered on a full disk |
| the listener is LMTP mode on port 25 | RFC 2033 §5 — *"it MUST NOT be used on the TCP service port 25."* A misconfiguration that silently produces a protocol violation on the most-scanned port on the internet is not a warning-level event |
| `BINARYMIME` is enabled without `CHUNKING` | RFC 3030 §3 — *"The BINARYMIME service extension can only be used with the 'CHUNKING' service extension."* |

`MaxConnections × MaxSpoolBytes` exceeding `MaxTotalSpoolBytes` is **not** an
error. It is the normal case, because not every connection spools at once.

`Backend.NewSession` returning a `*Session` with a nil required handler is the
one case the startup gate cannot catch. It terminates that connection with `421`
and a logged framework error. It never panics.

Every exported entry point takes an options struct from its first commit, even
where empty, and `ctx` is the first parameter of every blocking one
(`API-STABILITY.md` §2, §3). `Server.Shutdown(ctx)` mirrors `net/http`: stop
accepting, let in-flight transactions finish until `ctx` expires, force-close
after.

## Part 2 — the connection loop

**One goroutine per connection.** Read a command, execute it, write the reply,
repeat. No reader goroutine, no command queue, no event loop, no update
protocol — SMTP has no unsolicited server data, and §4 argues at length that
importing the sibling's IMAP machinery here would be complexity with no cause.

Pipelining (RFC 2920 §3.2) is enforced **structurally**, not heuristically:

- respond in the order commands were received;
- buffer replies while more commands remain buffered, but **flush before every
  blocking read**. That single property is what prevents the deadlock §3.1 warns
  clients about, seen from the server side;
- never flush or lose the TCP input buffer — requirement 9, and the reason the
  STARTTLS discard in Part 4 is scoped to the *decoder's prefetch* rather than to
  the socket.

**Sync points need no hand-maintained list.** RFC 2920 §3.1 names the base set
(`EHLO`, `DATA`, `VRFY`, `EXPN`, `TURN`, `QUIT`, `NOOP`) and then makes extension
commands sync points **by default** — *"unless otherwise specified by the
extensions that define the commands"*. So `STARTTLS` and `AUTH` are sync points
by that rule, not by enumeration.

`BDAT` is the exception, and it is the one revision 1 got wrong: RFC 3030 §2
requires a server to accept and discard *"additional BDAT chunks already in the
pipeline"*, which is only meaningful if chunks may be pipelined. After `DATA`'s
`354`, by contrast, the next bytes are **content**; a loop that has consumed them
into a command buffer has desynchronised, which is the SMTP-smuggling failure
class and is tested with those vectors (T22).

## Part 3 — the state machine

States, and the transitions each command is legal in, are a table rather than a
scatter of `if` statements — the client's `state.go` is the precedent. It carries
the LMTP/SMTP mode, TLS state, authentication state, transaction state, and the
**failed-BDAT state** from §2a, which is a real state with its own command
legality and not a flag.

Mode is fixed at listener construction (§1): an LMTP listener answers
`EHLO`/`HELO` with `500` — RFC 2033 §4.1, *"A LMTP server MUST NOT return a
Positive Completion reply code to these commands"* — and an SMTP listener
rejects `LHLO`.

## Part 4 — TLS, and the five-step STARTTLS rule

§4 gives the rule in five steps and each one is load-bearing. Implement it
exactly, because revision 1's phrasing ("assert the input buffer was empty")
would lead an implementer to destroy the handshake:

1. `STARTTLS` is accepted only at a legal sync point, and only when TLS is not
   already active.
2. Plaintext bytes the SMTP decoder has **already prefetched** past the
   `STARTTLS` line are discarded, **and the event is logged as a protocol
   violation** — the injection attempt must be observable, not merely defeated.
   This is the CVE-2011-0411 defence.
3. The handshake runs on the **underlying `net.Conn`**. The plaintext decoder's
   buffer is gone and its reader is never consulted again.
4. Bytes consumed by the TLS layer are never inspected by the plaintext parser.
   No peeking, not even for diagnostics.
5. On success, discard all session knowledge per RFC 3207 §4.2 — EHLO argument,
   negotiated extensions, AUTH state, open transaction — which is
   `Reset(ResetStartTLS)` in §2c's terms.

Implicit TLS listeners are configuration, not a second code path.

## Part 5 — capability descriptors

The `EHLO` response is **computed**, never hand-written. A hand-maintained list
drifts from what the backend actually does, and every drifted entry is an interop
bug that presents to the peer as a server bug.

One descriptor per extension, with the seven fields §3 specifies: `Keyword`
(an `smtp.Extension`), `Params` (computed per session), `RequiresBackend`,
`RequiresTLS` (both directions — `STARTTLS` advertises only *before*),
`RequiresAuth`, `Modes`, and an optional per-session `Available`.

The cases the table exists to get right, each of which gets a test: `STARTTLS`
disappearing after TLS; `AUTH` mechanisms differing before and after TLS and each
appearing only when the `Session` field that can serve it is non-nil (§2d);
`CHUNKING` only when the spool is configured; `BINARYMIME` requiring `CHUNKING`;
`SMTPUTF8` changing what the path parser accepts; the whole table filtered by
`Modes` in LMTP mode.

**The advertisement/options pairing is a gate.** The framework must never
populate a receive-side options field for an extension it did not advertise — a
client sending `RET=HDRS` with no `DSN` advertised gets `501`, and the backend
never sees a field it cannot know is meaningless. Every extension-owned field in
the receive-side options structs declares the capability that activates it, and a
test fails on any field with no declaration.

## Part 6 — enhanced status codes

Two RFC 2034 rules, both framework-enforced, both verified against the RFC text
in the design document's appendix:

- **§3 — placement.** The code prefixes 2xx/4xx/5xx text **except** in the
  initial greeting and in `HELO`/`EHLO` replies. An implementation that prefixes
  unconditionally is wrong on exactly the two replies every session begins with.
- **§4 — class agreement.** *"a 2xx response must incorporate a 2.X.X code"*, and
  so on. A backend returning `550` with `4.7.1` must not reach the wire.

The repair is §3's decision and **not** revision 1's `451 4.3.0`, which silently
converted a permanent rejection into a temporary one and had every conforming
sender retry for days: **the three-digit code is authoritative and the enhanced
code is replaced with the generic subcode of its class** — `2xx → 2.0.0`,
`4xx → 4.0.0`, `5xx → 5.0.0`. So `550` + `4.7.1` goes out as `550 5.0.0`. The
defect is reported through the trace hook and the error log every time.

## Part 7 — the spool (§2a)

Approving §2a meant approving a contract. Implement its table as written:
memory-first up to `MaxSpoolMemoryBytes`; spill into `SpoolDir` with mode `0600`
and `O_EXCL`; **unlink immediately after open** so cleanup survives panic and
`SIGKILL`, with any platform deviation documented; `MaxSpoolBytes` enforced
independently of `SIZE`, because a peer may omit or misstate it; `552 5.3.4` to
the chunk that crosses the bound and `451 4.3.0` on storage failure, never a
disconnect; the final backend call **streams** from memory or file and never
materialises a `[]byte`; `SIZE` counts client octets, so the framework's own
prepended `Received:` is not counted against it.

**The aggregate budget is a `Server`-owned manager, not a per-connection
buffer:** `MaxTotalSpoolMemoryBytes`, `MaxTotalSpoolBytes`,
`MaxConcurrentSpools`. Reservation is **incremental, per accepted chunk** — a
per-transaction reservation at `MAIL` time lets a handful of small messages
exhaust the budget. Aggregate exhaustion is `452 4.3.1` (temporary,
insufficient system storage), deliberately distinct from the per-transaction
`552 5.3.4` (permanent, message too big); telling a peer its message is
permanently too large when the real problem is transient load is a lie that
loses mail. The announced chunk is still consumed before the failure reply — RFC
3030 §2, and resource pressure does not license breaking framing.

These bounds are **server-instance-wide, not process-wide.** Two listeners are
two budgets. Say so in the doc comment; a type that cannot honour a
process-wide claim must not make one.

**The incomplete-reader defence is the framework's, not the backend's.** In
order: call `Session.Data`; ask the tracked reader whether it reached real
end-of-data; if not, **drain to the end-of-data indication under the data
deadline before any further command parsing and before any reply**; apply §2a's
per-entry outcome rule (`2xx` entries replaced with `451 4.3.0`, `4xx`/`5xx`
preserved, because early rejection on the headers alone is *supported* rather
than defective); close the connection if the drain fails, since there is no safe
way to resume a stream whose framing is unresolved.

**The failed-BDAT state** consumes each further chunk's full announced octet
count and **then answers `503`**. Revision 2 said the chunks were consumed and
never said what reply followed — which would stall the reply stream, and the peer
is counting replies against commands (RFC 2920 §3.2), so a missing reply
desynchronises it as surely as a missing byte. `RSET` clears the state; `RSET`,
`QUIT` and `NOOP` are answered normally; everything else gets `503`.
`Session.Data` is never called — the backend sees `Reset(ResetFailed)`.

## Part 8 — limits and timeouts

§8's table is the checklist. Two things it insists on: **pre-authentication
limits are separate and much tighter** — RFC 5321 §4.5.3.2's 5-minute figure is
for an established, well-behaved session and is not the pre-greeting, pre-`EHLO`
or pre-`AUTH` budget — and **defaults are safe rather than permissive-with-a-
note**. The `LineReader` deadline mechanism is reusable; its values are not.

Recipients above the configured cap get `452`, never a disconnect, and the cap is
never below RFC 5321 §4.5.3.1's 100.

## Part 9 — trace hook, and the gate that scans this package

The trace hook takes `smtp.TraceEvent` and `smtp.TraceDirection` — T16 moved
both into `package smtp` precisely so the server would not define a second,
incompatible pair. The redaction guarantee is the same as the client's: no SASL
payload, no message content, ever.

Extend `api_surface_test.go` to scan `smtpserver/` — a data change to its
directory list and to `internalPackageSuffixes`, not a new mechanism. The
existing gates then apply to the server surface unchanged, and
`TestAPISurfaceNoExportedInterfaces` becomes the standing enforcement of §2's
rule that an extension adds a *field*, never a method and never an interface.
That file is T12's; record the boundary crossing in `.state/progress/T18.md`.

## Done when

- `NewServer` refuses to start on every row of Part 1's table, with a test per
  row asserting the error names the actual problem.
- A pipelined command group is answered in order, and a test proves the flush
  happens before the loop blocks — the property, not the timing.
- The five STARTTLS steps each have a test, including a CVE-2011-0411 injection
  asserting both that the bytes are discarded **and** that the violation is
  logged.
- The advertisement is computed: a test drives every `RequiresTLS`,
  `RequiresAuth`, `RequiresBackend`, `Modes` and `Available` combination and
  compares against the descriptor table rather than a golden string.
- No receive-side options field is populated for an unadvertised extension, and
  the declaration test fails on an undeclared field.
- `550` + `4.7.1` emits `550 5.0.0` and reports the defect.
- Every row of §2a's contract table has a test, including the unlink-on-open
  behaviour, the incremental aggregate reservation, and release on all seven
  `Reset` paths plus panic.
- The failed-BDAT state answers `503` after consuming the announced octets.
- `go test ./...` in both modules stays fast: no network, no containers.
