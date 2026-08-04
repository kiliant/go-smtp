# Server framework — design

**Status: APPROVED, revision 4.** Approved by the human 2026-08-04 after three
review rounds.

`smtpserver` **implementation** remains gated on the v1.0 tag — that was always a
separate condition from design approval, and it is unchanged (`ROADMAP.md` M6).
What approval lifts is the design gate: T18–T23 specs may now be written, and
T16 and T17 may proceed per the dependency graph.

### Approval state

| Section | State |
|---|---|
| §6 — the reference backend ships supported | **approved** 2026-08-04 |
| §8 — resource limits and the named vulnerability classes | **approved** 2026-08-04 |
| §9 — `smtpserver` as a nested v0.x module | **approved** 2026-08-04 |
| §0 — one options type for both directions, recommendation (a) | **approved** 2026-08-04, with the qualification in §0 |
| §2 — two-level function-field structure | **approved** 2026-08-04 |
| §2a — framework-owned BDAT spool strategy | **approved** 2026-08-04 |
| §2b — exact SMTP/LMTP result cardinality | **approved** 2026-08-04 |
| §2c — mandatory `Reset` with reasons | **approved** 2026-08-04 |
| §2d — session-scoped auth, three-identity model | **approved** 2026-08-04 |
| §4 — concurrency and STARTTLS model | **approved** 2026-08-04 |
| §2a incomplete-reader defence, §2a aggregate budget, §2d verification point | **approved** 2026-08-04 |
| §2d `CommitAuth`, §2a per-entry early-return rule | **approved** 2026-08-04 |
| **§2 as a whole** | **approved 2026-08-04** |

Every section is approved. The architecture was settled at revision 2 and did not
change after it; revisions 3 and 4 completed contracts at the three boundaries
where SMTP stops being simple — BDAT, AUTH, and transaction teardown.

### Revision 4 (2026-08-04) — what changed and why

Revision 3's review accepted all three contract completions and found one real
gap left, plus three stale or under-specified statements.

1. **Successful authentication was never committed to backend session state.**
   Revision 3 moved authentication onto `Session` so `Mail`, `Rcpt` and `Data`
   could see the principal, and then correctly forbade `SCRAMCredentials` from
   mutating the backend closure — without supplying any later moment at which it
   safely could. Framework state and backend state could therefore disagree for
   exactly the mechanism most likely to front a real user database. §2d adds
   **`CommitAuth`**, one uniform post-success transition for every mechanism, and
   states the general rule the gap exposed: *verification callbacks validate,
   they do not commit.*
2. **The early-return rule was phrased over a whole result**, which leaves an
   LMTP `DataResult` carrying `250`, `550` and `451` at once undefined. §2a now
   states it **per entry** — `2xx` replaced with `451 4.3.0`, `4xx`/`5xx`
   preserved — with a refinement noted there: a result with nothing replaced is
   authoritative and gets `ResetCompleted`, not `ResetFailed`.
3. **Two stale statements.** The revision-3 summary claimed a construction-time
   check on the per-connection product that the normative text explicitly says is
   *not* an error; and the spool manager was called server-scoped while its
   limits were called process-wide, which a `Server`-owned manager cannot honour.
   Both fixed, and the limits are now **server-instance-wide** by decision rather
   than by accident.
4. **`backendtest` still carried revision 2's "`Data` consumes or discards its
   reader"**, which contradicts the early-rejection contract it was meant to
   protect. Replaced with the six checks that verify the framework's side.

### Revision 3 (2026-08-04)

Revision 2's review approved the abstraction and held §2 for three contract
completions. All three are sustained, and one of the smaller corrections turned
out to have a firmer answer in the RFC text than the correction assumed.

1. **`Data`'s "MUST consume or discard" was a backend obligation with no
   framework defence.** A backend that reads the headers, rejects, and returns
   leaves message bytes in the socket reader — and the framework then parses them
   as commands. That is a framing failure in the smuggling family. §2a gains
   **the incomplete-reader defence**: a tracked reader, a mandatory drain before
   any further command parsing, and a defined outcome. Along the way this makes
   *deliberate early rejection* a supported pattern rather than a defect, which is
   a better answer than revision 2 had and better than "treat early return as a
   defect" — see the refinement noted there.
2. **AUTH assumed every mechanism finishes before verification.** It does not:
   RFC 7628 §3.2.2–3.2.3 requires OAUTHBEARER to send a JSON error challenge, take
   a dummy `%x01` response, and only then fail. §2d now places the backend call at
   the mechanism's **credential-verification point**, keeps exchange completion
   framework-owned, and adds a typed failure that can carry the mechanism's own
   error data without a new error type. SCRAM gains a post-proof result, so
   returning stored keys can no longer be mistaken for authentication.
3. **The spool was bounded per connection but not per server.** §2a gains an
   **aggregate budget** — a shared spool manager with server-instance-wide
   memory, byte and concurrency limits, incremental reservation, and a
   construction-time check requiring the aggregate limits to be set explicitly
   whenever CHUNKING is enabled.

Three smaller corrections:

- **The return matrix said "non-empty `DataResult`"; the cardinality rule allows
  `N == 0`.** Fixed to "cardinality-valid". But the zero case is **unreachable**
  rather than a case to define: RFC 2033 §4.2 requires *"when there have been no
  successful RCPT commands in the mail transaction, the DATA command MUST fail
  with a 503 reply code"*, and RFC 5321 §3.3 permits the same for SMTP. §2b now
  states the gate and the guarantee it buys a backend.
- **`ResetCompleted` depended on a successful socket write.** It must not: a peer
  that disconnects during the final `250` still had its message delivered. §2c
  redefines it around *obtaining and attempting to emit* the result, and states
  that `ResetSessionEnd` fires only when a transaction is still open at teardown.
- **The failed-BDAT state consumed chunks without saying what reply each gets.**
  §2a now says: `503`, after the full announced octet count is consumed.

### Revision 2 (2026-08-04) — what changed and why

Revision 1's review found that the design was correct where SMTP is simple and
incomplete at three boundaries where it is not: **BDAT, AUTH, and transaction
teardown.** That framing was right, and every one of the five blockers is
sustained.

1. **BDAT did not fit the claimed execution model** — the single biggest hole.
   Revision 1 promised one goroutine per connection, one continuous `io.Reader`
   for the backend, and a reply per chunk. Those three cannot hold together
   without a mechanism revision 1 never named. §2a now specifies a
   **framework-owned bounded spool**, with the full lifecycle contract, and
   records the two rejected alternatives.
2. **Prompt disconnect cancellation was not implementable.** A goroutine inside a
   backend handler is not reading the socket and cannot observe EOF. §4 now
   states the honest contract and stops promising what the architecture cannot
   provide.
3. **Authentication was attached to the wrong level.** `Backend.Authenticate` is
   global; the authenticated principal belongs to one session. §2d moves it to
   `Session`, adds `AuthResult`, and specifies the identity model the sketch
   never had — including CRAM-MD5, which the revision 1 sketch could not serve at
   all.
4. **`Reset` could not safely be optional.** If `Mail` or `Rcpt` allocated backend
   state, "the framework drops transaction state" does not reach it. §2c makes
   cleanup required, adds `ResetReason`, and enumerates all seven abandonment
   paths.
5. **`DataResult` semantics were ambiguous for SMTP.** Revision 1 said a backend
   "may return one entry" and never defined collapsing. §2b makes cardinality
   exact in both modes and removes the idea of a collapsing algorithm entirely.

Two corrections that are not blockers but were wrong:

- **The STARTTLS rule was too absolute**, and the RFC 2920/RFC 3207 relationship
  was mis-framed as a contradiction. RFC 2920 §3.1 already makes extension
  commands synchronisation points by default; RFC 3207 §4 confirms it for
  STARTTLS specifically. The two agree. §4 is rewritten.
- **The BDAT "smaller size" security test was wrong.** Bytes following an exact
  chunk are legally the next pipelined command. §7 replaces it with the four real
  cases.

And one correction the review's RFC reading surfaced in this document's own text:
**revision 1 listed `BDAT` as a synchronisation point. It is not** — RFC 3030 §2
explicitly requires a server to be prepared for "additional BDAT chunks already
in the pipeline", which makes BDAT one of RFC 2920 §3.1's "unless otherwise
specified" cases. Fixed in §4.

Also revised: §3's enhanced-code mismatch policy, which turned a permanent
rejection into a retryable one and so changed delivery semantics while trying to
report a defect.

### On the divergence from the sibling

This document deliberately diverges from `go-imap`'s `SERVER-DESIGN.md` in one
major decision — §2 — and the divergence is argued rather than assumed. Two
libraries with the same author and the same rules should not reach different
answers by accident; they may reach different answers because the protocols
differ, and here they do.

---

## 0. What already exists, and which direction it faces

The layering was built to make this addition cheap. It mostly did, and where it
did not, the gaps are small and named below. Every row in the tables here was
checked against the code on disk, not inferred from the architecture document.

### Reusable as-is

| Component | Why it is direction-agnostic |
|---|---|
| `package smtp` — `Extension`, `Param`, `EnhancedCode`, `Error`, `RecipientResult`, `RcptResult`, `DataResult`, `BodyType`, `DSNNotify`/`DSNReturn`/`MTPriority`/`ORcptAddressType`, the path-size constants, `EncodeXtext` | pure data, no I/O, no import of any sibling package |
| `internal/smtpwire.LineReader` | a byte-level line reader with a length cap and a deadline hook. Nothing in it knows a reply from a command |
| `internal/smtpwire.EncodeXtext` / `DecodeXtext` | **already both directions** |
| `internal/smtpwire.EnhancedCode`, `Param` | grammar-level value types |
| `internal/saslprep`, `internal/unicodenorm` | credential preparation, direction-free |

### The finding that matters most: transparency is already bidirectional

`internal/smtpwire/dotstuff.go` contains **both** `DotStuffWriter` (send) and
`DotUnstuffReader` (receive). `DotUnstuffReader` has **no caller anywhere in the
module** outside its own tests — the client never receives message content. It
was written ahead of a consumer, and the consumer is this framework.

It matters more than "one type we did not have to write", because of what it
already decides. `ErrBareLFTerminator`'s doc comment takes the correct and
security-relevant position on **SMTP smuggling** in writing:

> RFC 5321 §4.1.1.4 is explicit that the sequence `<LF>.<LF>` MUST NOT be
> treated as equivalent to `<CRLF>.<CRLF>` as the end of mail data indication,
> so this cannot be accepted as a terminator. Nor can it be treated as content:
> an implementation that disagreed would end the message here, so continuing
> past it is precisely the SMTP-smuggling desynchronisation the rule exists to
> prevent. It is an error.

That is the single highest-consequence decision in an SMTP server's data path
(§8), it is already made, it is already fuzzed, and the server inherits it rather
than re-litigating it. `ErrMalformedTerminator` covers the `<CR>` variant.

### One-directional — needs a mirror

| Exists (client direction) | The server needs |
|---|---|
| `LineReader.ReadReply` — decode a multiline reply | **decode a command line**: verb, argument, esmtp-params |
| `EncodeCommand`, `EncodeParam` | **encode a reply**: multiline `nnn-`/`nnn ` framing, enhanced-code prefixing |
| `ParseEHLOReply` — parse the advertisement | **parse the EHLO/HELO/LHLO command**, and **encode the advertisement** |
| `ExtractEnhancedCode` — strip a code off reply text | **format** a code onto reply text, per RFC 2034 §3/§4 (below) |
| `EncodeBDATCommand`, `CopyBDATChunk` — announce and write a chunk | **parse `BDAT <n> [LAST]`** and read exactly `n` octets |
| `DotStuffWriter` | — already covered by `DotUnstuffReader` |

Roughly 700–900 lines, all inside `internal/smtpwire`, all internal, and **none
of it depends on the backend abstraction**. It is specifiable and buildable while
§2 is still under review, and it is the bulk of the codec work. That is T17.

### Absent entirely — in either direction

Two subsystems are not mirrors of anything, because a client never needed them:

