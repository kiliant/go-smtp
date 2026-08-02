# T04 — Authentication & SASL

**Agent:** `client-core` · **Milestone:** M1 · **Depends on:** T03

**Owns:** `smtpclient/auth.go`, `internal/smtpsasl/**`, `internal/saslprep/**`,
`internal/unicodenorm/**`

## Goal

`AUTH` (RFC 4954) and the SASL mechanisms behind it, with credential handling
that is correct rather than merely functional.

## Deliverables

### AUTH (RFC 4954)

- `AUTH <mechanism>` with an **initial response** in the command when the
  mechanism supplies one and it fits — the SMTP equivalent of SASL-IR. A zero
  length initial response is `=`, not an empty argument.
- Base64 challenge/response rounds via `334` continuations.
- Cancellation with `*` on a mechanism-level abort.
- The mechanism list comes from the `AUTH` EHLO keyword's parameters. A server
  advertising `AUTH` with no parameters, and the historical `AUTH=` form with an
  equals sign, both occur — handle both.
- Re-issue `EHLO` after successful authentication when the server's extension set
  may change (some servers advertise more post-auth).
- The `AUTH=` **MAIL parameter** (RFC 4954 §5) is a separate thing from the AUTH
  command: it asserts the authenticated identity of the original submitter.
  Implement it as a `MailOptions` field.

### Mechanisms (`internal/smtpsasl`)

| Mechanism | RFC | Note |
|---|---|---|
| `PLAIN` | 4616 | |
| `LOGIN` | — | No RFC, never standardised, still ubiquitous. Implement it and say why in the doc comment. |
| `CRAM-MD5` | 2195 | Legacy, still common |
| `SCRAM-SHA-1`, `SCRAM-SHA-256` | 5802, 7677 | |
| `SCRAM-SHA-*-PLUS` | 5802, 7677 | Channel binding to the TLS exporter via `tls.ConnectionState` |
| `EXTERNAL` | 4422 | Client certificate |
| `OAUTHBEARER` | 7628 | |
| `XOAUTH2` | — | De-facto; Gmail and Outlook |

Mechanism selection is caller-controllable. The default order prefers channel-
bound and hash-based mechanisms over password-revealing ones; a caller must be
able to pin a single mechanism.

### Security requirements — not optional

- **Refuse to send credentials over an unencrypted connection by default.** An
  explicit, obviously-named opt-in is required to override. T11 asserts this.
- Credentials never appear in wire tracing. `AUTH` payloads are redacted. T11
  asserts this too.
- Zero credential buffers where the language permits it.

### SASLprep (`internal/saslprep`, `internal/unicodenorm`)

RFC 4013 over RFC 3454, with NFKC from generated tables.

- **Opt-in**, not default. Deployed servers overwhelmingly compare raw octets,
  and preparing a password that the server stored unprepared turns a working
  login into a failing one. Document that trade-off on the option.
- The generated tables live in the tree; generators in
  `internal/{unicodenorm,saslprep}/gen/`, run by hand, never at build time. A
  `go generate` that reaches the network during a build reintroduces exactly the
  fragility the zero-dependency rule exists to prevent.
- Table versions differ deliberately: normalisation tracks the toolchain's
  `unicode.Version`, while the RFC 3454 tables stay frozen at Unicode 3.2 as that
  RFC requires. RFC 3454 §7's assigned/unassigned split exists precisely so a
  stringprep profile need not follow new Unicode releases.

These packages duplicate near-identical code in the sibling `go-imap` repository.
That is intended — see CLAUDE.md. Do not attempt a shared module.

## Testing

- Per-mechanism vectors from the RFCs.
- SCRAM-PLUS channel binding against a real TLS connection, not a stub.
- `FuzzSASLprep`, `FuzzSCRAMParse`, `FuzzNormalise`.
- The two SASLprep diagnostic accounts in `docs/INTEROP.md` discriminate whether
  preparation actually happened; a test that passes with preparation disabled is
  asserting nothing.

## Done when

Every mechanism has RFC vectors passing; cleartext-credential refusal and
tracing redaction are tested; `go test -race` passes; `api-guardian` has approved
the options surface.
