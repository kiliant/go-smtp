# API stability rules

This document is the contract that makes v1.0 possible. It exists because the
failure mode we are avoiding is specific and documented: Go's own `net/smtp`
carries the note *"The smtp package is frozen and is not accepting new
features."* A client that cannot absorb an extension either freezes or breaks its
callers.

Each rule below states the extension pressure it absorbs. If you cannot name the
future extension a design accommodates, the design is not finished.

## 1. The three sets that always grow

They grow in **different directions**, and the direction determines the fix.
Applying the wrong remedy is the common mistake here.

### 1a. EHLO extension keywords — server to client, must be preserved

The server names its extensions in the EHLO reply. The IANA registry has grown
from RFC 821's handful to `LIMITS` (RFC 9422), and will grow again.

**Rule.** `Extension` is a `string`-backed named type, not an enum. Named
constants exist for known keywords; an unknown keyword **and its parameters** are
retained verbatim and remain queryable. Dropping an unrecognised keyword is data
loss — a caller may know about an extension this library does not.

```go
// Good — a new extension is a constant, and unknown ones still reach the caller.
type Extension string
const ExtSize Extension = "SIZE"
func (c *Client) Extension(ext Extension) (params string, ok bool)
```

**Anti-pattern:** a `struct { SupportsSize, SupportsDSN bool }`. It cannot
express `LIMITS` until we implement `LIMITS`, and it silently discards the
keyword's parameters, which for `SIZE`, `AUTH` and `LIMITS` carry the payload.

### 1b. MAIL/RCPT esmtp-params — client to server, must be expressible

Nearly every extension RFC adds a parameter to `MAIL FROM` or `RCPT TO`:
`SIZE=` (1870), `BODY=` (6152), `RET=`/`ENVID=`/`NOTIFY=`/`ORCPT=` (3461),
`AUTH=` (4954), `BY=` (2852), `MT-PRIORITY=` (6710), `RRVS=` (7293),
`REQUIRETLS` (8689), `HOLDFOR=`/`HOLDUNTIL=` (4865), `SOLICIT=` (3865),
`TRANSID=` (3885), `SMTPUTF8` (6531).

This is the SMTP analogue of the IMAP FETCH-item problem, but the failure mode is
**not** additivity. Adding a field to an options struct is already non-breaking,
and these parameters only ever travel outbound, so there is no unknown-data-loss
risk. The failure mode is **expressiveness**: a caller who needs a parameter we
have not implemented yet has no way to send it and must abandon the library.

**Rule.** Typed fields on the options struct for modelled parameters, plus a raw
escape hatch that is part of the API from the first commit:

```go
type MailOptions struct {
    Size       int64          // SIZE=
    Body       BodyType       // BODY=
    RequireTLS bool           // REQUIRETLS
    // ... a new RFC adds a field here. Not a break.

    // Extra carries parameters this library does not model. It is the
    // escape hatch that makes an unimplemented extension usable today.
    Extra []Param
}

type Param struct{ Keyword, Value string } // Value empty ⇒ valueless parameter
```

Parameter **values** are `string`-backed named types, never enums:
`BodyType`, `DSNNotify`, `DSNReturn`, `MTPriority`. New values get registered
without new RFCs — `BODY=BINARYMIME` arrived after `BODY=8BITMIME`, and a
`switch` that was exhaustive stopped being so.

The client must reject a parameter whose extension the server did not advertise
**locally**, before writing, rather than letting the server reply `501`. That
check applies to `Extra` too, unless the caller opts out.

**The opt-out is not a field on the options struct.** It is
`smtpclient.MailSendOptions` / `RcptSendOptions`, for the reason §10 gives:
`MailOptions` and `RcptOptions` are direction-neutral, and "permit a parameter
the *server* did not advertise" is meaningless when the server is the one
reading it. Amended by T16, 2026-08-12.

**Receive direction: three kinds of parameter, not two.** `MailOptions` and
`RcptOptions` are also what a server's parser produces, and a parser must
distinguish:

