# T01 — Wire codec

**Agent:** `wire-protocol` · **Milestone:** M0 · **Depends on:** nothing

**Owns:** `internal/smtpwire/**`

Runs in parallel with T02. Both must land before anything else starts.

## Goal

A total, streaming, hand-written codec for the SMTP wire grammar. This is the
foundation everything else sits on, and the one package that must never appear in
the public API — so it can be rewritten later without breaking anyone.

## Deliverables

### Layering rule: primitives only

**`internal/smtpwire` must not import the root `smtp` package.** It deals in
wire primitives — reply lines, three-digit codes, keywords, parameters, byte
transparency. It knows nothing about `smtp.EnhancedCode`, `smtp.Error` or
per-recipient results.

Semantic assembly (wire triple → `smtp.EnhancedCode`, reply → `*smtp.Error`)
happens in `smtpclient`. This keeps the dependency graph acyclic and keeps T01
and T02 genuinely parallel: neither needs the other's output to compile.

### Reply reader (`reply.go`)

RFC 5321 §4.2 framing. A reply is one or more lines; continuation lines are
`nnn-text`, the final line is `nnn<SP>text`.

- All lines of one reply **must carry the same three-digit code**; a mismatch is
  a protocol error, not something to paper over.
- The code is three digits, first digit `2`–`5`, second `0`–`5`. Reject
  otherwise.
- `nnn` with neither `-` nor space following, and a bare `nnn` with no text, both
  occur in the wild. Decide and document the handling; do not crash.
- Return the code, the joined text, and **the individual lines**. The EHLO parser
  needs the lines; a collapsed string is exactly what makes `net/textproto`
  unusable here.
- Cap the number of continuation lines and the total reply size before
  accumulating.

### Enhanced status code extraction (`enhanced.go`)

RFC 3463 syntax: an optional `class.subject.detail` at the very start of the
reply text. Return the three integers **and** the raw substring, plus the text
with the prefix removed. Never discard the raw form — an unparseable or
unregistered code must survive to the caller (`API-STABILITY.md` §1c).

Extraction is syntactic. Do not condition it on `ENHANCEDSTATUSCODES` here; that
policy belongs to `smtpclient`.

### EHLO reply parser (`ehlo.go`)

The most under-tested parser in most SMTP clients, and the source of a specific
recurring bug:

- **The first line of an EHLO reply is the server's greeting domain, not an
  extension keyword.** A parser that treats it as one silently registers a bogus
  extension named after the hostname.
- Each subsequent line is `keyword` optionally followed by space-separated
  parameters. Keywords are case-insensitive on the wire; normalise to upper case.
- Parameters must be preserved verbatim and in order. `SIZE 10485760`,
  `AUTH PLAIN LOGIN` and `LIMITS RCPTMAX=100` all carry their payload there, and
  dropping it is data loss.
- A keyword the library has never heard of is returned like any other.

### Command encoder (`encoder.go`)

- Command serialisation with the correct `CRLF` termination and no bare CR or LF
  anywhere in an argument.
- `esmtp-param` encoding: `keyword` or `keyword=value`, with the value obeying
  `esmtp-value` (printable ASCII excluding `=` and control characters).
- **`xtext` encoding (RFC 3461 §4)** for parameters that require it — `ENVID=`
  and `ORCPT=`. Characters outside the permitted set become `+XX` hex escapes.
  This is routinely forgotten and produces a `501` only against strict servers.
- Reject locally, before writing, any argument that cannot be encoded. A command
  that would desynchronise the session must never reach the wire.

### Transparency layer (`dotstuff.go`)

RFC 5321 §4.5.2, both directions, as **streaming filters** over `io.Writer` and
`io.Reader` — never a transform over a `[]byte`. A 200 MiB message must not
buffer.

The boundary cases are the whole job:

- a line consisting of exactly `.` → `..`
- a line already beginning `..` → un-stuffed symmetrically on read
- content not ending in `CRLF` — the terminator must still be well formed
- a `.` arriving as the last byte of one `Write` and the newline in the next: the
  filter is stateful across calls, and this is the bug that unit tests written
  against a single `Write` never catch
- bare `LF` or bare `CR` inside content — decide and document; do not silently
  emit a malformed stream

### BDAT framing (`bdat.go`)

RFC 3030. `BDAT <size>` / `BDAT <size> LAST`, with the chunk written as exactly
`size` octets and **no** transparency encoding. One reply per chunk. Chunk sizing
is `smtpclient`'s policy; the framing is yours.

## Hard requirements

1. **Total.** Any byte sequence returns an error. No panic, no unbounded
   allocation, no hang. Includes integer overflow in `BDAT` size arithmetic.
2. **Configurable limits, checked *before* allocating:**
   - max reply line length — default well above RFC 5321's 512 octets, because
     real servers exceed it routinely with long `EHLO` keyword lists; rejecting a
     conformant-in-practice server is a bug and accepting an unbounded line is a
     denial of service
   - max continuation lines per reply
   - max total reply size
   - max `BDAT` chunk size announced by a peer
   A `BDAT 4294967295` must be rejected, not attempted.
3. **Invisible.** Nothing in this package may become reachable from an exported
   signature — checked by T02's `api_surface_test.go`, but design for it now.
4. **Deadline-aware.** The reader supports deadlines on deadline-capable readers.
   A server that sends `354` and then stalls must time out.

## Testing

- Table-driven tests per production, malformed cases included.
- Round-trip: `unstuff(stuff(x)) == x` over the seed corpus, including every
  boundary case above, and with the input split at every possible offset to
  exercise the stateful filter.
- `FuzzReplyReader`, `FuzzEHLOParse`, `FuzzDotStuffRoundTrip`, `FuzzParamEncode`,
  with a seed corpus in `testdata/`. Every bug fixed adds its input to the
  corpus.
- Golden files from real servers once T06 lands — prefer these over hand-written
  examples, because servers do things the RFC does not suggest.

## Done when

Fuzz targets run 5 minutes clean; `go test -race` passes; no exported symbol
references an unexported type in a way that could leak; the limits above are
enforced and unit-tested at their boundaries; the split-write dot-stuffing case
has an explicit regression test.