1. **Reverse-path / forward-path parsing.** `address.go` in the root package is
   *constants only* — RFC 5321 §4.5.3.1's size figures, with a doc comment
   explaining they are minimums a receiving server must accept. There is no
   parser, because the client transmits the string it is handed. A server must
   parse `MAIL FROM:<...>` and `RCPT TO:<...>`, which means: the null
   reverse-path `<>`; the `<@a,@b:user@d>` source-route form, which RFC 5321
   §4.1.2 requires a server to *accept* and *ignore*; `<Postmaster>` and
   `<Postmaster@domain>`; quoted local parts; address literals `[192.0.2.1]` and
   `[IPv6:...]`; and UTF-8 local parts and domains under SMTPUTF8.
2. **`Received:` header generation.** RFC 5321 §4.4 requires the receiving server
   to prepend one. It needs the RFC 3848 `with` protocol keywords — `SMTP`,
   `ESMTP`, `ESMTPA`, `ESMTPS`, `ESMTPSA`, and for LMTP mode `LMTP`, `LMTPA`,
   `LMTPS`, `LMTPSA` — chosen from the actual session state, plus the `FOR`
   clause rules and a correctly formatted date. This is the **only** place the
   framework touches message content, and getting the keyword wrong misreports
   whether a hop was authenticated or encrypted, which downstream policy engines
   read.

Both are net-new. Neither depends on §2. Both belong to T17.

### The vocabulary findings — this is what T16 exists to fix

`package smtp` is in far better shape than the sibling's equivalent was: the
result types, the error type, the enhanced code, the parameter escape hatch and
the extension keyword type are all already in the shared, I/O-free package.
`DataResult []RecipientResult` in particular was shaped in M0 for LMTP's
"one reply per recipient after the final dot", and that is *exactly* the shape an
LMTP server framework must produce. The M0 decision pays off a second time, in a
direction nobody was testing.

Three concrete defects remain, and all three are cheap now and impossible after
the freeze:

1. **`smtpclient.Limits` and `smtpclient.ParseLimitsParam` are in the wrong
   package.** RFC 9422 `LIMITS` is an *advertisement*: the client parses it, and a
   server must **produce** it. A backend cannot declare its limits without
   `smtpserver` importing `smtpclient`, which inverts the dependency graph the
   layering exists to protect.
   **Verdict: move to `package smtp`, leaving `type Limits = smtp.Limits` and a
   forwarding `ParseLimitsParam` behind.** A type alias preserves type identity,
   so every caller and every keyed literal keeps compiling; `apidiff` should
   report it compatible, and T16 verifies that rather than assuming it.

2. **`MailOptions.AllowUnadvertisedParameters` and
   `RcptOptions.AllowUnadvertisedParameters` are client-only fields.** Their
   documented meaning is "permit `Extra` parameters even when *the server* did
   not advertise their extension keyword". A server receiving `MAIL FROM` has no
   such concept — it *is* the advertiser. If a server-side parser fills a
   `*smtp.MailOptions`, one of its fields is permanently meaningless in that
   direction, which is evidence the shared type has the wrong boundary.
   **Verdict: decided — the field moves to a client-side validation options
   type; the vocabulary structs are reused in both directions.** See below. The
   move must land before v1.0.

3. **`smtpclient.Recipient`, `smtpclient.TraceEvent` and
   `smtpclient.TraceDirection` are shared-observability shapes in a
   direction-specific package.** A server wants a trace hook for exactly the
   reasons `API-STABILITY.md` §4a gives, and it wants the same redaction
   guarantee. Duplicating the type gives callers two incompatible
   `TraceDirection`s. **Verdict: candidates for the same alias-preserving move.
   Lower stakes than (1) because the server could define its own, but a caller
   running both halves in one process should not have to translate.**

### The options-struct direction question — DECIDED 2026-08-04

**Decision: recommendation (a). `smtp.MailOptions` and `smtp.RcptOptions` are
reused in both directions. `AllowUnadvertisedParameters` moves out of them into a
client-side validation options type. Receive-side parse failures stay out of band
as errors.** Approved with the qualification recorded at the end of this section.

The reasoning below is retained because it is what the decision rests on.

`smtp.MailOptions` today means *"the parameters this client will send"*. A server
needs *"the parameters this client sent"*. Those are close enough to tempt
reuse and different enough to be wrong:

| | Client (send) | Server (receive) |
|---|---|---|
| `Extra []Param` | parameters we chose not to model | parameters we did not recognise — **must be preserved verbatim**, `API-STABILITY.md` §1b |
| `AllowUnadvertisedParameters` | a local validation opt-out | meaningless |
| `Size *int64` | what we declare | what the client declared, to check against our own limit |
| a parameter with a syntactically invalid value | cannot arise; we construct it | **must arise**, and must produce a `501` naming the parameter |

The last row is the deciding one. A receive-side struct has a failure mode the
send-side struct does not have, and no field to express it. Two candidate
answers, both viable, neither free:

- **(a) Reuse `smtp.MailOptions`/`smtp.RcptOptions` for both directions**, drop
  or relocate `AllowUnadvertisedParameters`, and report parse failures out of
  band as an `*smtp.Error`. Cheapest; keeps one vocabulary; costs one field move
  that is breaking after v1.0 and free before it.
- **(b) Separate `smtp.MailParams`/`smtp.RcptParams` receive-side types.**
  Honest, and each direction grows independently — but every future extension
  then has to add its field twice, forever, which is precisely the doubling cost
  the sibling repo rejected for its search criteria.

The doubling cost in (b) is paid on every one of the seventeen `MAIL`/`RCPT`
parameters already implemented and on every one still to come; the cost of (a) is
one field, moved once, before the freeze.

**The "invalid parameter value" row does not argue for (b), and revision 1
overweighted it.** A syntactically invalid parameter is a *parse failure*, and a
parse failure is not a value that belongs inside a successfully parsed struct. It
is reported as an error — the same way every other malformed input in this
library is reported — and the options struct is simply never produced. Once that
is stated plainly, the last argument for duplicating every extension field
forever disappears.

**`AllowUnadvertisedParameters` moves out rather than being deleted.** It is
policy governing what *this client permits itself to transmit*, not SMTP wire
vocabulary, and it never belonged in a vocabulary struct in either direction.
T16 relocates it to a client-side validation options type; the client keeps the
behaviour, the shared struct loses a field that was always in the wrong place.

**Qualification — three kinds of parameter, not two.** The receive side must
distinguish:

| | Meaning |
|---|---|
| **recognised, decoded** | a parameter this library models, parsed into its typed field |
| **unknown, syntactically valid** | preserved verbatim in `Extra []Param`, per §1b — the server hands it to the backend rather than rejecting it, because rejecting an unmodelled-but-legal parameter is the failure mode §1b exists to prevent |
| **original spelling** | retained where round-trip fidelity matters — keyword case, and the exact `xtext` encoding of a value, which a `Received:` line or a forwarding decision may need to reproduce |

The third is the one a naive implementation loses, and it is lost silently. T16
decides whether that is a field on `Param` or a parallel raw slice; either is
additive to `Param`, which already documents that its field set may grow.

---

## 1. Protocol baseline

### The wire targets

RFC 5321 SMTP with the ESMTP extension mechanism, RFC 2033 LMTP, and RFC 6409
Message Submission — the same three the client targets, which keeps
`RFC-COVERAGE.md` a single table serving both directions.

`draft-ietf-emailcore-rfc5321bis` is **not an RFC** and must not be implemented
against or cited as one, per `CLAUDE.md`. Re-check at each milestone.

### HELO is mandatory, not legacy

RFC 5321 §4.5.1 lists the commands a conforming server MUST support: `EHLO`,
`HELO`, `MAIL`, `RCPT`, `DATA`, `RSET`, `NOOP`, `QUIT`, `VRFY`. `HELO` is on that
list. A server that answers only `EHLO` is not a conforming SMTP server, and
`VRFY` must be *implemented* even if the implementation is a `252` that declines
to verify — which RFC 5321 explicitly permits and which is what nearly every
deployed server does.

`EXPN` and `HELP` are optional and default to `502`.

### LMTP is a mode of the listener, not an extension

RFC 2033 §5 is unambiguous: *"As LMTP is a different protocol than SMTP, it MUST
NOT be used on the TCP service port 25."* And §4.1: the `HELO`/`EHLO` commands
are *replaced* by `LHLO`, and *"An LMTP server MUST NOT return a Positive
Completion reply code to these commands. The 500 reply code is recommended."*

So the mode is fixed when the listener is constructed, and the framework enforces
both halves: an LMTP listener rejects `EHLO`/`HELO` with `500`, an SMTP listener
rejects `LHLO`, and **the framework refuses to start an LMTP listener on port
25** rather than warning about it. A misconfiguration that silently produces a
protocol violation on the internet's most-scanned port is not a warning-level
event.

The behavioural difference that reaches the backend is one thing only: after the
final dot, LMTP emits one reply **per accepted recipient, in RCPT order**. That
is `smtp.DataResult` unchanged. SMTP mode emits one reply for the message,
modelled as the single-outcome case applied to every accepted recipient —
identical to the rule `API-STABILITY.md` §8 fixed for the client, running
backwards.

### Submission is a profile, not a mode

RFC 6409 submission differs from relay by *policy*: require authentication,
require TLS, enforce a submission-appropriate size limit, refuse to relay for
unauthenticated senders. Those are all settings and backend decisions, not
protocol changes. The framework provides them as options; it does **not** provide
RFC 6409 §8's optional message fixups (adding `Date:`, `Message-ID:`,
completing bare addresses). Rewriting message content is the caller's business
and is out of scope at every version, exactly as MIME composition is for the
client.

### The extension floor

A server framework is not useful below this line:

| Extension | RFC | Why it is floor, not optional |
|---|---|---|
| `PIPELINING` | 2920 | every modern client uses it; without it, throughput collapses and §4's read-ahead rules are untested |
| `SIZE` | 1870 | without an advertised limit, the only defence against a huge message is disconnecting mid-`DATA` |
| `8BITMIME` | 6152 | refusing 8-bit content in 2026 breaks essentially all real mail |
| `ENHANCEDSTATUSCODES` | 2034 | the framework already carries `smtp.EnhancedCode` everywhere; not advertising it discards information it already has |
| `STARTTLS` | 3207 | non-negotiable on any internet-facing listener |
| `AUTH` | 4954 | required for the submission profile to exist at all |
| `SMTPUTF8` | 6531 | the client verified it; a server that cannot receive what the client sends is an embarrassing asymmetry |
| `CHUNKING`/`BDAT` | 3030 | the transparency layer is the highest-risk code in the server (§8), and `BDAT` is the path that avoids it — **conditional on §2a**, which is what makes it implementable at all; a reviewer of revision 1 was right that without the spool contract this row could not be claimed |

`DSN`, `LIMITS`, `REQUIRETLS`, `MT-PRIORITY`, `DELIVERBY`, `FUTURERELEASE`,
`RRVS`, `BINARYMIME` and the group C keywords are optional and reach the backend
as options-struct fields (§3).

`ATRN` (RFC 2645) is deferred from the client in T10 because its role reversal
does not fit a client session model. It fits here — the server is the party that
hands the connection back — and it is the one extension whose *natural* home is
the server framework. Still deferred to T21; recorded so it is not lost.

### Enhanced status codes have a placement rule, and it has exceptions

RFC 2034 §3: the text of 2xx, 4xx and 5xx responses is prefaced with the status
code, followed by one or more spaces — **except the initial greeting and any
response to `HELO` or `EHLO`**. RFC 2034 §4: *"All status codes returned by the
server must agree with the primary response code, that is, a 2xx response must
incorporate a 2.X.X code, a 4xx response must incorporate a 4.X.X code, and a 5xx
response must incorporate a 5.X.X code."*

Both are framework-enforced, and the second is enforced as an **invariant, not a
convention**: a backend returning a `550` with a `4.7.1` enhanced code is a bug
in the backend, and the framework must not put the contradiction on the wire. See
§3 for what it does instead.

---

## 2. The backend abstraction

The decision that determines whether this framework can be frozen — and the one
where this document parts company with the sibling.

### The rule, and what it is protecting against

`API-STABILITY.md` §4 permits exported interfaces only as marker interfaces or
stdlib interfaces; everything else is a struct of function fields. The reason is
mechanical: adding a method to an exported interface breaks every external
implementer, and a mail backend is the classic place that rule gets broken.