| | Handling |
|---|---|
| recognised, decoded | parsed into its typed field |
| unknown, syntactically valid | preserved verbatim in `Extra []Param`, handed to the backend, never rejected |
| original spelling | retained where round-trip fidelity matters: keyword case, and the exact `xtext` encoding of a value, which a `Received:` line or a forwarding decision may need to reproduce |

A syntactically **invalid** parameter is not a fourth kind and needs no field: it
is a parse failure, reported as an error like every other malformed input, and a
server turns that error into a `501` naming the parameter.

**Decision (T16, 2026-08-12), by kind of parameter.** The two kinds need
different answers, and an earlier revision of this section gave one answer to
both — caught in api-guardian review, because the answer it gave cannot reach the
case row 3 was written for:

- **Unknown, syntactically valid** — the original spelling is a field on `Param`,
  not a parallel raw slice. A parallel slice would have to be index-matched
  against `Extra` by every consumer, and the association is exactly what must not
  be lost. **T17 is bound by this**; it does not get to revisit this shape.
- **Recognised and decoded** — a `Param` field cannot serve this case at all. A
  recognised parameter is row 1: parsed into its typed field, never entering
  `Extra`, so there is no `Param` to hang a spelling on. And the spelling is
  genuinely unrecoverable from the decoded value, because `EncodeXtext` documents
  that bytes 33–126 other than `+` and `=` pass through unchanged — a peer may
  legally send `+41` where this library emits `A`, and both decode to `"A"`. This
  applies today to `ENVID=` (`DSNMailOptions.EnvelopeID`), `ORCPT=`
  (`DSNRcptOptions.Original`), `AUTH=` (`MailOptions.Auth`) and `SUBMITTER=`
  (`LegacyOptions.Submitter`), and to every future xtext parameter this library
  models with a typed field. The receive parser uses an adjacent
  `Original *Param` field, such as `MailOptions.AuthOriginal`, for that exact
  wire form. Nil means the parameter was absent. The normal send path uses the
  decoded typed field; a relay that intentionally needs byte-for-byte parameter
  forwarding may use the original explicitly. Future decoded xtext parameters
  follow this same additive companion-field pattern; every affected struct
  carries the §7 guard.

Both land with the receive-side parser that populates them, never earlier as an
anticipatory field with no producer.

### 1c. Enhanced status codes — server to client, must not be flattened

RFC 3463 defines the `class.subject.detail` structure; RFC 5248 establishes the
IANA registry, which grows independently of the extension registry. These are two
different citations and both belong in doc comments.

**Rule.** A structured value that keeps the raw text:

```go
type EnhancedCode struct {
    Class, Subject, Detail int
    // Raw is the code exactly as sent, retained when it does not parse.
    Raw string
}
```

Never flatten an unrecognised code to zero or to an "unknown" constant. A caller
matching on `5.7.x` security-and-policy codes must be able to see a detail value
this library has never heard of.

## 2. context.Context from commit one

Every operation that waits for a server reply or transfers caller-sized data
takes `ctx context.Context` as its first parameter.

Adding the context at a blocking boundary later is a breaking change — it is the
most expensive retrofit in Go and a frequent cause of permanent v0.

Cancellation semantics are documented once, centrally: SMTP has no command abort,
so cancelling a command already on the wire invalidates the connection. The
client marks it unusable rather than desynchronising the stream. `RSET` recovers
an aborted *transaction*; it does not recover a half-written `DATA`.

## 3. Options structs, never positional parameters

```go
// Good — a new extension adds a field.
func (c *Client) Mail(ctx context.Context, from string, opts *smtp.MailOptions, send *MailSendOptions) error
// Bad — DSN, SMTPUTF8, REQUIRETLS and MT-PRIORITY each want another parameter.
func (c *Client) Mail(ctx context.Context, from string, size int64, body string) error
```

**Two options structs on one command is legitimate, and `Mail`/`Rcpt` are the
precedent** (T16, 2026-08-12). One carries the wire vocabulary and is shared with
the receive direction (`*smtp.MailOptions`); the other carries this client's
transmission policy (`*MailSendOptions`). They are separate because §10 forbids
direction-specific policy inside shared vocabulary — and because a call whose
only options struct is the shared one has nowhere to put client-only policy once
the surface is frozen. A reviewer should read a second options struct as correct
when the two have different *directions*, and as a smell when they merely have
different *topics*: topics are fields.

