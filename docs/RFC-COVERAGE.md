# RFC / extension coverage

Source of truth: the **IANA SMTP Service Extensions registry**
(<https://www.iana.org/assignments/smtp/>), retrieved 2026-08-02.

**Do not add a row from memory.** Several widely cited numbers are wrong:
`8BITMIME` is RFC 6152 (not 1652, which it obsoletes); `UTF8SMTP` (5336) is the
*obsoleted predecessor* of `SMTPUTF8` (6531) and both appear in the registry;
`HOLDFOR`/`HOLDUNTIL` belong to `FUTURERELEASE` (RFC 4865) and were
misattributed to RFC 6729 — *Indicating Email Handling States in Trace Fields*,
which defines no SMTP keyword at all — by a scrape during this repo's setup. If a
keyword is missing here, check the registry, then add it.

Status: `planned` → `in progress` → `done` → `verified` (exercised against at
least two independent servers in the interop matrix).

M0 (T01 wire codec, T02 core types) and T03 (connection, EHLO, TLS and
pipelining) have landed; the rows carrying those task IDs are updated below.
No row is `verified` yet and none can be until the T06 interop matrix exists.

## Base — RFC 5321 core and the session extensions

| Capability | RFC | Task | Status |
|---|---|---|---|
| SMTP core (HELO/EHLO/MAIL/RCPT/DATA/RSET/NOOP/QUIT) | 5321 | T01,T02,T03,T05 | in progress [^core] |
| VRFY | 5321 | T05 | planned [^vrfy] |
| EXPN | 5321 | T05 | planned |
| HELP | 5321 | T05 | planned |
| Message Submission | 6409 | T03 | in progress [^submission] |
| STARTTLS | 3207 | T03 | done |
| PIPELINING | 2920 | T03 | done |
| ENHANCEDSTATUSCODES | 2034 | T01,T02,T03 | done [^esc] |
| AUTH | 4954 | T04 | planned |
| LMTP (`LHLO`, per-recipient DATA replies) | 2033 | T07 | planned [^lmtp] |

[^vrfy]: The IANA registry cites `draft-ietf-emailcore-rfc5321bis` for `VRFY`.
    That draft is at revision 44, sits in the RFC Editor queue in state
    *Blocked*, and is **not** an RFC. Cite RFC 5321 until it publishes. See the
    watch list.

[^lmtp]: The *command surface* is T07, but the per-recipient result shape is a
    T01/T05 requirement and a `docs/API-STABILITY.md` §8 rule. It cannot be
    retrofitted. The shape itself — `smtp.DataResult` as a per-recipient
    collection — landed with T02.

[^core]: T01 through T03 have landed reply framing, EHLO parsing, command and
    esmtp-param encoding, the shared core types, connection establishment,
    greeting/EHLO negotiation and orderly shutdown. T05 still owns the mail
    transaction commands, so the full SMTP core remains `in progress`.

[^submission]: T03 supplies cleartext, STARTTLS and implicit-TLS connection
    entry points suitable for submission endpoints. Authentication and message
    submission commands land with T04 and T05, so end-to-end submission remains
    `in progress`.

[^esc]: The RFC 3463 structure (`smtp.EnhancedCode`) and the syntactic
    extraction of a leading `class.subject.detail` from reply text
    (`smtpwire.ExtractEnhancedCode`) both landed with T01/T02, including the
    §1c requirement that an unparseable code survive verbatim in `Raw`.
    Extraction is deliberately unconditional at the wire layer. T03's session
    layer gates semantic extraction on the server having advertised
    `ENHANCEDSTATUSCODES`; greeting and EHLO failures remain ungated because no
    extension list has been negotiated yet.

## Group A — transport core (task T08)

The extensions that change how message content itself is transmitted.

| Capability | RFC | Status |
|---|---|---|
| SIZE | 1870 | planned |
| 8BITMIME | 6152 | planned |
| SMTPUTF8 | 6531 | planned |
| CHUNKING | 3030 | planned |
| BINARYMIME | 3030 | planned |
| UTF8SMTP | 5336 | planned [^utf8smtp] |

[^utf8smtp]: Obsoleted by RFC 6531. Recognised on the wire for compatibility
    with servers still advertising it; never *sent* as a preference. Do not
    implement its distinct address syntax.

## Group B — delivery control (task T09)

Extensions that add `MAIL`/`RCPT` parameters influencing handling.

| Capability | RFC | Status |
|---|---|---|
| DSN | 3461 | planned |
| DELIVERBY | 2852 | planned |
| FUTURERELEASE | 4865 | planned |
| MT-PRIORITY | 6710 | planned |
| RRVS | 7293 | planned |
| REQUIRETLS | 8689 | planned |
| LIMITS | 9422 | planned |
| BURL | 4468 | planned |

## Group C — legacy & niche (task T10)

Implement the parse path so an EHLO reply advertising these does not break the
client; full command support is best-effort. Several will stay `deferred`.

| Capability | RFC | Status | Note |
|---|---|---|---|
| ETRN | 1985 | planned | queue-start command |
| ATRN | 2645 | planned | authenticated TURN, ODMR |
| NO-SOLICITING | 3865 | planned | `SOLICIT=` parameter |
| MTRK | 3885 | planned | `TRANSID=` parameter |
| SUBMITTER | 4405 | planned | `SUBMITTER=` parameter |
| CONPERM | 4141 | planned | content conversion permission |
| CONNEG | 4141 | planned | content negotiation |
| CHECKPOINT | 1845 | deferred | no known server support |
| VERB | — | deferred | Eric Allman, non-RFC; sendmail verbose mode |
| ONEX | — | deferred | Eric Allman, non-RFC; one-transaction-only |
| SEND | 821 | deferred | removed by RFC 5321; keyword parses, never sent |
| SOML | 821 | deferred | as above |
| SAML | 821 | deferred | as above |
| TURN | 821 | deferred | removed by RFC 5321 for security reasons; superseded by ETRN/ATRN |

A `deferred` row still requires that the keyword parse and reach the caller via
the extension accessor. Deferred means "we do not implement the command", never
"we break on the keyword".

## Not in the registry but required

| Item | RFC | Task | Status | Note |
|---|---|---|---|---|
| ESMTP extension mechanism (historical) | 1869 | T01,T02 | done [^esmtp] | origin of the EHLO extension framework |
| Implicit TLS for submission | 8314 | T03 | done | TLS handshake precedes the server greeting, conventionally on port 465 |
| xtext encoding | 3461 §4 | T01,T02 | done | required for `ENVID=`, `ORCPT=`, `AUTH=`; exported as `smtp.EncodeXtext` [^xtext] |
| Enhanced status code structure | 3463 | T02 | done | `class.subject.detail`; unparseable codes survive in `Raw` |
| Enhanced status code registry | 5248 | T02 | done | grows independently; codes stay open — never switched on exhaustively |
| DSN message format | 3464 | T09 | planned | parsing a returned DSN is out of scope; cited by T09 |
| Internationalised DSN | 6533 | T09 | planned | with SMTPUTF8 |
| SMTPUTF8 framework | 6530 | T08 | planned | |
| Internationalised email headers | 6532 | T08 | planned | referenced, not composed |
| Dot-stuffing / transparency | 5321 §4.5.2 | T01 | done | streaming filter both directions; CRLF-normalising on send [^barelf] |
| Line-ending conformance | 5321 §2.3.8, §4.1.1.4 | T01 | done | no bare CR/LF transmitted; bare-LF terminator rejected on read |
| PLAIN | 4616 | T04 | planned | SASL |
| LOGIN | — | T04 | planned | de-facto, no RFC; still ubiquitous |
| CRAM-MD5 | 2195 | T04 | planned | legacy, still common |
| SCRAM-SHA-1 / SCRAM-SHA-256 | 5802, 7677 | T04 | planned | |
| SCRAM-SHA-\*-PLUS | 5802, 7677 | T04 | planned | channel binding to TLS exporter |
| EXTERNAL | 4422 | T04 | planned | client certificate auth |
| OAUTHBEARER | 7628 | T04 | planned | |
| XOAUTH2 | — | T04 | planned | de-facto, Gmail/Outlook |
| SASLprep | 4013, 3454 | T04 | planned | opt-in; deployed servers compare raw octets |
| NFC/NFKC normalisation | UAX #15 | T04 | planned | generated tables, no `x/text` |

[^xtext]: Exported from `package smtp` deliberately, as a **deliberate twin**
    of `internal/smtpwire.EncodeXtext` rather than a shared implementation.
    Package smtp imports nothing from this module, so it cannot reuse the
    internal one, and the internal one can never be exported. Without an
    exported encoder the `Extra []Param` escape hatch of API-STABILITY.md §1b is
    weaker than §1b claims: a caller sending an unmodelled parameter whose value
    needs xtext would have to reimplement RFC 3461 §4. The two copies are pinned
    to identical golden vectors in their respective tests; changing one without
    the other fails a test.

[^esmtp]: RFC 1869 *SMTP Service Extensions* (STD 10) is where the `EHLO`
    extension framework originates, and `doc.go` cites it. It is **obsolete** —
    obsoleted by RFC 2821, itself obsoleted by RFC 5321 — so cite RFC 5321 §2.2
    for the current text and treat 1869 as historical attribution only. Added
    here because an api-guardian review found it was the one RFC number cited in
    `package smtp` that this file did not carry, and `doc.go` claims every number
    it cites is checked against this file.

[^barelf]: `DotStuffWriter` normalises caller content to CRLF, per RFC 5321
    §2.3.8: *"SMTP client implementations MUST NOT transmit these characters
    except when they are intended as line terminators and then MUST, as
    indicated above, transmit them only as a <CRLF> sequence."* Line starts are
    therefore defined by CRLF alone and every line start is stuffed.

    It previously forwarded bare CR and bare LF unchanged. That was
    non-conforming, and asymmetric in a way that mattered: LF alone *was*
    treated as a line start, so `<LF>.<LF>` was stuffed, but CR alone was not,
    so `<CR>.<CR><LF>` reached the wire with the dot unstuffed — an
    SMTP-smuggling vector against any receiver honouring bare CR as a line
    terminator. Normalisation removes the disagreement rather than picking a
    side of it.

    The receiving half is `DotUnstuffReader`, which stays lenient about
    un-stuffing after a bare LF but rejects a bare-LF *end-of-content marker*
    with `ErrBareLFTerminator`, because RFC 5321 §4.1.1.4 states that
    `<LF>.<LF>` MUST NOT be treated as equivalent to `<CRLF>.<CRLF>`.

    BDAT is unaffected: `CopyBDATChunk` has no transparency layer, so
    BINARYMIME content stays byte-exact.

## Explicitly out of scope for the core client

Tracked here so nobody re-litigates them mid-task. See `ARCHITECTURE.md` and
[T14](tasks/T14-delivery-design.md).

| Item | RFC | Where it goes |
|---|---|---|
| MTA-STS | 8461 | `smtpdeliver`, post-v1.0 |
| DANE for SMTP | 7672 | `smtpdeliver`, post-v1.0, via a caller-supplied DNSSEC-aware resolver |
| SMTP TLS Reporting | 8460 | out of scope |
| MX resolution / multi-address attempts | 5321 §5 | `smtpdeliver`, post-v1.0 |
| DKIM / ARC signing | 6376, 8617 | out of scope — use a dedicated library |
| MIME composition | 2045–2049, 5322 | out of scope — the client transmits what it is given |

## Watch list

- **`draft-ietf-emailcore-rfc5321bis`** — revision 44, RFC Editor queue, state
  *Blocked*, IANA state *IANA OK - Actions Needed*. Intended status Internet
  Standard. When it publishes it obsoletes RFC 5321 and the base citations in
  this file change. It is a **documentation** change, not an API change — the
  wire protocol is not changing under it. Do not implement against the draft.
  Re-check at each milestone.
- New IANA registrations. The registry is the source of truth; a keyword absent
  from this file is a gap in this file, not evidence the keyword does not exist.