There is also a **mechanical gate** already in the tree.
`api_surface_test.go`'s `TestAPISurfaceNoExportedInterfaces` fails on an exported
interface in the scanned package directories. Choosing interfaces for the backend
does not merely need a written exception — it needs an existing gate amended. That
is a high bar, deliberately, and it is the right bar.

### Why the sibling reached the opposite answer, and why that reasoning does not transfer

`go-imap`'s design recommends a small mandatory interface set plus optional
capability interfaces, and amends its rule 4 to allow it. Its argument is
specific and correct *for IMAP*: nine already-published RFCs each want a **method
group** on the backend — CONDSTORE wants modseq-filtered fetch and store,
QRESYNC wants durable expunge history, ACL wants a rights model, QUOTA wants
quota roots, METADATA wants annotations, and so on. A growable mandatory
interface breaks nine times before it meets an RFC nobody has written, and a flat
function struct reaches roughly sixty nilable fields.

**SMTP's extension pressure has a different shape, and it is worth counting
rather than asserting.** Take every extension in `RFC-COVERAGE.md` and ask what
each one needs from a *backend*:

| What the extension adds | Extensions | Backend impact |
|---|---|---|
| a `MAIL`/`RCPT` parameter | SIZE, BODY/8BITMIME/BINARYMIME, SMTPUTF8, DSN (`RET=`,`ENVID=`,`NOTIFY=`,`ORCPT=`), DELIVERBY, FUTURERELEASE, MT-PRIORITY, RRVS, REQUIRETLS, AUTH=, NO-SOLICITING, MTRK, SUBMITTER, CONPERM, CONNEG | **a field on an options struct the backend already receives** |
| an advertisement only | LIMITS, ENHANCEDSTATUSCODES, PIPELINING | a field on a capability declaration |
| framing, not semantics | CHUNKING/BDAT | nothing — the backend still sees one `io.Reader` |
| a session-layer command | STARTTLS, AUTH | framework-owned; AUTH needs a credential callback |
| a genuinely new operation | ETRN, ATRN, BURL, VRFY, EXPN | **one function field each** |

Fifteen of the extensions this library already implements need **zero** new
backend operations, because `smtp.MailOptions` and `smtp.RcptOptions` are already
the growth surface and are already governed by `API-STABILITY.md` §3, which makes
adding a field non-breaking by construction. Five need one function each. The
mandatory core is five operations. The projected ceiling over the protocol's
remaining life is roughly **ten to twelve function fields**, not sixty.

That is the whole difference. In IMAP the extension pressure lands on the backend
interface; in SMTP it lands on the parameter structs, and those already exist,
are already open-ended, and were already designed for exactly this.

### Recommendation

**A struct of function fields, in two levels. This *applies* `API-STABILITY.md`
§4 rather than amending it, and needs no exception.**

```go
// Backend is shared by every connection and must be safe for concurrent use.
type Backend struct {
	// NewSession is REQUIRED, and is the only field. It is called once per
	// accepted connection, after the TCP accept and before the greeting is
	// written, so a backend may refuse a connection by returning an
	// *smtp.Error.
	//
	// Everything else lives on the returned *Session, because everything
	// else is per-connection state. See §2d for why authentication in
	// particular cannot live here.
	NewSession func(ctx context.Context, conn *ConnInfo, opts *NewSessionOptions) (*Session, error)

	_ struct{}
}

// Session is one connection's handlers. The framework never calls two of
// these concurrently for the same session (§4), so a backend needs no locking
// for per-session state — closures over the session's own variables are the
// intended style, and are why this is a per-session struct rather than
// fields on Backend taking a session token.
//
// A nil optional field means the corresponding capability is not advertised
// and the corresponding command is refused by the framework (§3).
type Session struct {
	// --- required ---

	// Mail is REQUIRED. reversePath is the parsed reverse-path with angle
	// brackets removed and any source route already discarded; the empty
	// string is the null reverse-path <>.
	Mail func(ctx context.Context, reversePath string, params *smtp.MailOptions, opts *MailOptions) error

	// Rcpt is REQUIRED. Returning an *smtp.Error rejects this recipient
	// only; the transaction continues and earlier recipients stay accepted.
	Rcpt func(ctx context.Context, forwardPath string, params *smtp.RcptOptions, opts *RcptOptions) error

	// Data is REQUIRED. r yields the complete message content with
	// transparency already removed and with the framing difference between
	// DATA and BDAT already erased (§2a).
	//
	// A backend SHOULD consume or discard r before returning, but the
	// framework does not depend on it: returning early is legitimate — it
	// is how a backend rejects on the headers alone — and the framework
	// resynchronises the wire itself. See "the incomplete-reader defence"
	// in §2a for what an early return does and does not permit.
	//
	// The result cardinality is exact and mode-dependent; see §2b. It is
	// never collapsed, summarised or reordered by the framework.
	Data func(ctx context.Context, r io.Reader, opts *DataOptions) (smtp.DataResult, error)

	// Reset is REQUIRED. It releases whatever transaction state Mail and
	// Rcpt allocated, and is called on EVERY path that abandons or
	// completes a transaction — see §2c for the seven of them and for what
	// reason carries.
	//
	// It cannot fail: a backend that cannot clean up has nothing useful to
	// tell the peer, and the framework has nothing useful to do with the
	// answer. Report it through the trace hook instead.
	Reset func(ctx context.Context, reason ResetReason, opts *ResetOptions)

	// Close is REQUIRED and idempotent. Resource release, NOT protocol
	// QUIT — the framework answers QUIT, and calls Close on every teardown
	// path including timeout, error, shutdown and client disconnect.
	Close func(ctx context.Context)

	// --- optional: authentication (§2d) ---

	// Authenticate is called at the mechanism's credential-verification
	// point, which is NOT the same as the end of the SASL exchange: some
	// mechanisms have further round trips to perform after verification
	// succeeds or fails (§2d). Completing the mechanism-specific exchange
	// afterwards is the framework's job, in both directions.
	//
	// Nil, together with a nil ChallengeResponse and a nil
	// SCRAMCredentials, means AUTH is not advertised at all.
	Authenticate func(ctx context.Context, cred *Credentials, opts *AuthenticateOptions) (*AuthResult, error)

	// ChallengeResponse serves the mechanisms the framework cannot verify
	// from a credential alone because it does not hold the secret —
	// CRAM-MD5 is the case that exists today. The backend is given the
	// challenge the framework issued and the response the client returned,
	// and answers whether they match. Nil means no such mechanism is
	// advertised.
	ChallengeResponse func(ctx context.Context, ch *Challenge, opts *ChallengeOptions) (*AuthResult, error)

	// SCRAMCredentials returns stored key material: salt, iteration count,
	// StoredKey and ServerKey. SCRAM cannot be served from a plaintext
	// password check, which is why this is a separate field rather than a
	// mode of Authenticate. Nil means no AUTH=SCRAM-* mechanism is
	// advertised.
	SCRAMCredentials func(ctx context.Context, username string, opts *SCRAMOptions) (*SCRAMKeys, error)

	// CommitAuth is REQUIRED whenever any of the three verification fields
	// above is non-nil. It is the single point at which a session becomes
	// authenticated, for every mechanism.
	//
	// The framework calls it exactly once, after every mechanism-specific
	// proof and round trip has completed successfully, and before the 235
	// goes on the wire. It is never called for a refused, aborted,
	// malformed or internally failed attempt.
	//
	// It cannot fail: every authoritative decision has already been made.
	// A backend that keeps no local authorization state supplies a no-op.
	CommitAuth func(ctx context.Context, result *AuthResult, opts *CommitAuthOptions)

	// --- optional operations, one field each ---

	Verify func(ctx context.Context, address string, opts *VerifyOptions) (string, error)
	Expand func(ctx context.Context, list string, opts *ExpandOptions) ([]string, error)
	Help   func(ctx context.Context, topic string, opts *HelpOptions) (string, error)
	ETRN   func(ctx context.Context, domain string, opts *ETRNOptions) error

	_ struct{}
}
```

One required field on `Backend`, five on `Session`. Everything else is
nil-means-unadvertised.

Revision 1 put `Authenticate` and `SCRAMCredentials` on `Backend`. That was
wrong and §2d explains why; it also had no `ChallengeResponse`, which meant the
sketch could not serve CRAM-MD5 at all despite `internal/smtpsasl` already
implementing its initiator half.

### The three objections, answered

**"Nil fields move a compile-time error to runtime."** True, and with sixty
fields it would be disqualifying. With six it is closed by a **construction-time
gate**: `NewServer` returns an error naming every missing required field, and
`Backend.NewSession` returning a `*Session` with a nil required handler
terminates that connection with a `421` and a logged framework error rather than
panicking. The check runs once at startup, not per request, so the failure mode
is "the process refuses to start", which is a good failure mode — not "a
production request panics", which is the one the sibling was right to fear at its
scale.

**"Closures instead of methods are unidiomatic for a stateful backend."** SMTP's
session state is genuinely shallow: connection → optionally authenticated →
transaction → recipients → content, one transaction at a time, no concurrency
within a session. `Backend.NewSession` returning a per-connection `*Session`
struct is where the state lives, and a backend author writes
`func (s *mySession) mail(...)` and assigns the method value. That is one line of
plumbing per handler, against IMAP's three-level hierarchy where the same pattern
genuinely does become session-plumbing-by-closure.

**"A future extension will need a method on an existing type."** It needs a
*field*, which is additive, which is the entire point. The one shape that would
break this is an extension that changes the *signature* of an existing operation
— and the defence against that is the same one the client uses: every handler
takes an options struct with a `_ struct{}` guard from its first commit, even
where that struct is empty today, per `API-STABILITY.md` §3 and §7. Note the
double options parameter on `Mail` and `Rcpt`: `params` is the wire vocabulary
the peer sent, `opts` is the framework's per-call options. They grow for
different reasons and must not be merged.

### The rule, and its gate

> A new extension may add a **field** to `Backend`, to `Session`, or to any
> options struct. It may never change the signature of an existing field, and it
> may never introduce an exported interface.

Enforced by extending `api_surface_test.go` to scan `smtpserver/` — a data change
to `internalPackageSuffixes` and the directory list, not a new mechanism. The
existing gates (`TestAPISurfaceContextFirst`, `TestAPISurfaceOptionsStruct`,
`TestAPISurfaceNoExportedInterfaces`, `TestAPISurfaceKeyedLiteralDocNote`) then
apply to the server surface unchanged, and `TestAPISurfaceNoExportedInterfaces`
in particular becomes the standing enforcement of this section. `API-STABILITY.md`
§3's record of what happens to a rule without a gate is the reason this is not
left as prose.

## 2a. BDAT, and how the backend still sees one reader

**This is the contract revision 1 was missing, and it is the reason CHUNKING
belongs in the extension floor rather than being deferred.**

### The problem, stated exactly

Revision 1 promised three things that cannot all hold:

1. one goroutine per connection;
2. the backend receives one continuous `io.Reader`, independent of DATA versus
   BDAT;
3. each successful non-final BDAT chunk gets its own reply.

For `DATA` the three are compatible: the backend's `Read` calls pull from the
socket until `<CRLF>.<CRLF>`, and there is exactly one reply afterwards. For
`BDAT` they are not. RFC 3030 §2 requires a reply per chunk — *"A 250 response
MUST be sent to each successful BDAT data block within a mail transaction"* —
and between chunks the same goroutine must parse the next `BDAT` command, read
its declared size, write the previous chunk's reply, and handle `RSET`. A
goroutine sitting inside `Session.Data` doing none of those things cannot do them.

### Decision: a framework-owned bounded spool

The framework reads each BDAT chunk, acknowledges it, and accumulates. On
`BDAT ... LAST` it calls `Session.Data(ctx, spoolReader, opts)` once.

The backend therefore never learns whether the peer used `DATA` or `BDAT`, which
is the property that matters: the alternative is every backend implementing
CHUNKING separately and inconsistently.

The asymmetry this buys must be stated rather than glossed: **`DATA` streams to
the backend live, and `BDAT` does not.** Under `DATA`, backpressure from a slow
backend reaches the peer through TCP. Under `BDAT`, the whole message is
accumulated before the backend sees a byte, so a slow backend cannot slow the
upload and the spool is the buffer that absorbs it. That is the cost of the
per-chunk reply obligation, and it is intrinsic to CHUNKING rather than to this
design.

