# T09 — Extensions group B: delivery control

**Agent:** `extensions` · **Milestone:** M2 · **Depends on:** T05

**Owns:** `smtpclient/ext_b_*.go`

Runs in parallel with T08. No shared files.

## Scope

Extensions that add `MAIL`/`RCPT` parameters influencing how the message is
handled after it is accepted.

| Capability | RFC | Parameters / commands |
|---|---|---|
| DSN | 3461 | `RET=`, `ENVID=` on MAIL; `NOTIFY=`, `ORCPT=` on RCPT |
| DELIVERBY | 2852 | `BY=` |
| FUTURERELEASE | 4865 | `HOLDFOR=`, `HOLDUNTIL=` |
| MT-PRIORITY | 6710 | `MT-PRIORITY=` |
| RRVS | 7293 | `RRVS=` |
| REQUIRETLS | 8689 | `REQUIRETLS` (valueless) |
| LIMITS | 9422 | EHLO keyword parameters only |
| BURL | 4468 | `BURL` command |

`HOLDFOR`/`HOLDUNTIL` are **RFC 4865**. A scrape of the IANA registry during this
repo's setup attributed them to RFC 6729, which is *Indicating Email Handling
States in Trace Fields* and defines no SMTP keyword. Verified against RFC 4865
directly. Do not "correct" it back.

## Deliverables

### DSN (RFC 3461)

The largest item here and the one with the encoding trap.

- `RET=FULL|HDRS` and `ENVID=` on `MAIL`; `NOTIFY=` and `ORCPT=` on `RCPT`.
- `NOTIFY=` takes `NEVER` **alone**, or a comma-separated combination of
  `SUCCESS`, `FAILURE`, `DELAY`. `NEVER` combined with anything else is invalid;
  reject locally.
- **`ENVID=` and `ORCPT=` require `xtext` encoding (RFC 3461 §4).** The encoder
  is T01's; using it is yours. Skipping it yields a `501` only against strict
  servers, so it passes casual testing and fails in production.
- `ORCPT=` carries an address-type prefix, conventionally `rfc822;`.
- Parameter *values* are `string`-backed named types, not enums — RFC 6533 adds
  the internationalised `utf-8;` address type, which is exactly the event an
  exhaustive `switch` does not survive.

Parsing a returned DSN message (RFC 3464) is **out of scope** — that is a MIME
parsing job for a different library. The doc comment should say so, since callers
will ask.

### DELIVERBY (RFC 2852) and FUTURERELEASE (RFC 4865)

Both express time, in different units, with different failure semantics:
`BY=` is a relative deadline in seconds with a mode suffix (`N` notify, `R`
return); `HOLDFOR=` is a relative delay in seconds and `HOLDUNTIL=` an absolute
time. They are mutually exclusive with each other. The EHLO keyword parameters
state server-side limits — respect them locally.

`FUTURERELEASE` is a *submission* extension (RFC 6409), so expect it only on 587.

### MT-PRIORITY (RFC 6710) and RRVS (RFC 7293)

`MT-PRIORITY=` is a signed integer in a server-advertised range with a named
priority-assignment policy in the EHLO parameter. `RRVS=` is a timestamp plus an
optional `;C` or `;R` disposition — a "require recipient valid since" assertion
that guards against a reassigned mailbox.

### REQUIRETLS (RFC 8689)

Valueless MAIL parameter. Two things worth stating in the doc comment because
they surprise callers: it requires TLS on **onward** hops, not just this one, and
a server advertising it commits to that. It also constrains the `NOTIFY=`
values that may accompany it.

This is the closest thing in v1.0 to transport policy, and it is in scope because
it is an esmtp-param, not a decision about which host to talk to. It does **not**
open the door to MTA-STS or DANE — those remain T14.

### LIMITS (RFC 9422)

The newest extension in the registry and EHLO-parameter-only: `RCPTMAX`,
`MAILMAX`, `RCPTDOMAINMAX`. No MAIL/RCPT parameter of its own.

It is the best available test of whether §1a was implemented properly: its
parameters are structured, they were unknown when most clients were written, and
a client that models EHLO keywords as booleans cannot express it at all.

### BURL (RFC 4468)

A command, not a parameter — submits content by reference to an IMAP URL rather
than inline. Requires `IMAP4 URLAUTH` on the far side and is rarely deployed;
implement the command and the reply handling, and expect the coverage row to stay
`done` rather than reaching `verified`.

## Ownership note

Typed `MailOptions`/`RcptOptions` fields live in the root package (T02); the
transaction path is T05's. Missing field → record in `.state/progress/T09.md` and
escalate. Do not edit across the boundary.

## Testing

- Table-driven `xtext` round-trip tests, and a test that a non-`xtext`-encoded
  `ENVID` never reaches the wire.
- `NOTIFY=NEVER` combination rejection.
- `FuzzXtext`, `FuzzParseLimitsParam`.
- Interop: Stalwart and Postfix advertise `DSN`; `LIMITS` and `REQUIRETLS` are
  sparse, so expect skips and record which servers advertise what.

## Done when

Each row reaches `done` in `docs/RFC-COVERAGE.md`, `verified` where two
independent servers advertise it; rows short of two servers carry a footnote
saying so, as `docs/RFC-COVERAGE.md` requires; `api-guardian` has approved the
exported surface.