A `nil` options pointer must always be valid and mean "defaults". That is what
lets us add *fields to an existing options struct* without breaking callers.

It does **not** rescue a method that shipped without an options parameter.
Adding a parameter to a Go signature breaks every call site regardless of whether
`nil` is accepted for it. Every command entry point therefore takes one **from
its first commit, even when the struct is empty today**.

> **Inherited lesson — read this before arguing the exception.** The sibling
> `go-imap` repository shipped this rule as prose with no mechanical check. Its
> v1.0 freeze audit found **28** exported `Client` methods with no options
> struct, licensed by a sentence in that document wrongly claiming the parameter
> could be added later without a break. That class of mistake is not repairable
> after a freeze. The gate below therefore exists **from T02**, not from the
> hardening milestone.

Enforced by `TestAPISurfaceOptionsStruct` in `api_surface_test.go`, which walks
every exported `Client` command entry point and fails when one has no
`*...Options` parameter. Two escapes exist and both need written justification
here:

1. `optionsExemptClientMethods` — command entry points deliberately shipped
   without options. Empty today; each entry needs a written exception.
2. `nonBlockingClientMethods` — accessors that are not command entry points,
   shared with the context-first gate. A method belongs here only if it neither
   writes to the wire nor waits for a reply: extension and session accessors
   reading cached state, plus `Close`, which matches `io.Closer` and so can never
   take an options parameter.

The second list is the looser one and therefore the easier to abuse: adding a
wire-writing method to it silences the rule-3 gate *and* the context-first gate
at once. Adding an entry there is an API decision, not a test fix.

## 4. Exported interfaces are a liability

Adding a method to an exported interface breaks every external implementer.
Permitted exported interfaces are exactly:

- marker interfaces with an unexported method, which external code cannot
  implement, so they are safe to extend;
- interfaces the standard library already defines (`io.Reader`, `net.Conn`, …).

Everything else is expressed as a struct of function fields. This is what the
dial hook is, and what any future observation hook must be:

```go
type ClientOptions struct {
    // DialContext, when non-nil, establishes the connection.
    DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
    // A new hook is a new field here. Not a break.
}
```

### 4a. Observation hooks take one struct argument

`ClientOptions.Trace func(TraceEvent)` is the first hook added under the rule
above, and it fixes the shape for the rest. Approved by `api-guardian`
2026-08-03.

Two decisions are precedent, not local taste:

- **One struct parameter, never a parameter list.** `func(TraceEvent)` can
  grow a field; `func(TraceDirection, string)` cannot grow anything. The event
  struct carries the §7 guard for exactly this reason, so a later `Code int`
  or timestamp is additive.
- **The direction is an open string type**, for the §1a reason. A caller must
  not be able to write an exhaustive `switch` that a third direction — a
  connection-lifecycle note, say — would silently break.

A second hook is a second field. That is the whole extension story, and it is
why no `Tracer` interface exists.

### 4b. The hard case: a server backend — APPROVED 2026-08-04

The server framework's backend is the worst instance of this rule in the
library, and it is the place the rule most often gets broken elsewhere.
`docs/SERVER-DESIGN.md` §2 proposes an answer that **applies** this rule rather
than amending it, which is worth recording because the sibling `go-imap` reached
the opposite conclusion for its own backend.

- **The proposal is a struct of function fields, in two levels** — `Backend` →
  `Session`, one required field on the first and five on the second, everything
  else nil-means-unadvertised. No exported interface, so
  `TestAPISurfaceNoExportedInterfaces` needs no exemption. The *direction* is
  approved; the contract is at revision 2 and awaiting approval.
- **The reason the sibling's amendment does not transfer** is a property of the
  protocol, not a matter of taste. IMAP's extension pressure lands on the backend
  as *method groups*: nine already-published RFCs each want one, and flattening
  them reaches ~60 nilable fields. SMTP's lands on `MAIL`/`RCPT` parameters, and
  those already have a home in `smtp.MailOptions`/`smtp.RcptOptions`, whose
  growth is governed by §3 and is non-breaking by construction. Counted against
  `RFC-COVERAGE.md`: fifteen implemented extensions need **zero** new backend
  operations; five need one function each.