### The spool contract

Approval of §2a means approving these. They are contract, not implementation
detail, because a backend author and an operator both have to reason about them.

| Item | Contract |
|---|---|
| **Start in memory** | up to `MaxSpoolMemoryBytes`, default small (64 KiB order), so the common small message never touches disk |
| **Spill to disk** | above that threshold, into `SpoolDir` (default `os.TempDir()`), file mode `0600`, created with `O_EXCL` |
| **Unlink immediately after open** | the file is removed from the directory while still open, so cleanup is guaranteed by process exit even on panic or `SIGKILL`. On platforms where that is unavailable, cleanup is explicit and the deviation is documented |
| **Total bound** | `MaxSpoolBytes`, and it is enforced independently of `SIZE` — a peer may omit `SIZE` or lie about it |
| **Exceeding the bound** | reply `552 5.3.4` to the chunk that crosses it, then enter the failed-BDAT state below. Never a disconnect: a size rejection the peer can read is strictly better than one it must infer |
| **Storage failure** | reply `451 4.3.0`, log it, enter the failed-BDAT state. A full disk is an operational condition, not a client error, and the class must say so |
| **The final backend call streams** | `spoolReader` reads from memory or from the file; the framework never materialises the message as a `[]byte`, in either mode |
| **`SIZE` counts client octets** | the `SIZE` advertisement and its enforcement count the message octets the client supplied. The framework's own prepended `Received:` header is not counted against them, in either direction |
| **Cleanup** | on `RSET`, on a new `MAIL`, on completed `DATA`/`BDAT`, on failed `DATA`/`BDAT`, on `STARTTLS`, on disconnect, on timeout, on shutdown, and on a panic in the backend. That is §2c's list, and the spool is released on every entry in it |

### The incomplete-reader defence

Revision 2 said `Data` "MUST be consumed or discarded before returning" and
stopped there. **A backend obligation cannot be the framework's only defence
against a framing failure.**

Under live `DATA`, the reader is socket-backed. A backend that reads the headers,
decides to reject, and returns leaves the rest of the message — *including the
`<CRLF>.<CRLF>` terminator* — unread. If the framework writes the reply and
resumes command parsing, message body bytes are parsed as SMTP commands. That is
the smuggling failure class arriving through the backend API instead of through
the parser, and RFC 5321 §3.3 is clear that the final reply follows receipt of
the complete end-of-mail-data indication — so resynchronisation is not something
the framework may delegate.

**The framework hands `Data` a tracked reader and enforces the following, in this
order:**

1. Call `Session.Data`.
2. Ask the tracked reader whether it reached the real end of data.
3. If not, **drain it to the end-of-data indication, under the data deadline,
   before any further command parsing.** No reply is written until the drain
   completes.
4. Apply the outcome rule below.
5. If the drain fails — peer disconnect, deadline expiry, or a transport error —
   close the connection. There is no safe way to resume a stream whose framing is
   unresolved.

**The outcome rule, refined from the review's version.** The review said to treat
early return as a backend defect and discard the result. That is right for a
*success* claim and too strong for a rejection:

**The rule is per entry, not per result**, because an LMTP `DataResult` can carry
several outcomes at once — `250`, `550` and `451` in one transaction — and a rule
phrased over "a successful result" leaves that case to the implementer:

| Entry in the result | Handling when the reader was not drained |
|---|---|
| `2xx` | **replaced with `451 4.3.0`.** A claim to have delivered a message the backend never read is not credible |
| `4xx` or `5xx` | **preserved.** Rejecting on the headers alone is legitimate and supported; the only thing the backend got wrong is that the framework, not it, resynchronised the wire |

Applied to the transaction as a whole, after the drain completes:

| Case | Outcome |
|---|---|
| at least one `2xx` was replaced | the result is incoherent in part: `Reset(ResetFailed)`, and the backend defect is reported |
| no `2xx` present, so nothing was replaced | the outcome is authoritative and complete: emitted as-is, `Reset(ResetCompleted)`, **no defect reported** |
| the backend returned an `error` | honoured as §2b defines, after the drain |
| the drain failed | connection closed; no reply is meaningful, and no `DataResult` is emitted |

SMTP mode is the single-entry case of the same rule, since §2b fixes its
cardinality at one.

That second row is a refinement of the review's version, which sent every
early-return case to `ResetFailed`. A backend that rejected every recipient and
returned without reading did nothing wrong under this contract — telling it the
transaction failed would ask it to roll back a decision it made deliberately and
would report a defect that does not exist.

Making early rejection *supported* rather than *defective* is the better contract
generally: a backend that must read 200 MiB it has already decided to discard is
a backend that will read it badly. What must never be optional is who
resynchronises the wire.

**Under `BDAT` the hazard does not arise** — `Session.Data` reads from a completed
spool, not the socket, so an unread remainder cannot desynchronise anything. An
early return is still diagnosed, and a success claim over an undrained spool
reader is still discarded, because the credibility argument is unchanged.

### Failure and the poisoned state

RFC 3030 §2 is specific about all three parts of this, and the framework
implements all three:

- *"If a failure occurs after a BDAT command is received, the receiver-SMTP MUST
  accept and discard the associated message data before sending the appropriate
  5XX or 4XX code."* — the framework consumes the full announced octet count even
  when it has already decided to reject. It does not disconnect early, because
  the peer's next bytes are content it must not parse as commands.
- *"the receiver SMTP MUST be prepared to accept and discard additional BDAT
  chunks already in the pipeline after the failed BDAT."* — so the failed state
  still parses `BDAT` commands and consumes their octets.
- *"The resulting state from a failed BDAT command is indeterminate. A RSET
  command MUST be issued to clear the transaction before additional commands may
  be sent."*

So after a failed chunk the session enters an explicit **failed-BDAT state**:

- A further `BDAT` has its full announced octet count consumed and discarded, and
  **is then answered with `503`.** Revision 2 said the chunks were consumed and
  discarded but never said what reply followed, which would let an implementation
  silently swallow a pipelined chunk and stall the reply stream — the peer is
  counting replies against commands (RFC 2920 §3.2), so a missing one
  desynchronises it just as surely as a missing byte.
- `RSET` clears the state and is answered normally. `QUIT` and `NOOP` are
  answered normally.
- Everything else gets `503`.

`Session.Data` is never called — the backend sees `Reset(ResetFailed)` instead,
which is §2c.

### The aggregate budget

`MaxSpoolBytes` and `MaxSpoolMemoryBytes` bound **one transaction**. They do not
bound the server, and revision 2 stopped there, which left four resources
exhaustible by connections that are each individually compliant: heap, the
filesystem behind `SpoolDir`, file descriptors, and write bandwidth.

**The spool is therefore a shared manager owned by the `Server`, not a
per-connection buffer:**

| Bound | Scope |
|---|---|
| `MaxTotalSpoolMemoryBytes` | ceiling on the memory-resident portion of all live spools |
| `MaxTotalSpoolBytes` | ceiling on total live spool bytes, memory plus disk |
| `MaxConcurrentSpools` | how many transactions may hold a spool at once |

**These are server-instance-wide, not process-wide, and the distinction is
deliberate.** A manager owned by a `Server` cannot bound a second `Server` in the
same process — a submission listener and a relay listener are two instances and
would each get their own budget. Calling them process-wide, as an earlier
revision did, would be a claim the type cannot honour.

Server-instance scope is sufficient for the deployment this framework targets and
is the simpler contract. If a caller genuinely needs one budget across several
listeners, the fix is to export the manager so it can be shared — an additive
change under §3, deliberately not made now, because designing a shared-manager
API against zero callers is the mistake this project's client surface spent M0
avoiding.

- **Reservation is incremental**, per accepted chunk, not per transaction at
  `MAIL` time. Reserving `MaxSpoolBytes` up front would let a handful of small
  messages exhaust the budget.
- **Release happens on every cleanup path in the contract table above** — the same
  seven `Reset` paths, plus panic. A reservation leak is indistinguishable from a
  slow memory leak and presents in production as unexplained `452`s.
- **Aggregate exhaustion is temporary, not permanent**: `452 4.3.1`
  (insufficient system storage), distinct from the per-transaction `552 5.3.4`
  (message too big). A peer retrying later is exactly the right behaviour, and a
  peer being told its message is permanently too large is not.
- **The announced chunk is still consumed before the failure reply**, per RFC
  3030 §2. Resource pressure does not license breaking framing.
- **`NewServer` validates the product.** If `MaxConnections × MaxSpoolBytes`
  exceeds `MaxTotalSpoolBytes`, that is not an error — it is the normal,
  intentional case, since not every connection spools at once. What *is* an error
  is leaving the aggregate limits unset while CHUNKING is enabled: the server
  refuses to start, because the alternative is an unbounded default discovered on
  a full disk.

### The two alternatives, rejected

- **A pipe plus a backend goroutine.** `Session.Data` runs concurrently on an
  `io.Pipe` while the command loop feeds it chunk by chunk. It preserves
  streaming and eliminates the spool. It also invalidates §4's stated concurrency
  model — the backend is no longer on the connection goroutine — and it
  reintroduces exactly the re-entrancy and lifetime questions this design avoids:
  what cancels the backend goroutine when the peer disconnects mid-chunk, what
  happens to a backend blocked writing when the reader has gone, and whether
  `Reset` may run while `Data` is still live. Rejected: the complexity lands on
  every backend, and the spool's cost lands only on operators who enable
  CHUNKING.
- **A chunk-aware backend API.** `Session.DataChunk(ctx, r, last, opts)`. Honest
  about the protocol and cheapest to implement — and it exposes CHUNKING to every
  backend, including the ones that will never care, which is the one thing the
  whole design is trying to avoid. Rejected.

## 2b. The data outcome contract

Revision 1 said an SMTP backend "may return one entry" and that the framework
collapses. **There is no collapsing algorithm, and there must not be one** — a
framework that merges differing per-recipient outcomes into a single reply is
inventing a delivery decision the backend did not make.

The cardinality is exact:

```
SMTP mode: len(result) == 1
           the single outcome applies to the whole transaction

LMTP mode: len(result) == N, where N is the number of successful RCPT commands
           in original RCPT order, one entry per successful RCPT command —
           including duplicates
```

The LMTP rule is RFC 2033 §4.2 verbatim: *"after the final '.', the server
returns one reply for each previously successful RCPT command in the mail
transaction, in the order that the RCPT commands were issued"*, and *"Even if
there were multiple successful RCPT commands giving the same forward-path, there
must be one reply for each successful RCPT command."* Deduplicating identical
forward-paths is therefore a protocol violation, not an optimisation, and the
framework must not do it on the backend's behalf.

### `N` is never zero, and that is a guarantee rather than a case to handle

The cardinality rule permits `N == 0` arithmetically. The protocol does not
permit reaching it, and the framework closes the gap before the backend is
involved.

RFC 2033 §4.2 states the restriction directly: *"when there have been no
successful RCPT commands in the mail transaction, the DATA command MUST fail with
a 503 reply code."* RFC 5321 §3.3 gives SMTP the same latitude — *"If there was no
MAIL, or no RCPT, command, or all such commands were rejected, the server MAY
return a 'command out of sequence' (503) or 'no valid recipients' (554) reply"* —
and the framework takes it, so both modes behave alike.

**So the framework refuses `DATA` and `BDAT` with `503` when no `RCPT` has
succeeded, and `Session.Data` is never called with zero accepted recipients.** A
backend may rely on `N >= 1`. RFC 3030 does not restate the restriction for
`BDAT` because it predates neither problem nor precedent; applying it there is the
only reading consistent with `BDAT` being a data-phase command.

An earlier draft of this section contemplated an "empty result or framework-owned
discard path". That was solving a problem the RFCs already close, and it would
have put a branch in every backend for a state it can never observe.

### `DataResult` versus `error`

They answer different questions and are mutually exclusive:

| Return | Meaning |
|---|---|
| **cardinality-valid** `DataResult`, nil `error` | the backend reached an authoritative delivery outcome. It goes on the wire as-is |
| empty `DataResult`, non-nil `error` | no authoritative outcome exists — an internal failure. The framework produces a single `451 4.3.0` in SMTP mode, or one per recipient in LMTP mode |
| **both non-empty and non-nil** | **invalid.** A framework error: the transaction fails safely and the backend defect is reported |
| wrong cardinality for the mode | **invalid**, same handling |

