# Changelog

All notable changes to this project are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and version numbers follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
There are no release tags yet, so the complete project history remains under
Unreleased. Conventional Commit subjects are the input; entries below describe
user-visible outcomes rather than repeating commit messages.

## [Unreleased]

### Added

- **Exported API:** added the shared `smtp` vocabulary: open extension and
  parameter value types, `MailOptions`/`RcptOptions` with `Extra []Param`,
  enhanced status codes, `*smtp.Error`, xtext encoding, and per-recipient
  `RcptResult`/`DataResult` values.
- **Exported API:** added `smtpclient.Client`, caller-supplied connection
  construction, a dial hook, separate dial address and TLS server identity,
  context-aware commands, nil-default option structs, and cached extension
  access.
- **Exported API:** added STARTTLS, implicit TLS, PIPELINING, Message Submission,
  LMTP, SASL authentication, trace callbacks with mandatory credential
  redaction, and SMTP transaction commands.
- **Exported API:** added SIZE, 8BITMIME, SMTPUTF8, CHUNKING/BDAT, BINARYMIME,
  DSN, DELIVERBY, FUTURERELEASE, MT-PRIORITY, RRVS, REQUIRETLS, LIMITS, BURL,
  ETRN, ATRN, NO-SOLICITING, MTRK, SUBMITTER, CONPERM, and CONNEG support.
- Added a Podman-backed interoperability matrix for Postfix, Exim, Stalwart,
  maddy, Mailpit, Dovecot LMTP, GreenMail, and optionally emulated Apache James.
- Added adversarial-server tests and discovered fuzz coverage for protocol,
  authentication, option, LMTP, and trace-redaction boundaries.
- Added runnable, CI-compiled examples for STARTTLS submission, implicit TLS,
  partial recipient rejection, DATA/BDAT streaming, LMTP, DSN, and unmodelled
  ESMTP parameters.

### Changed

- **Breaking exported API change (pre-v1):** changed `Client.BURL` from returning
  only `error` to returning `(smtp.DataResult, error)`. BURL with `LAST` is a
  content-completion command and must preserve the same per-recipient result
  shape as DATA and future content transports.
- **Exported API:** placed RRVS on recipient options, matching RFC 7293. The
  earlier sender-level field remains source-compatible but is rejected with an
  actionable error.
- Normalized DATA line endings to CRLF and reject bare-LF terminators; BDAT
  remains byte-exact.
- REQUIRETLS now requires an established TLS session before its MAIL parameter
  can be sent.
- The public API freeze audit now mechanically enforces contexts, option
  structs, open sets, internal-package isolation, keyed literals, documentation,
  interface policy, exemption maps, and per-recipient content results.

### Fixed

- Preserved session state across transactions and reset SMTPUTF8/BINARYMIME
  state at transaction boundaries.
- Required the RFC-defined final success code after DATA and corrected
  pipelining synchronization on the production path.
- Hardened authentication selection, SCRAM-PLUS preference, SASLprep tables,
  extension validation, reply-size enforcement, and LMTP final-reply handling.
- Corrected interop sink reset behavior, Dovecot user lookup, published-port
  parsing, maildir ordering, and JVM server verification.

### Security

- Trace callbacks never receive SASL payloads or message content.
- Malformed server input is bounded and fuzzed; cancellation after an in-flight
  command poisons the connection instead of risking protocol desynchronization.

## Release process

The authoritative tag, release-candidate, compatibility, and pre-tag checklist
is [`.github/RELEASING.md`](.github/RELEASING.md). Every future release moves
the relevant Unreleased entries into a dated version section; every exported
API change must continue to be labeled explicitly.
