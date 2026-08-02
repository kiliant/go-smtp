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
func (c *Client) Mail(ctx context.Context, from string, opts *MailOptions) error
// Bad — DSN, SMTPUTF8, REQUIRETLS and MT-PRIORITY each want another parameter.
func (c *Client) Mail(ctx context.Context, from string, size int64, body string) error
```

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

## Reviewing against this document

The `api-guardian` agent (`.claude/agents/api-guardian.md`) reviews every diff
that touches an exported symbol. Its single question is the one from CLAUDE.md:
*can the next extension be added without breaking this?* It has authority to
reject a functionally correct change.