"Cardinality-valid" rather than "non-empty" because the two are not the same
statement, and revision 2 conflated them: the check is `len(result) == 1` in SMTP
mode and `len(result) == N` in LMTP mode, evaluated against the mode, not against
zero.

A backend that wants to reject the message says so *in* the `DataResult`, with a
`5xx` and an enhanced code. `error` is not a rejection channel.

## 2c. Transaction lifecycle, and why `Reset` is required

Revision 1 made `Reset` optional with the note "nil means the framework simply
drops transaction state". That is only true if the *framework* holds the state.
The design elsewhere encourages backends to hold transaction state in their
session closure — an allocated queue file, a database transaction, a reserved
quota — and the framework cannot drop what it does not own.

**`Reset` is required, and it runs on every path that ends a transaction:**

```go
type ResetReason uint8

const (
	ResetExplicit  ResetReason = iota // RSET
	ResetNewMail                      // MAIL replacing an open transaction
	ResetCompleted                    // DATA or BDAT LAST completed, result delivered
	ResetFailed                       // DATA or BDAT failed, including the poisoned state
	ResetStartTLS                     // RFC 3207 §4.2 state discard
	ResetSessionEnd                   // QUIT, disconnect, timeout, shutdown, panic
)
```

Three rules, because each is ambiguous otherwise and each is observable:

1. **`Reset` runs *before* the handler that caused it.** A `MAIL` arriving with a
   transaction open calls `Reset(ResetNewMail)` and *then* `Mail`. The backend
   therefore never sees two overlapping transactions, and never has to detect the
   replacement itself.
2. **`ResetCompleted` does not depend on the reply reaching the peer.** Revision 2
   said it "runs after the result has been written to the wire", which is
   ambiguous in the case that matters: the backend has committed delivery, the
   framework attempts the final `250`, and the peer disconnects mid-write. **The
   message was still delivered.** The reason stays `ResetCompleted` — not
   `ResetFailed`, which would tell the backend to roll back something it already
   committed, and not `ResetSessionEnd`, which would lose the outcome entirely.

   The precise definition:

   > `ResetCompleted` is called after an authoritative delivery result has been
   > obtained and after the framework has *attempted* to emit it. Write success
   > is not a prerequisite.

   Cleanup still cannot change the outcome, which was the point revision 2 was
   reaching for; it just cannot be conditioned on the socket either.
3. **`ResetSessionEnd` fires only when a transaction is still open at teardown.**
   A session that completed its last transaction cleanly has already had its
   `Reset(ResetCompleted)`; firing again at teardown would reset an already-reset
   transaction, and a backend using `Reset` for accounting would double-count. If
   no transaction is open, `Close` alone follows.

**`Reset` cannot fail.** It returns nothing. A backend whose cleanup failed has
nothing useful to tell the peer — the transaction is already over — and the
framework has nothing useful to do with the answer except log it, which the
backend can do itself. Giving it an error return would create a path where a
cleanup failure changes a delivery outcome, which is worse than the failure.

`Close` remains separate, is called exactly once per session after the final
`Reset(ResetSessionEnd)`, and is idempotent.

A backend that holds no transaction state supplies a no-op. That is one line, and
it is a better default than a nil field that silently does the wrong thing for
the backends that do hold state.

## 2d. Authentication is a session operation

Revision 1 put `Authenticate` on `Backend`, which is shared by every connection.
The authenticated principal is not shared by every connection. There was
consequently no defined way for a successful authentication to bind an identity
to the session, change what `Mail`/`Rcpt`/`Data` decide, alter per-user
capabilities and limits, or reach `Received:` generation — all four of which are
the entire point of authenticating.

**Authentication moves to `Session`.** The backend's per-connection closure
updates its own state directly, which is the shape the rest of this design
already uses, and the framework separately records a canonical identity:

```go
type AuthResult struct {
	// Identity is the authenticated identity, canonicalised by the backend.
	// The framework records it for Received: generation (RFC 3848 chooses
	// ESMTPA/ESMTPSA from whether this is set) and for tracing.
	// Set on success; ignored when Failure is non-nil.
	Identity string

	// Failure, when non-nil, is an authentication *decision*: the exchange
	// worked and the credential was refused. Nil means success.
	Failure *AuthFailure

	_ struct{}
}

// AuthFailure is a refused credential. It is deliberately NOT an error type:
// API-STABILITY §5 permits exactly one, *smtp.Error, and this carries
// mechanism-specific data an SMTP reply has nowhere to put.
type AuthFailure struct {
	// Err is what the peer sees. Nil defaults to 535 5.7.8.
	Err *smtp.Error

	// OAuth carries RFC 7628 §3.2.2's error document for OAUTHBEARER and
	// XOAUTH2. Ignored by every other mechanism.
	OAuth *OAuthError

	_ struct{}
}

type OAuthError struct {
	Status              string // REQUIRED by RFC 7628, e.g. "invalid_token"
	Scope               string // optional
	OpenIDConfiguration string // optional
	_                   struct{}
}
```

**`error` keeps the meaning §2b gave it, and the symmetry is deliberate.** A
refused credential is an *outcome* and travels in the result; a non-nil `error`
means no authoritative outcome exists — the token service was unreachable, the
user database was down — and becomes `454 4.7.0`, RFC 4954's temporary
authentication failure, which tells the peer to retry rather than to fix its
credentials. Two mechanisms in this document now share one shape, which is one
fewer contract for a backend author to learn.

### The identity model

Three identities exist and conflating them is a privilege-escalation bug:

| | Source | Meaning |
|---|---|---|
| **authentication identity** | the SASL `authcid` | who proved possession of a credential |
| **authorization identity** | the SASL `authzid`, when supplied | who the session acts as |
| **transport identity** | TLS client certificate | what the connection proved, independent of SASL |

`Credentials` carries all three, populated per mechanism. The framework never
decides whether an `authzid` is permitted — that is an authorization question and
belongs to the backend, which returns an `*smtp.Error` with `535 5.7.8` if it is
not. A framework that silently accepted a mismatched `authzid` would let any
authenticated user act as any other.

### What each mechanism needs, and which field serves it

| Mechanism | Backend field | Notes |
|---|---|---|
| PLAIN, LOGIN | `Authenticate` | framework extracts authcid, authzid, password |
| EXTERNAL | `Authenticate` | no password. The identity is derived by the framework from `tls.ConnectionState` and placed in `Credentials.TLS`; an EXTERNAL attempt on a session with no client certificate is refused by the framework before the backend sees it |
| OAUTHBEARER, XOAUTH2 | `Authenticate` | the token is passed opaquely. The framework does not parse, validate or introspect it; that requires an issuer the framework has no business knowing about |
| CRAM-MD5 | `ChallengeResponse` | the framework issues the challenge and passes it with the client's response. The backend holds the shared secret and verifies. **Revision 1 had no field that could serve this** |
| SCRAM-\*, SCRAM-\*-PLUS | `SCRAMCredentials` | stored salt, iteration count, StoredKey, ServerKey. Channel binding stays framework-side via `tls.ConnectionState.ExportKeyingMaterial`. **See the post-proof rule below** |

Advertisement follows from the fields, per §3: `AUTH` is not advertised at all
when all three are nil, and each mechanism appears only when the field that can
serve it is non-nil. A server advertising `AUTH=CRAM-MD5` against a backend that
cannot verify a challenge is a lie the client discovers only after failing.

RFC 4954's sequencing rules stay framework-owned: `AUTH` is refused after a
successful authentication, refused mid-transaction, and the advertised mechanism
list may differ before and after TLS.

### The backend is called at the verification point, not at the end

Revision 2 said `Authenticate` is called "after the framework has run the SASL
exchange to completion". **That is true of PLAIN and LOGIN and false in
general**, and OAUTHBEARER is the counter-example that matters.

RFC 7628 §3.2.2 has the server return *"an error result in JSON format"* on
failure; §3.2.3 then requires that *"The client MUST then send either an
additional client response consisting of a single %x01 (control A) character to
the server in order to allow the server to finish the exchange or a SASL abort
message"*, after which the server fails the authentication. The reason is
structural: *"SASL explicitly prohibits additional information in an unsuccessful
authentication outcome"*, so the mechanism smuggles its diagnostics into a
challenge *before* the failure.

So token validation happens **during** the exchange, and there is a round trip
still owed afterwards. The contract:

> The framework calls the applicable backend handler at the mechanism's
> credential-verification point. The framework remains responsible for completing
> the mechanism-specific exchange after the handler succeeds or fails — including
> any challenge the mechanism requires it to emit, and any dummy response it
> requires it to consume.

`AuthFailure.OAuth` is what lets the framework build that challenge without the
backend reaching the wire, and without `535` swallowing the diagnostic the client
actually needs.

### `CommitAuth`: one state transition, every mechanism

Revision 3 moved authentication to `Session` so that `Mail`, `Rcpt` and `Data`
could see the authenticated principal, then left SCRAM with no safe moment to
record it. The sequence was:

```
SCRAMCredentials returns keys + Result
    → framework verifies the client proof
    → framework records the identity
    → ...and the backend closure is never told
```

The backend must not mutate its closure inside `SCRAMCredentials` — that would
authenticate anyone who knows a valid username — so the effect was a design that
forbade the unsafe moment without supplying a safe one. Framework state and
backend state could then disagree for exactly the mechanism most likely to be
deployed against a real user database, which defeats the reason authentication
was moved onto `Session` at all.

The general form of the problem is worth stating, because it outlives SCRAM: **a
verification callback runs at the mechanism's verification point, which is not
the same instant as successful protocol completion.** Verification handlers
validate. They must not commit.

So there is one commit point, and it is uniform:

```
mechanism exchange
    → backend verification      (Authenticate / ChallengeResponse / SCRAMCredentials)
    → remaining proof and round trips   (framework)
    → CommitAuth                (backend records its own state)
    → framework records AuthResult.Identity
    → 235
```

The rules:

- **Required whenever any verification field is non-nil.** A backend that
  verifies credentials and has nowhere to record the outcome is not a
  configuration worth supporting; a no-op is one line.
- **Called after SCRAM proof verification**, after CRAM-MD5 response
  verification, and after a successful PLAIN, LOGIN, EXTERNAL, OAUTHBEARER or
  XOAUTH2 exchange.
- **Called before the `235`**, so a backend cannot observe an authenticated
  session on the wire before it has been told about it.
- **Never called** for refusal, client abort, malformed exchange, or internal
  failure.
- **Cannot fail**, for §2c's reason: every authoritative decision is already
  made, and an error return here would create a path where bookkeeping changes an
  authentication outcome.
- **Verification callbacks must not independently mark the session
  authenticated.** That is now a stated rule rather than an inference, and
  `backendtest` checks it.

`SCRAMKeys.Result` keeps its job: after the proof verifies, it is the value the
framework passes to `CommitAuth`.

### SCRAM: returning keys is not authenticating

`SCRAMCredentials` returns stored key material. **A backend must not treat the
lookup as an authentication event**, and revision 2's sketch left this
undefined — a backend closure that flipped itself to "authenticated" on lookup
would accept anyone who knew a valid username, since the client has not yet
proved possession of anything.

The framework verifies the client proof. The identity becomes active only after
that. So the result travels with the keys and is applied later:

```go
type SCRAMKeys struct {
	Salt          []byte
	Iterations    int
	StoredKey     []byte
	ServerKey     []byte

	// Result is applied by the framework ONLY after it has verified the
	// client proof. Returning it does not authenticate the session.
	Result AuthResult

	_ struct{}
}
```

Authorising the `authzid` may happen during the lookup — the backend has
everything it needs to decide — but the resulting identity is inert until the
proof verifies, at which point `CommitAuth` delivers it.

A SCRAM-specific `CompleteSCRAM` callback was the alternative and is rejected:
the problem is not SCRAM's, it is every mechanism's, and one uniform transition
is worth more than a special case for the mechanism that made it visible.

### What the framework hands the backend, and what it never does

- **Backends see parsed paths, never raw wire strings.** The angle brackets,
  source route, quoting and address-literal forms are resolved before the handler
  is called. Every backend written against a raw string re-derives RFC 5321
  §4.1.2 badly; centralising it means it is wrong in at most one place. This is
  the direct analogue of the sibling's "backends see UIDs, never sequence
  numbers" decision, taken for the same reason.
