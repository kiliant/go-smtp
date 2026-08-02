# T08 — Extensions group A: transport core

**Agent:** `extensions` · **Milestone:** M2 · **Depends on:** T05

**Owns:** `smtpclient/ext_a_*.go`

Runs in parallel with T09. No shared files.

## Scope

The extensions that change how message content itself is transmitted.

| Capability | RFC | Notes |
|---|---|---|
| SIZE | 1870 | `SIZE=` MAIL parameter; the EHLO keyword's parameter is the server's maximum |
| 8BITMIME | 6152 | `BODY=7BIT` / `BODY=8BITMIME` |
| SMTPUTF8 | 6531 | UTF-8 addresses; valueless MAIL parameter |
| CHUNKING | 3030 | `BDAT` |
| BINARYMIME | 3030 | `BODY=BINARYMIME`, legal only with CHUNKING |
| UTF8SMTP | 5336 | Obsoleted by 6531; **recognise only**, never send |

## Deliverables

### SIZE (RFC 1870)

Two distinct things share the keyword and are routinely conflated:

- the **EHLO parameter** `SIZE 10485760` — the server's maximum message size, or
  `SIZE 0` meaning no stated limit;
- the **MAIL parameter** `SIZE=` — the client's declaration of this message's
  size, which is an estimate and explicitly permitted to be one.

Declaring a size over the server's stated maximum should fail locally with a
useful error rather than after the content has been transmitted. That is the
entire point of the extension.

### 8BITMIME (RFC 6152) and BINARYMIME (RFC 3030)

`BODY=` takes `7BIT`, `8BITMIME` or `BINARYMIME`. Validate the requested value
against what the server advertises and fail locally rather than downgrading
silently — a silent downgrade corrupts 8-bit content, which is worse than a
refusal.

**`BODY=BINARYMIME` is legal only over `BDAT`.** RFC 3030 forbids it with `DATA`.
Enforce that combination locally.

### SMTPUTF8 (RFC 6531)

- Valueless `SMTPUTF8` MAIL parameter.
- UTF-8 in local-part and domain of both paths. The path length figures in RFC
  5321 §4.5.3.1 are **octets, not characters** — a UTF-8 address hits them
  sooner than an ASCII one, and this is a real interop failure, not a theoretical
  one.
- Interacts with `8BITMIME`: RFC 6531 requires the server to also support 8-bit
  transport for the message body.
- `UTF8SMTP` (RFC 5336) is its obsoleted predecessor. Recognise the keyword so a
  server advertising it does not look extensionless; never prefer it, and do not
  implement its distinct address syntax.

### CHUNKING (RFC 3030)

The second content path, alongside `DATA`. Framing is T01's; policy is yours:

- chunk sizing, and a configurable maximum;
- `BDAT <n> LAST` termination, including the zero-length final chunk;
- one reply per chunk, correlated through T03's queue;
- no transparency encoding — a `.` in content is just a `.`, which is precisely
  why `BINARYMIME` requires it;
- failure mid-sequence: the transaction is dead and `RSET` is the only recovery.

`CHUNKING` and `DATA` must produce byte-identical delivered content for the same
input. That equivalence is an M2 exit criterion and belongs in the interop
suite, not just a unit test.

## Ownership note

You own `smtpclient/ext_a_*.go`. Typed `MailOptions`/`RcptOptions` fields live in
the root package (T02) and the transaction path is T05's. If a field is missing,
record the request in `.state/progress/T08.md` and escalate — do not edit across
the boundary.

## Testing

- Scripted server per extension, including the negative cases: `SIZE` exceeded,
  `BODY=BINARYMIME` with `DATA`, `SMTPUTF8` address to a server without it.
- Fuzz targets for anything that parses (`FuzzParseSizeParam`).
- Interop: Stalwart and Postfix advertise most of this; Mailpit deliberately does
  not, which is what makes it useful.

## Done when

Each row reaches `done` in `docs/RFC-COVERAGE.md`, `verified` where two
independent servers advertise it; the CHUNKING/DATA byte-equivalence test passes
against a real server; `api-guardian` has approved any exported symbol added.
