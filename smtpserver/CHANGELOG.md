# Changelog

All notable changes to the `github.com/kiliant/go-smtp/smtpserver` module are
documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and version numbers follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
This module is versioned independently from the stable root client module.

## [Unreleased]

## [0.1.0] - 2026-08-21

The first server-module release. The backend contract is deliberately v0.x so
incompatible changes remain possible while third-party backend experience is
collected; any such change will be labeled explicitly in this changelog.

### Added

- **Exported API:** added `Server`, `ServerOptions`, `NewServer`, `Serve`,
  `Shutdown`, their nil-default options, and `ErrorEvent` for bounded RFC 5321
  SMTP and RFC 2033 LMTP listener lifecycle management.
- **Exported API:** added the concrete `Backend` and `Session` function-field
  contract, `ConnInfo`, open `Mode`, reset reasons, per-command option structs,
  and the required context-first `Mail`, `Rcpt`, `Data`, `Reset`, and `Close`
  handlers. New extensions add fields instead of methods to an exported
  interface.
- **Exported API:** added RFC 4954 authentication verification and commit
  callbacks, open mechanism credentials/challenges, RFC 7628 OAuth failure
  documents, and RFC 5802/RFC 7677 SCRAM verifier keys.
- **Exported API:** added open parameter-extension declarations, RFC 9422 LIMITS
  advertisement, RFC 4141 successful RCPT continuation lines, and RFC 2645 ATRN
  connection takeover.
- **Exported API:** added `smtpserver/memory` with `Sink`, `Message`,
  nil-default `Options`, snapshots, and a reusable backend. It is non-durable
  and supported only for tests and development, not production storage.
- **Exported API:** added `smtpserver/backendtest.Run` and its nil-default
  `Options` for checking required handlers, reset/close lifecycle, and exact
  SMTP/LMTP result cardinality.
- Added RFC 5321 core commands, RFC 2033 LMTP, RFC 3207 STARTTLS, RFC 2920
  PIPELINING, RFC 4954 AUTH, RFC 3030 CHUNKING/BINARYMIME, and the implemented
  Group A–C extensions recorded in `docs/RFC-COVERAGE.md`.
- Added bounded instance-wide connection, per-source connection, transaction,
  recipient, message, spool-memory, spool-disk, and concurrent-spool limits.
- Added runnable examples for a minimal sink, an authenticated TLS submission
  listener, LMTP per-recipient results, the five-handler backend floor, and an
  smtpclient test double.

### Security

- Rejects ambiguous bare-CR/bare-LF DATA terminators, preserves exact BDAT
  framing after failures, discards prefetched plaintext across STARTTLS, and
  redacts SASL payloads and message content from trace callbacks.
- Applies per-command and content deadlines, bounded line/content parsing,
  panic-safe backend cleanup, and resource release across disconnect, shutdown,
  authentication, DATA, and BDAT failure paths.

## Release process

The authoritative nested-module tag and compatibility procedure is documented
in [`.github/RELEASING.md`](../.github/RELEASING.md). Server tags use the
directory prefix, for example `smtpserver/v0.1.0`; they do not advance the root
module's `apidiff` baseline.

[Unreleased]: https://github.com/kiliant/go-smtp/compare/smtpserver/v0.1.0...HEAD
[0.1.0]: https://github.com/kiliant/go-smtp/releases/tag/smtpserver%2Fv0.1.0