- **The nil-field cost is closed by a construction-time gate**, not by a promise:
  `NewServer` refuses to start, naming every missing required field. With six
  fields the failure mode is "the process will not start", which is acceptable.
  With sixty it would not be.
- **A required field stays required even when a no-op would do.** `Session.Reset`
  is the case that proves it: revision 1 made it optional on the grounds that the
  framework could "just drop transaction state", which is only true when the
  framework owns that state. A nil field that silently does the wrong thing for
  backends holding their own state is worse than one line of no-op.
- **The rule it establishes:** *a new extension may add a field to `Backend`, to
  `Session`, or to any options struct; it may never change the signature of an
  existing field, and it may never introduce an exported interface.* Enforced by
  extending `api_surface_test.go` to scan `smtpserver/` — a data change to an
  existing gate, not a new mechanism. §3 of this document is the standing record
  of what happens to a rule with no gate.

`SERVER-DESIGN.md` is approved (revision 4, 2026-08-04), so this is settled
design. `smtpserver` code still waits for the v1.0 tag.

**Redaction is behaviour, not shape.** The hook never sees SASL payloads and
there is no opt-out. That is a deliberate security choice rather than an API
constraint: should an un-redacted mode ever be justified, it arrives as an
additive `ClientOptions` field under §3 and breaks nobody. Refusing it today
forecloses nothing, which is why the safe default was chosen without a
compatibility cost.

## 5. A single error type

```go
type Error struct {
    Code     int          // three-digit reply code, e.g. 550
    Enhanced EnhancedCode // RFC 3463 class.subject.detail, zero if absent
    Text     string       // reply text, newline-joined across a multiline reply
    Command  string       // the command that provoked it, e.g. "RCPT"
    Err      error        // optional underlying protocol/transport cause
}
```

Callers match with `errors.As` and compare `Code` or `Enhanced`. No
per-extension error types: an extension adds *codes*, which is a data change.

`Code` is an `int`, not an enum — the 3-digit space is open by construction, and
servers emit codes no RFC lists. Convenience predicates (`IsTransient`,
`IsPermanent`) are methods, not a closed classification type.

**Per-recipient errors are not an exception to this rule.** LMTP and `RCPT`
failures produce a *collection* of `*Error` values keyed by recipient, not a new
error type. See §8.

## 6. internal/ never leaks

The wire codec must not appear in any exported signature — not as a parameter,
return value, embedded field, or opaque handle. The moment it does, the parser is
frozen forever. Enforced by a test that reflects over the public API
(`api_surface_test.go`).

## 7. Struct literal safety

Public structs that callers construct are documented as keyed-literal-only, and
the API-surface test rejects adding a field to a struct that lacks that doc
comment. Public structs that only *we* construct are safe.

## 8. The per-recipient result shape

Not a general Go rule — a protocol-specific one that is unfixable after the
freeze, which is why it is in this document rather than only in the architecture.

LMTP (RFC 2033) returns **one reply per accepted recipient** after the final `.`,
where SMTP returns one reply for the message. Therefore:

> The result of submitting message content is a per-recipient collection from the
> first commit. SMTP is modelled as the single-element case. Never the reverse.

A type named or shaped like "the reply to DATA" is wrong even while LMTP support
is unimplemented, because changing it later changes the return type of the most
frequently called method in the library.

## 9. Reservations for the delivery layer

The scope decision in `ARCHITECTURE.md` defers MX selection, MTA-STS and DANE to
a post-v1.0 package. Three things in the v1.0 surface make that additive, and
they are API commitments, not implementation details:

1. **Connection injection.** A constructor accepting an established `net.Conn`.
2. **Dial address and TLS server identity are separate fields.** Under MX
   selection the certificate is validated against the MX hostname while the
   connection goes to an address chosen from it. Collapsing them into one string
   is a breaking change to fix.