- **Backends see content through an `io.Reader`, never a `[]byte`.** A 200 MiB
  message must not buffer, in either direction. The reader is bounded by the
  negotiated `SIZE` and by §8's limits, and yields identical bytes whether the
  client used `DATA` or `BDAT` — the framing difference must not be visible to a
  backend, or every backend implements CHUNKING separately and inconsistently.
- **The framework never calls a backend handler from a timer, a signal handler,
  or another connection's goroutine.** See §4.

---

## 3. Framework versus backend, and how capabilities are advertised

| Framework-owned | Backend-delegated | Cooperative |
|---|---|---|
| greeting, `EHLO`/`HELO`/`LHLO`, the advertisement | `MAIL`, `RCPT` acceptance | `AUTH` — framework runs the mechanism, backend answers the credential |
| `RSET`, `NOOP`, `QUIT` | message delivery (`DATA`/`BDAT` content) | `SIZE` — framework enforces the advertised cap, backend may reject a smaller one |
| `STARTTLS` and the §8 state reset | `VRFY`, `EXPN`, `HELP`, `ETRN` | `LIMITS` — backend declares, framework formats and advertises |
| `PIPELINING` read-ahead and sync points | | `REQUIRETLS`, `DSN`, `MT-PRIORITY` and the rest — framework parses and validates syntax, backend decides |
| `DATA`/`BDAT` framing and transparency | | enhanced status codes — backend supplies, framework validates against the reply code |
| `Received:` header generation | | |
| command parsing, reply encoding, the state machine, timeouts, limits | | |
| LMTP per-recipient reply emission | | |

### AUTH is split at the credential, and SCRAM is the exception that proves it

`internal/smtpsasl` today implements the **initiator** half only —
`Mechanism.Start()` and `Next(challenge)`. The server needs the responder half
for PLAIN, LOGIN, CRAM-MD5, EXTERNAL, OAUTHBEARER, XOAUTH2 and SCRAM-\*. That is
T17 work, in `internal/smtpsasl`, and it is not a rewrite: the mechanisms'
message formats are shared, only the direction of the state machine flips.

The split is clean for PLAIN, LOGIN, OAUTHBEARER and XOAUTH2, and is not clean
for CRAM-MD5 or SCRAM. §2d gives the full model — three `Session` fields, three
distinct identities, and the per-mechanism table — and is the normative version;
the point here is only that the split exists at the credential and that where it
does not, the design says so with a field rather than by dropping the mechanism.

### Advertisement is derived, never hand-written

The framework owns a **capability descriptor table**. Each entry declares:

| Field | Purpose |
|---|---|
| `Keyword` | wire spelling, an `smtp.Extension` |
| `Params` | the parameter text after the keyword, computed per session (`SIZE 10485760`, `AUTH PLAIN LOGIN`, `LIMITS RCPTMAX=100`) |
| `RequiresBackend` | which `Backend`/`Session` fields must be non-nil |
| `RequiresTLS` | advertise only after TLS, or only before (`STARTTLS` is the latter) |
| `RequiresAuth` | advertise only to an authenticated session |
| `Modes` | SMTP, LMTP, or both |
| `Available` | optional dynamic check, per session |

The `EHLO` response is then **computed** from the table against the current
session state. A hand-maintained list drifts from what the backend actually does,
and every drifted entry is an interop bug that presents to the peer as a server
bug — the mirror of the interop harness's rule that a server failing to advertise
a keyword its profile claims is a failure, not a skip.

Cases the table exists to get right: `STARTTLS` disappears after TLS is
established; `AUTH` mechanisms differ before and after TLS, and each mechanism
appears only when the `Session` field that can serve it is non-nil (§2d);
`CHUNKING` is advertised only when the spool is configured and enabled (§2a);
`BINARYMIME` requires `CHUNKING`, per RFC 3030 §3 — *"The BINARYMIME service
extension can only be used with the 'CHUNKING' service extension"*; `SMTPUTF8`
changes what the address parser accepts; and in LMTP mode the whole table is
filtered by `Modes`.

**Options fields pair with advertisement, and that pairing is a gate, not a
promise.** The framework must not populate a `params` field for an extension it
did not advertise — a client that sends `RET=HDRS` without `DSN` advertised gets
a `501`, and the backend never sees a field it has no way to know is meaningless.
Every extension-owned field in the receive-side options structs declares the
capability that activates it, and a test fails on any field with no declaration.

### Enhanced-code disagreement: keep the primary class, replace the detail

RFC 2034 §4 requires the classes to agree (§1). When a backend returns a `550`
with a `4.7.1`, the framework must not put the contradiction on the wire — a
peer's policy engine reads whichever half it prefers, and the two halves disagree
about whether to retry.

**Revision 1 answered `451 4.3.0` and that was wrong.** It converted a permanent
rejection into a temporary one, so a message the backend intended to reject
outright gets retried on a schedule, for days, by every sender that respects
`4xx`. A bug-reporting mechanism that changes delivery semantics is worse than
the bug it reports.

**Decision: the three-digit reply code is authoritative, and the enhanced code is
replaced with the generic subcode of its class:**

```
2xx → 2.0.0
4xx → 4.0.0
5xx → 5.0.0
```

So `550` + `4.7.1` goes on the wire as `550 5.0.0`. This preserves the backend's
primary delivery decision, produces a syntactically consistent reply, and loses
only the *detail* — which was untrustworthy anyway, since it came from the half
of the reply that was already wrong.

The defect is reported through the trace hook and the error log every time, so it
stays loud operationally, and `backendtest` (§7) catches it in development, which
is where it should be caught. The three-digit code is chosen as authoritative
rather than the enhanced code because it is the one every peer since 1982
understands and the one that actually drives queue behaviour.

---

## 4. The session model, concurrency, and pipelining

**This is where SMTP is dramatically simpler than IMAP, and the design should say
so rather than importing complexity that has no cause here.**

The sibling's design needs a reader goroutine, a bounded command queue, an event
loop, a synchronous writer, an update batch protocol with a revision chain,
origin accounting and an overflow policy — all because IMAP servers push
unsolicited data to the client and must do so without renumbering messages the
client is mid-read on.

**SMTP has no unsolicited server data.** Every reply belongs to exactly one
command, in order. There is no update queue, no revision chain, no origin
accounting, no coalescing question and no overflow policy, because there is
nothing to overflow. `ARCHITECTURE.md` already records this for the client — *"SMTP
is lockstep, so there is no demultiplexer"* — and it holds identically in the
other direction.

### The structure

**One goroutine per connection.** It reads a command, executes it, writes the
reply, and repeats. That is the whole model.

Two things complicate it, and only two.

#### 1. Pipelining read-ahead, and the sync points

RFC 2920 §3.2's server requirements are specific and are enforced structurally:

- *"MUST respond to commands in the order they are received from the client."*
- *"MUST send all pending responses immediately whenever the local TCP input
  buffer is emptied."*
- *"MUST NOT flush or otherwise lose the contents of the TCP input buffer under
  any circumstances whatsoever."*

Concretely: the server may buffer replies while more commands remain buffered,
but must flush before it blocks on a read. That is what prevents the deadlock RFC
2920 §3.1 warns clients about, seen from the other side, and it is a *structural*
property of "flush before every blocking read" rather than a heuristic.

The framework must **not** read ahead past a sync point. RFC 2920 §3.1 names the
base set — *"The EHLO, DATA, VRFY, EXPN, TURN, QUIT, and NOOP commands can only
appear as the last command in a group"* — and then makes the general rule:
*"Additional commands added by other SMTP extensions may only appear as the last
command in a group unless otherwise specified by the extensions that define the
commands."*

