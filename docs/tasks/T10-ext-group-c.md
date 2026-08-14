# T10 — Extensions group C: legacy & niche

**Agent:** `extensions` · **Milestone:** M3 · **Depends on:** T08

**Owns:** `smtpclient/ext_c_*.go`

## Scope

Everything remaining in the IANA registry. The bar here is different from T08 and
T09: **implement the parse path so an EHLO reply advertising these never breaks
the client.** Full command support is best-effort, and several rows will stay
`deferred` by design.

| Capability | RFC | Intent |
|---|---|---|
| ETRN | 1985 | implement — queue-start command, still deployed |
| ATRN | 2645 | implement — authenticated TURN / ODMR |
| NO-SOLICITING | 3865 | implement `SOLICIT=` parameter |
| MTRK | 3885 | implement `MTRK=` certifier and optional timeout parameter |
| SUBMITTER | 4405 | implement `SUBMITTER=` parameter |
| CONPERM | 4141 | implement — content conversion permission |
| CONNEG | 4141 | implement — content negotiation |
| CHECKPOINT | 1845 | defer — no known server support |
| VERB | — | defer — Eric Allman, non-RFC, sendmail verbose mode |
| ONEX | — | defer — Eric Allman, non-RFC, one transaction only |
| SEND / SOML / SAML | 821 | defer — removed by RFC 5321 |
| TURN | 821 | defer — removed by RFC 5321 for security reasons |

## The rule that makes `deferred` safe

A `deferred` row still requires that **the keyword parse and reach the caller
through the extension accessor**. Deferred means "we do not implement the
command", never "we break on the keyword". A server advertising `VERB` must not
degrade anything.

This is the payoff for `API-STABILITY.md` §1a. If any keyword in this table
requires a code change to be *observable*, the extension table was modelled wrong
and that is an escalation to `api-guardian`, not a patch here.

## Notes on the ones worth care

- **ETRN (RFC 1985)** is genuinely deployed on dial-up-heritage and backup-MX
  configurations. It has its own reply-code vocabulary (`250`, `251`, `252`,
  `253`, `458`, `459`) that does not map onto the usual meanings — read the RFC's
  table rather than assuming.
- **ATRN (RFC 2645)** reverses the client and server roles on the same
  connection. The client becomes the receiver. That does not fit this library's
  session model, and pretending otherwise would distort it. Implement the command
  and surface the role reversal as an explicit, documented dead end — the
  connection is handed back or closed. **Do not** build a receiving path into the
  client to accommodate it; if ATRN is ever fully supported it belongs to the
  server framework (T15).
- **SEND / SOML / SAML / TURN** were removed by RFC 5321. `TURN` specifically was
  removed for security reasons. Never generate them. Parsing the keyword is the
  entire deliverable.

## Escalation

If an extension here appears to need a breaking change to a core type, stop and
flag `api-guardian`. Group C exists partly as a stress test of the M0 type
decisions — a break surfacing here is a genuine finding about T02, not an
inconvenience to route around.

## Testing

- Extension-accessor test asserting every keyword in the table above, including
  every `deferred` one, is observable without a code change.
- Scripted server per implemented command.
- Interop: expect heavy skipping. Postfix and Exim advertise `ETRN`; most of the
  rest will not appear anywhere in the matrix, and that is the expected result,
  recorded rather than chased.

## Done when

`docs/RFC-COVERAGE.md` has no `planned` rows outside the `deferred` set — the M3
exit criterion; every keyword is observable; `api-guardian` has approved the
exported surface.
