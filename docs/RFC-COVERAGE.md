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

Nothing is implemented yet; every row is `planned`.

## Base — RFC 5321 core and the session extensions

| Capability | RFC | Task | Status |
|---|---|---|---|
| SMTP core (HELO/EHLO/MAIL/RCPT/DATA/RSET/NOOP/QUIT) | 5321 | T01,T02,T03,T05 | planned |
| VRFY | 5321 | T05 | planned [^vrfy] |
| EXPN | 5321 | T05 | planned |
| HELP | 5321 | T05 | planned |
| Message Submission | 6409 | T03 | planned |
| STARTTLS | 3207 | T03 | planned |
| PIPELINING | 2920 | T03 | planned |
| ENHANCEDSTATUSCODES | 2034 | T01,T02 | planned |
| AUTH | 4954 | T04 | planned |
| LMTP (`LHLO`, per-recipient DATA replies) | 2033 | T07 | planned [^lmtp] |

[^vrfy]: The IANA registry cites `draft-ietf-emailcore-rfc5321bis` for `VRFY`.
    That draft is at revision 44, sits in the RFC Editor queue in state
    *Blocked*, and is **not** an RFC. Cite RFC 5321 until it publishes. See the
    watch list.

[^lmtp]: The *command surface* is T07, but the per-recipient result shape is a
    T01/T05 requirement and a `docs/API-STABILITY.md` §8 rule. It cannot be
    retrofitted.

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
| Enhanced status code structure | 3463 | T02 | planned | `class.subject.detail` |
| Enhanced status code registry | 5248 | T02 | planned | grows independently; codes stay open |
| DSN message format | 3464 | T09 | planned | parsing a returned DSN is out of scope; cited by T09 |
| Internationalised DSN | 6533 | T09 | planned | with SMTPUTF8 |
| SMTPUTF8 framework | 6530 | T08 | planned | |
| Internationalised email headers | 6532 | T08 | planned | referenced, not composed |
| Dot-stuffing / transparency | 5321 §4.5.2 | T01 | planned | streaming filter both directions |
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