3. **A dial hook** (§4).

Correspondingly prohibited on the v1.0 surface: MX or transport-policy
vocabulary in `package smtp`, DNSSEC anywhere in-tree, and any general
`TLSPolicy`-style interface designed before the delivery layer has a caller for
it. A speculative abstraction on a frozen surface is worse than a missing one —
the missing one can be added.

## 10. Direction-neutral vocabulary lives in `package smtp` — T16, 2026-08-12

`package smtp` is the shared vocabulary for both directions of the protocol. A
type that has only ever been exercised in the client direction can contain a
field a server can consume but cannot naturally produce, and no client-side
review finds it, because the client is the direction that works. `SERVER-DESIGN.md`
§0 ran that review from the other end; T16 executed the result.

**Rule.** Before a type or field is added to either public package, ask which of
three things it is:

| | Goes | Test |
|---|---|---|
| **shared vocabulary** | `package smtp` | both ends can produce it: a client sends it, a server's parser or advertiser produces it |
| **call shape** | `smtpclient` (later `smtpserver`) | it describes how *this* library is invoked, not what is on the wire |
| **direction-specific policy** | the direction's own package | it is meaningless, or means something different, at the other end |

Applied:

- **`Limits`, `ParseLimitsParam` (RFC 9422)** are shared vocabulary and moved to
  `package smtp`. `LIMITS` is an advertisement: a client parses it, a server
  produces it, and a backend must be able to declare its limits without
  `smtpserver` importing `smtpclient`. `Client.Limits()` stays — an accessor over
  negotiated session state is client-side even when its return type is not.
- **`TraceEvent`, `TraceDirection`** moved for the same reason plus a usability
  one: two incompatible `TraceDirection` types in one process is a tax on anyone
  running both halves.
- **`AllowUnadvertisedParameters`** was direction-specific policy inside a shared
  type. It moved to `smtpclient.MailSendOptions` / `RcptSendOptions`. This is why
  `Client.Mail` and `Client.Rcpt` take **two** options structs — the wire
  vocabulary and the client's transmission policy — and it is deliberate: after
  the freeze no client-only field can be added to a `MAIL` or `RCPT` call any
  other way.
- **`Recipient` stays in `smtpclient`.** It is a call shape: the input to
  `RcptBatch`, which exists because of RFC 2920 pipelining. A server receives one
  `RCPT TO` at a time and has no use for a batch.

**Mechanism for a move.** Relocate the definition, leave a type alias behind
(`type Limits = smtp.Limits`), and verify. An alias preserves type identity, so
keyed struct literals and every existing caller keep compiling — the technique
the standard library used for `context.Context`. Aliasing works for constants too
(`const TraceSent = smtp.TraceSent`). A *removal* —
`AllowUnadvertisedParameters` — has no such escape and is exactly why this audit
was an M4 exit criterion rather than post-tag work.

### `apidiff` cannot verify an alias-preserving move — finding, T16, 2026-08-12

T16 was instructed to confirm each move compatible with `apidiff` and to treat a
contrary result as the deliverable. The contrary result happened. Against the
pre-move commit, `apidiff` reports **every** relocated symbol under
*Incompatible changes*:

```text
- ./smtpclient.Limits: changed from Limits to github.com/kiliant/go-smtp.Limits
- ./smtpclient.TraceEvent: changed from TraceEvent to github.com/kiliant/go-smtp.TraceEvent
- ./smtpclient.ClientOptions.Trace: changed from func(TraceEvent) to func(TraceEvent)
```

The third line is the tell: the two signatures are textually identical. `apidiff`
compares two independently loaded copies of the module and treats a type whose
declaring package changed as a different type; it has no way to observe that an
alias makes the two spellings **the same type**. The tool is measuring
declaration provenance, and Go compatibility is decided by type identity.

Consequences, all three binding:

1. **The verification is a compile-time identity assertion, not an `apidiff`
   line.** `smtpclient/alias_compat_test.go` declares
   `var _ Limits = smtp.Limits{}` in both directions for every moved type, plus
   the `ClientOptions.Trace` field shape. Assignment between two *distinct* named
   types is illegal in Go even when their underlying types match, so replacing an
   alias with a redeclared type fails the build. Mutation-checked: turning
   `type Limits = smtp.Limits` into `type Limits smtp.Limits` breaks compilation
   at that line.