So extension commands are synchronisation points **by default**, and the
framework does not need a hand-maintained list: `STARTTLS` and `AUTH` are sync
points because RFC 2920 says extension commands are, and RFC 3207 §4 confirms it
for STARTTLS specifically (*"the STARTTLS command must be the last command in a
group"*).

**`BDAT` is the exception, and revision 1 got this wrong by listing it as a sync
point.** RFC 3030 §2 explicitly requires a server to be prepared to *"accept and
discard additional BDAT chunks already in the pipeline after the failed BDAT"*,
which only makes sense if BDAT chunks may be pipelined. `BDAT` is therefore one
of RFC 2920 §3.1's "unless otherwise specified" cases, and §2a's design depends
on it: the framework reads the chunk's announced octets and then expects the next
command, possibly already buffered.

After `DATA`'s `354`, by contrast, the next bytes are content, not commands, and
a server that has already consumed them into a command buffer has desynchronised.
That is the same class of bug as SMTP smuggling and is tested with the same
vectors.

#### 2. The STARTTLS discard — complementary to RFC 2920, not contradictory

**Revision 1 framed RFC 2920 and RFC 3207 as conflicting. They do not conflict,
and the correction matters because the wrong framing produces a wrong
implementation rule.**

RFC 2920 §3.2 requirement 9 — *"MUST NOT flush or otherwise lose the contents of
the TCP input buffer under any circumstances whatsoever"* — exists to prevent
losing commands a client *legitimately* pipelined. RFC 2920 §3.1 already forbids
a client from pipelining anything after an extension command, and RFC 3207 §4
says so again for STARTTLS. **Plaintext bytes after the STARTTLS line are
therefore illegal by RFC 2920's own rule**, and discarding them contradicts
neither document.

Revision 1's implementation rule — "assert the input buffer was empty" — is also
too absolute, and the imprecision is the dangerous part. *Which* buffer? After the
handshake begins, bytes arriving on the connection are the TLS handshake, and a
server that drains the socket before starting TLS destroys it.

The rule, in five steps:

1. `STARTTLS` is accepted only at a legal synchronisation point, and only in a
   state where TLS is not already active.
2. Any plaintext bytes the SMTP decoder has **already prefetched** beyond the
   `STARTTLS` line are discarded, and the event is logged as a protocol violation
   — so the injection attempt is observable, not merely defeated.
3. The TLS handshake runs on the **underlying** `net.Conn`. The plaintext
   decoder's buffer is gone by then and its reader is never consulted again.
4. Bytes consumed by the TLS layer are never inspected by the plaintext SMTP
   parser. The framework must not "peek" for diagnostics here.
5. On a successful handshake, all SMTP session knowledge is reset per RFC 3207
   §4.2 — the `EHLO` argument, the negotiated extension set, `AUTH` state and any
   open transaction, which is `Reset(ResetStartTLS)` in §2c's terms.

Step 2 is the CVE-2011-0411 defence: an attacker injects plaintext commands after
`STARTTLS`, and a server that keeps its decoder buffer executes them inside the
TLS session, where the peer attributes them to the authenticated party. Step 3 is
what keeps the fix from breaking the handshake, and it is the step revision 1's
phrasing would have led an implementer to get wrong.

### The re-entrancy contract — what backend authors get in writing

- **`Session` handlers are never called concurrently for the same session.** No
  locking is needed for per-session state.
- **`Backend` fields and anything shared between sessions must be safe for
  concurrent use.**
- **A handler owns its goroutine for its duration.** There is no update path that
  can call back into a backend, so a backend cannot deadlock against the
  framework by holding a lock.

### What `ctx` cancellation actually promises

**Revision 1 said "`ctx` is cancelled when the connection dies, so a slow backend
observes a disconnected client". That is not implementable and the promise is
withdrawn.**

The connection goroutine is *inside* the backend handler for its duration. It is
not reading the socket, so it cannot observe EOF. SMTP's lack of unsolicited
server data removes the need for an IMAP-style event loop; it does **not** make a
peer disconnect observable during an arbitrary backend call. A `Session.Mail`
that blocks for five minutes without touching the socket will not learn that the
peer vanished four minutes ago, and no amount of context plumbing changes that.

The honest contract, which is what the framework provides:

| Signal | Promise |
|---|---|
| **per-command deadline** | every backend call receives a `ctx` with a deadline derived from the stage timeout. This is the bound that actually holds |
| **server shutdown** | cancels immediately and unconditionally. The framework owns this signal and does not need the socket to observe it |
| **peer disconnect** | **best-effort.** Detected at the next network operation — writing the reply, or the next read. A backend blocked without I/O learns about it when it returns |

The alternative — a separate read pump, or a platform-specific connection watcher
— would have to define exactly how it avoids consuming bytes the command parser
needs, which for a lockstep protocol with pipelining is a real design problem and
not a small one. It buys prompt cancellation of backend calls that are already
deadline-bounded. **Rejected**, and recorded so that a later revision proposing it
has to argue against this paragraph rather than discover the question fresh.

The deadline is what a backend author should rely on. It is stated in
`Session`'s doc comment in those words.

### Cancellation, `421`, and shutdown

SMTP has no command abort, so cancelling an in-flight command poisons the
connection — the same rule as the client, in reverse. The framework closes rather
than attempting to resynchronise.

Graceful shutdown sends `421` at the next protocol-legal point and closes. A
`421` mid-`DATA` is legal and is the correct answer to a shutdown during a large
message; the framework must not wait for a 200 MiB upload to finish before
exiting. `Server.Shutdown(ctx)` mirrors `net/http`: stop accepting, let in-flight
transactions finish until `ctx` expires, then force-close.

---

## 5. What has no analogue on the client side

Two subsystems from §0, expanded, because both are net-new and both are places
where "we will do it when we get there" produces a security bug rather than a
missing feature.

### Path parsing

Input is attacker-controlled and pre-authentication. The parser must handle, and
be fuzzed against: `<>`; `<@a.example,@b.example:user@c.example>` (accept, discard
the route, use the final address — RFC 5321 §4.1.2); `<Postmaster>`;
`<"quoted local"@example.com>`; `[192.0.2.1]` and `[IPv6:2001:db8::1]`; UTF-8
local parts and domains when `SMTPUTF8` was declared and **not** when it was not;
trailing esmtp-params separated from the path; and the size figures in
`address.go` as *minimums to accept*, with the server's own configurable maximum
enforced above them.

Two rules that are easy to get wrong: `MAIL FROM:` and `RCPT TO:` take **no space**
between the colon and the `<` in the strict grammar, but real clients emit one and
RFC 5321 §4.1.1.2's own notes acknowledge the practice — accept both, per the
project's standing rule that the library accommodates conformant-in-practice
peers. And `RCPT TO:<Postmaster>` without a domain is a distinct, legal form that
a naive `strings.Split(s, "@")` mishandles.

### `Received:` generation

RFC 5321 §4.4 requires it. The `with` keyword comes from RFC 3848 and is a
function of the session state, not a constant:

| Session | `with` |
|---|---|
| HELO, no TLS | `SMTP` |
| EHLO, no TLS, not authenticated | `ESMTP` |
| EHLO, no TLS, authenticated | `ESMTPA` |
| EHLO, TLS, not authenticated | `ESMTPS` |
| EHLO, TLS, authenticated | `ESMTPSA` |
| LMTP mode | `LMTP`, `LMTPA`, `LMTPS`, `LMTPSA` correspondingly |

Getting this wrong misreports whether a hop was encrypted or authenticated, and
downstream policy — DMARC alignment reporting, forwarding heuristics, abuse
tooling — reads it. It is one table and it must be driven by session state, never
by configuration.

The `FOR` clause is emitted only when the transaction has exactly one recipient,
because emitting it with several discloses one recipient's address to another.
This is a privacy requirement, not a formatting preference.

---

## 6. The reference backend

**Ships as a supported package: `smtpserver/memory`.** Not test-internal.

Four reasons, one of which the sibling does not have:

1. It is the only thing putting real pressure on §2's abstraction before it
   freezes. An abstraction validated only by its designer's mock is validated by
   nothing.
2. It gives the conformance suite a target drivable to any state on demand.
3. A framework nobody can run without first writing a storage layer gets no users
   and therefore no bug reports before its API freezes.
4. **It is immediately useful to users of the *client*.** An in-process SMTP sink
   that accepts everything and exposes the delivered messages is exactly what a
   test suite for an application using `smtpclient` needs, and today those users
   reach for a container or a third-party mock. This makes the reference backend
   load-bearing for the half of the library that already exists.

Constraints: documented not durable and not for production; a constructor, an
options struct, and the handler fields; it implements every optional handler
**the release itself claims to support**, not every one forever; and
pathological-state manipulation lives in `backendtest`, not in the supported
surface.

Out of scope, confirmed: maildir or SQL storage, a user database, queueing,
forwarding, spam filtering, DKIM verification. A server *framework* provides the
protocol and hands decisions to the caller; every item on that list is the
caller's, permanently.

---

## 7. Testing strategy

**Loopback — our client against our server over `net.Pipe` — is the inner loop,
not validation.** Fast, hermetic, catches regressions, and a shared misreading of
an RFC passes both sides silently. That is exactly the failure the client's
interop matrix exists to catch, and the server needs the same answer.

In descending order of value:

1. **Point the existing interop matrix at ourselves.** T06 already starts seven
   servers under podman, provisions accounts, drives real transactions and reads
   messages back through sinks. Adding `smtpserver/memory` as an
   `interop/servers/` entry reuses all of it and reports our coverage in the same
   units as Postfix's — the comparison that actually means something. Being
   in-process, it needs no image and no podman, so **the harness must stop
   assuming every profile has a container**; that is a real change to T06's code
   and is called out so it is budgeted.

   It is also the one matrix entry where the profile assertion ("a server not
   advertising a keyword its profile claims is a failure") catches *our* bug
   rather than a broken container.

2. **Real MTAs sending *to* us.** This is the mirror of the client matrix and it
   is nearly free, because the containers already exist. Postfix with a
   `relayhost` pointing at our listener; Exim likewise; `swaks`, the de-facto SMTP
   testing tool, for scripted edge cases; `msmtp` and Python `smtplib` for
   submission. For **LMTP server mode**, Postfix's `lmtp:` transport is a real
   LMTP client and is the natural counterpart to Dovecot's LMTP server already in
   the matrix.

   Honest note: **there is no `imaptest` equivalent for SMTP.** The sibling's
   single highest-value external check has no counterpart here, and pretending
   otherwise would set a false expectation. The compensation is that real MTAs
   are trivially available as senders, which is not true of IMAP clients.

3. **Server-side fuzzing** — the mirror of T11, non-optional. The command parser,
   the path parser and the BDAT framer face hostile input from **unauthenticated
   remote clients**, a larger and more exposed surface than the client's
   hostile-server case. Bar unchanged: no panic, no hang, no unbounded
   allocation. Corpus seeded from real client traffic and from the published
   SMTP-smuggling vectors, not invention.

4. **`smtpserver/backendtest`** — a conformance suite a third-party backend runs
   against itself, and the mitigation for §2's runtime-nil-field cost. It checks
   the contracts §2 relies on and never re-derives, because a backend that breaks
   one of these corrupts a transaction rather than failing visibly:

   - **The incomplete-reader path behaves as §2a specifies.** This replaces
     revision 2's "`Data` consumes or discards its reader before returning",
     which contradicts the supported early-rejection contract. Six checks:
     unread `DATA` is drained by the framework before any reply; an early
     rejection survives the drain unchanged; an early `2xx` is replaced with
     `451 4.3.0`; a drain failure closes the connection with no final reply;
     unread `BDAT` spool content cannot desynchronise the socket but still
     invalidates a success claim; and **no command parsing resumes until framing
     is resolved** — the check that actually catches the smuggling variant.
   - **`DataResult` cardinality is exact for the mode** — one entry in SMTP mode,
     one per successful `RCPT` in issue order in LMTP mode, duplicates included
     (§2b).
   - **`DataResult` and `error` are never both non-empty** (§2b).
   - A rejected `Rcpt` returns an `*smtp.Error` whose enhanced-code class agrees
     with its three-digit code (§3) — checked here so the framework's repair path
     is a backstop rather than the normal case.
   - **`Reset` is called on all seven paths**, with the right `ResetReason`, and
     `Reset(ResetNewMail)` precedes the replacing `Mail` (§2c).
   - `Close` is called exactly once, after the final `Reset`, and is idempotent.
   - **A backend holding transaction state releases it on every `Reset`** — the
     suite drives all seven paths and asserts the backend's own accounting
     returns to zero, which is the check that would have caught revision 1's
     optional-`Reset` defect.
   - **`SCRAMCredentials` does not authenticate.** The suite performs a lookup
     followed by a *failed* proof and asserts `CommitAuth` was never called and
     the backend did not consider the session authenticated (§2d).
   - **`CommitAuth` fires exactly once, on success only, before the `235`** —
     driven across all five mechanism shapes, plus the refusal, abort and
     internal-failure paths where it must not fire at all.
   - The early-return cases above are the conformance test for the *framework*
     more than for the backend, and they belong here because they are what a
     third-party backend author would otherwise discover in production.

### Stateful security tests

Parser fuzzing reaches none of these, and each is a known vulnerability class:

- **SMTP smuggling.** The published vectors — `<LF>.<LF>`, `<CR>.<CR><LF>`,
  `<CR><LF>.<CR>` and the rest — asserted to terminate nothing and to be
  rejected. The `DotUnstuffReader` position (§0) is the implementation; this is
  the test that it is actually reachable through the server's `DATA` path.
- **STARTTLS plaintext injection** (CVE-2011-0411): bytes buffered after the
  `STARTTLS` command must be discarded and the session state reset (§4).
- **Pipelining desynchronisation**: a command group whose last member is `DATA`,
  followed immediately by content; a group spanning `RSET`; a group spanning a
  rejected `RCPT`.
- **`BDAT` size handling — four cases, not "lying about its size".** Revision 1
  said "larger and smaller than the bytes that follow", and the "smaller" half was
  wrong: bytes after an exact chunk are legally the next pipelined `BDAT`
  command, which §4 now establishes. The real cases:

  | Case | Required behaviour |
  |---|---|
  | announced size exceeds the bytes that arrive | block until the data deadline, then fail. Never treat a short read as end-of-chunk |
  | bytes after exactly `n` octets form a valid next command | **legal.** Proceed |
  | bytes after exactly `n` octets do not form a valid command | command syntax failure, `500`, then the failed-BDAT state |
  | a failed chunk followed by further pipelined chunks | consume and discard them, per RFC 3030 §2 (§2a) |

  Plus `BDAT 0 LAST` on an empty transaction, `BDAT` before `MAIL`, `BDAT` after
  `DATA`, and a chunk size that overflows the announced-size parse.
- **Spool exhaustion** (§2a): a message that crosses `MaxSpoolBytes` mid-chunk
  gets `552 5.3.4` after the full announced chunk is consumed, not a disconnect;
  a spool write failure gets `451 4.3.0`; and the spool file is gone from the
  filesystem in both cases and after a panic.
- Disconnect mid-`DATA`, mid-`BDAT`, and after `354` with no content.
- Slow-loris: one byte per minute against every read deadline.
- Repeated failed authentication; `AUTH` after `AUTH`; `AUTH` mid-transaction.
- Resource exhaustion: maximum recipients, maximum transactions per connection,
  connection floods from one source.
- Goroutine and file-descriptor leak checks across all of the above.

---

## 8. Resource limits, and the threat model inversion

The client's threat model asked whether a hostile *server* could make it panic,
hang or allocate without bound. The server faces the same parsers from
**unauthenticated remote clients**, at connection rates a client never sees, on
the internet's most-scanned port.

**Pre-authentication limits are separate and much tighter.** RFC 5321 §4.5.3.2
gives the server side a 5-minute minimum for awaiting the next command; that is a
figure for an established, well-behaved session and is not the pre-greeting or
pre-`MAIL` budget.

| Bound | Why |
|---|---|
| command line length | RFC 5321 §4.5.3.1 sets 512 octets as the minimum a server must accept; the cap is configurable above it and enforced before allocation |
| path length | 256 octets minimum accepted, per `address.go`; configurable cap above |
| recipients per transaction | RFC 5321 §4.5.3.1 requires accepting at least 100; `452` above the configured cap, never a disconnect |
| message size | the `SIZE` advertisement is the contract; enforcement is independent of it, because a client may lie or omit `SIZE` entirely |
| `BDAT` chunk size | already bounded by `smtpwire.Limits.MaxBDATChunkSize`; the server needs its own, lower, pre-auth value |
| commands per connection, and bad commands per connection | an unauthenticated peer looping `NOOP` costs nothing to send |
| transactions per connection | the `LIMITS` `MAILMAX` advertisement should reflect the real bound |
| authentication attempts per connection, per account, per source | credential stuffing |
| concurrent connections, total and per source address | the cheapest denial of service there is |
| pre-greeting, pre-`EHLO` and pre-`AUTH` deadlines | separate from and much shorter than the §4.5.3.2 command timeout |
| total transaction wall time | a slow-loris `DATA` is legal-looking and unbounded |
| `DataResult` cardinality | §2b makes it exact; the framework validates rather than assuming the backend respected it |
| **total spool bytes per transaction** (`MaxSpoolBytes`) | §2a's accumulated BDAT chunks. Independent of `SIZE`, because a peer may omit or misstate it |
| **in-memory spool bytes** (`MaxSpoolMemoryBytes`) | the threshold above which a transaction spills to disk. Without it, `MaxSpoolBytes` alone is a per-connection memory amplification |
| **`MaxTotalSpoolBytes`, `MaxTotalSpoolMemoryBytes`, `MaxConcurrentSpools`** | one connection bounded at 64 MiB is fine; a thousand of them is a full disk, an exhausted heap, or an exhausted file-descriptor table. These are server-wide, reserved incrementally per chunk, and **required** when CHUNKING is enabled — `NewServer` refuses to start otherwise (§2a) |

**`smtpwire.Limits` is client-shaped and must grow, not be reused as-is.** Its
fields today are `MaxReplyLineLength`, `MaxReplyLines`, `MaxReplySize` and
`MaxBDATChunkSize` — three of the four bound *reading a reply*, which a server
never does. The package is `internal/`, so adding command-side fields is free and
breaks nobody; the note here is that the *name* is now ambiguous against
`smtpclient.Limits` (RFC 9422), and T16's move of the latter into `package smtp`
makes the collision worse, not better. T17 decides whether the wire-level type is
renamed or split by direction.

Defaults are **safe**, not permissive-with-a-note. The deadline mechanism in
`LineReader` is reusable; the values are not.

---

## 9. What this costs v1.0, and how `smtpserver` is versioned

### The exit criterion

Adding types to `package smtp` after v1.0 is additive and always allowed.
**Reshaping an existing type is not.** A vocabulary exercised in only one
direction can hold a type a server can consume but cannot naturally produce, and
no client-side review surfaces it, because the client is the direction that
works.

§0 shows this is not hypothetical for this repository — smaller than the
sibling's ~35 misplaced types, but non-empty and with a deadline:

- `smtpclient.Limits` / `ParseLimitsParam` are in the wrong package for a server
  to declare RFC 9422 limits.
- `AllowUnadvertisedParameters` is a client-only field on a struct a server-side
  parser would fill, and the options-struct direction question behind it is the
  one open item with no safe default.
- `TraceEvent` / `TraceDirection` / `Recipient` are shared shapes in a
  direction-specific package.

So **a bidirectional review of `package smtp` is a v1.0 exit criterion**
([T16](tasks/T16-bidirectional-vocabulary-audit.md), milestone M4). It is a
day's work today and a v2 otherwise.

### `smtpserver` versioning — APPROVED 2026-08-04

The versioning policy in `API-STABILITY.md` says v1.0 freezes the exported API,
without qualifying by package. Taken literally, `smtpserver` inherits the freeze
the moment it lands — which would freeze the backend abstraction, the hardest API
in this library, on its first commit, before a single third-party backend has been
written against it. That is the failure this project exists to avoid, relocated
one layer up.

**Decision: `smtpserver` is a nested module with its own `go.mod`, versioned
v0.x independently while the root module is v1.x.** Approved 2026-08-04 and
recorded as a real exception in `API-STABILITY.md`.

The alternative — same module with a carve-out enforced by scoping the `apidiff`
gate — is worse, and for a reason that has nothing to do with our tooling: an
`apidiff` scope is a gate on *us*, and changes nothing a user sees. Someone
importing a package from a module tagged v1 reasonably expects it not to break,
Go's compatibility guidance is built on that expectation, and no CI configuration
resets it. A doc comment claiming the package is exempt is a promise, which is the
mechanism this project distrusts everywhere else.

Two objections to the nested module do not survive contact:

- *Development ergonomics.* Go workspaces (`go.work`) exist to develop
  interdependent modules in one repository without committing `replace`
  directives.
- *The `go.sum` entry.* The zero-dependency rule exists because a `go.sum` entry
  is a stability liability **we do not control**. A self-referential entry on our
  own module is one we control entirely.

Real remaining costs, accepted: two tags per release, explicit version
coordination, and a deliberate bump of the root-module dependency.

Fallback if it proves unworkable: same-module with a documented stability
exception — needing its own separate approval.

The sibling repository reached the same recommendation independently. Two
libraries by one author diverging here would be a usability tax on anyone using
both, so consistency is a genuine, if secondary, argument.

---

## 10. Task breakdown

Specs exist for the tasks that do **not** depend on §2, because those can be
written honestly today. The rest get specs when §2 is approved.

| ID | Task | Milestone | Depends on | Spec |
|---|---|---|---|---|
| T15 | This document, and its approval | M5 | — | written |
| T16 | Bidirectional vocabulary audit of `package smtp` | **M4 — blocks v1.0** | T15 | written |
| T17 | Server-direction codec: command parsing, reply encoding, path parsing, `Received:` generation, SASL responder half | M6 | T15 | written |
| T18 | Server core: connection loop, state machine, capability descriptors, TLS, timeouts, **the §2a spool** | M6 | T17, §2 approved | after approval |
| T19 | Backend contract, `smtpserver/memory`, `smtpserver/backendtest` | M6 | T18 | after approval |
| T20 | Base command set and the extension floor, server side | M6 | T19 | after approval |
| T21 | Server extensions beyond the floor, incl. `ATRN` | M6 | T20 | after approval |
| T22 | Conformance, interop, server-side fuzzing, stateful security tests | M6 | T20 | after approval |
| T23 | API review, docs, examples, `smtpserver` release | M6 | T21, T22 | after approval |

T16 is the only one with a deadline. T17 is the bulk of the work and does not
wait on §2.

Revision 2 moves weight into T18: the spool is a lifecycle contract with its own
failure modes, its own resource bounds and its own security tests, not a buffer.
When T18's spec is written it should be scoped accordingly, and §2a's contract
table is the acceptance criterion.

### A note on file ownership

T17 extends `internal/smtpwire/**`, which T01 owns under `BOARD.md`, and
`internal/smtpsasl/**`, which T04 owns.

That rule exists to make *concurrent* work safe — it is a lock, and both tasks
finished. A completed task's lock passes to the task that supersedes it. The work
is also internal-only by construction: if it changes an exported signature it has
done something wrong, and `api_surface_test.go` plus the `apidiff` gate both say
so. Recorded here so the exception is deliberate rather than discovered in review.

---

## Appendix: claims verified against RFC text for this revision

Recorded because `CLAUDE.md` forbids working from recalled RFC numbers, and
because a reviewer should be able to see which claims were checked rather than
assumed.

| Claim | Source | Verified |
|---|---|---|
| LMTP MUST NOT be used on TCP port 25 | RFC 2033 §5 and Abstract | yes — *"As LMTP is a different protocol than SMTP, it MUST NOT be used on the TCP service port 25."* |
| LMTP replaces HELO/EHLO with LHLO and must not answer them positively | RFC 2033 §4.1 | yes — *"A LMTP server MUST NOT return a Positive Completion reply code to these commands. The 500 reply code is recommended."* |
| RFC 3848 registers the `with` keywords | RFC 3848, *ESMTP and LMTP Transmission Types Registration* | yes — ESMTPA, ESMTPS, ESMTPSA, LMTP, LMTPA, LMTPS, LMTPSA |
| Server must not lose the TCP input buffer | RFC 2920 §3.2 req. 9 | yes — *"MUST NOT flush or otherwise lose the contents of the TCP input buffer under any circumstances whatsoever."* |
| Server must flush pending responses when the input buffer empties | RFC 2920 §3.2 req. 7 | yes |
| Server must respond in command order | RFC 2920 §3.2 req. 1 | yes |
| Enhanced-code class must agree with the reply code | RFC 2034 §4 | yes — *"a 2xx response must incorporate a 2.X.X code, a 4xx response must incorporate a 4.X.X code, and a 5xx response must incorporate a 5.X.X code."* |
| Enhanced codes are omitted from the greeting and HELO/EHLO replies | RFC 2034 §3 | yes |

Added in revision 2, all fetched and quoted while writing it:

| Claim | Source | Verified |
|---|---|---|
| A reply is required for **each** successful BDAT chunk | RFC 3030 §2 | yes — *"A 250 response MUST be sent to each successful BDAT data block within a mail transaction."* |
| A failing server must consume the announced chunk before replying | RFC 3030 §2 | yes — *"the receiver-SMTP MUST accept and discard the associated message data before sending the appropriate 5XX or 4XX code."* |
| Pipelined chunks after a failure must be consumed and discarded | RFC 3030 §2 | yes — *"MUST be prepared to accept and discard additional BDAT chunks already in the pipeline after the failed BDAT."* This is also what proves BDAT is **not** a synchronisation point |
| Post-failure state is indeterminate; RSET is required | RFC 3030 §2 | yes — *"A RSET command MUST be issued to clear the transaction before additional commands may be sent."* |
| BINARYMIME requires CHUNKING | RFC 3030 §3 | yes — *"The BINARYMIME service extension can only be used with the 'CHUNKING' service extension."* |
| STARTTLS must be last in a pipelined group | RFC 3207 §4 | yes — *"the STARTTLS command must be the last command in a group."* |
| Extension commands are synchronisation points by default | RFC 2920 §3.1 | yes — *"Additional commands added by other SMTP extensions may only appear as the last command in a group unless otherwise specified by the extensions that define the commands."* This is why revision 1's framing of RFC 2920 vs RFC 3207 as contradictory was wrong |
| LMTP: one reply per successful RCPT, in issue order | RFC 2033 §4.2 | yes — *"the server returns one reply for each previously successful RCPT command in the mail transaction, in the order that the RCPT commands were issued."* |
| LMTP: duplicates each get their own reply | RFC 2033 §4.2 | yes — *"Even if there were multiple successful RCPT commands giving the same forward-path, there must be one reply for each successful RCPT command."* |

Added in revision 3, all fetched and quoted while writing it:

| Claim | Source | Verified |
|---|---|---|
| OAUTHBEARER returns a JSON error document and fails *after* a further round trip | RFC 7628 §3.2.2–3.2.3 | yes — the server *"returns an error result in JSON format and fails the authentication"*; *"The client MUST then send either an additional client response consisting of a single %x01 (control A) character to the server in order to allow the server to finish the exchange or a SASL abort message"* |
| Why that round trip exists | RFC 7628 §3.2.3 | yes — *"SASL explicitly prohibits additional information in an unsuccessful authentication outcome"* |
| LMTP: `DATA` with no successful RCPT MUST fail `503` | RFC 2033 §4.2 | yes — *"when there have been no successful RCPT commands in the mail transaction, the DATA command MUST fail with a 503 reply code."* This is what makes §2b's `N == 0` unreachable |
| SMTP: the same case MAY be refused | RFC 5321 §3.3 | yes — *"If there was no MAIL, or no RCPT, command, or all such commands were rejected, the server MAY return a 'command out of sequence' (503) or 'no valid recipients' (554) reply"* |
| The final DATA reply follows receipt of the complete end-of-mail indication | RFC 5321 §3.3 | yes — which is why §2a's resynchronisation cannot be delegated to the backend |

Claims **not** independently re-verified for this revision, taken from
`docs/RFC-COVERAGE.md` and the existing code's own citations: RFC 5321 §4.1.2
(source routes), §4.4 (`Received:`), §4.5.1 (mandatory commands), §4.5.3.1
(sizes), §4.5.3.2 (timeouts); RFC 3207 §4.2 (STARTTLS state discard); RFC 6409
(submission); RFC 4954 (AUTH sequencing and the `454 4.7.0` temporary-failure
code); RFC 5321 §4.2.2 / RFC 3463 (the `452 4.3.1` and `552 5.3.4` pairings used
in §2a). A reviewer wanting a second gate should start there.