2. **Post-v1.0 the CI `apidiff` gate will fail on a correct alias-preserving
   move.** The gate blocks on incompatibilities once a `v1.*` tag exists, and
   this class of change trips it while breaking nobody. The adjudication has one
   objective criterion, and it is a condition rather than a judgement call: such
   a report may be overruled **if and only if** `smtpclient/alias_compat_test.go`
   contains a compile-time identity assertion for every symbol `apidiff` names,
   and that file compiles and passes. No assertion, no overrule — a gate whose
   exception rests on someone's reading is not a gate.
3. `apidiff` remains authoritative for **removals, additions and genuine
   signature changes**, which is most of what it sees. This exception is narrow:
   a symbol that moved between packages under an alias.

## Versioning policy

- **v0.x** until every task in the board's M0–M4 milestones is complete and the
  interoperability acceptance matrix is green. Breaking changes allowed,
  documented in `CHANGELOG.md`.
- **v1.0** freezes the exported API. After it: additive changes only.
- Removal requires a deprecation notice for at least two minor releases, and
  never lands before v2.
- CI runs `golang.org/x/exp/cmd/apidiff` against the previous tag on every PR.
  Post-v1.0 a detected incompatible change fails the build; pre-v1.0 it posts the
  diff so the break is deliberate rather than accidental. Wiring this up is an
  exit criterion of M4, tracked by
  [T13](tasks/T13-release-engineering.md).
- **Go version floor: 1.24**, matching the sibling `go-imap` repository so the
  two can be used together without one forcing a toolchain upgrade. This is
  deliberately one release older than a strict "two most recent majors" policy
  would give. Raising it is a breaking change for callers pinned to an older
  toolchain and needs the same scrutiny as any other entry in this document.

### Exception: `smtpserver` outside the v1 promise — APPROVED 2026-08-04

**Status: approved. Nothing enforces it yet, because no `smtpserver` code exists;
it becomes real with the first `smtpserver/go.mod`.**

The policy above says v1.0 freezes the exported API, without qualifying by
package. Taken literally, `smtpserver` inherits the freeze the moment it lands —
which would freeze the backend abstraction, the hardest API in this library, on
its first commit, before a single third-party backend has been written against
it. That is the failure this project exists to avoid, relocated one layer up.

**The decision is a nested module:** `smtpserver` gets its own `go.mod` and is
versioned v0.x independently while the root module is v1.x.

The alternative — same module, with a carve-out enforced by scoping the `apidiff`
gate — is worse for a reason that has nothing to do with our tooling. An
`apidiff` scope is a gate on *us*; it changes nothing a user sees. Someone
importing a package from a module tagged v1 reasonably expects it not to break,
Go's compatibility guidance is built on that expectation, and no CI configuration
resets it. A doc comment claiming the package is exempt is a promise, which is the
mechanism this document distrusts everywhere else.

Two objections to the nested module do not survive contact:

- *Development ergonomics.* Go workspaces (`go.work`) exist to develop
  interdependent modules in one repository without committing `replace`
  directives.
- *The `go.sum` entry.* The zero-dependency rule exists because a `go.sum` entry
  is a stability liability **we do not control**. A self-referential entry on our
  own module is one we control entirely, so this is a narrow, well-founded
  exception rather than a hole in the policy.

Real remaining costs, accepted: two tags per release, explicit version
coordination, and a deliberate bump of the root-module dependency.

Fallback if the nested module proves unworkable in practice: same-module with a
documented stability exception — needing its own separate approval.

The sibling `go-imap` reached the same decision for `imapserver`. Two libraries
by one author diverging here would be a usability tax on anyone using both.

## Reviewing against this document

The `api-guardian` agent (`.claude/agents/api-guardian.md`) reviews every diff
that touches an exported symbol. Its single question is the one from CLAUDE.md:
*can the next extension be added without breaking this?* It has authority to
reject a functionally correct change.
