// Package smtp defines the shared vocabulary for ESMTP and LMTP clients:
// reply and enhanced status codes, EHLO extension keywords, MAIL/RCPT
// esmtp-params, the per-recipient result of submitting message content, and
// the single error type used throughout this module.
//
// This package performs no I/O and imports nothing from
// github.com/kiliant/go-smtp — see TestNoModuleImports. That is deliberate:
// it is the shared vocabulary that the smtpclient package, a future
// smtpdeliver package and a future server framework all build on, and none
// of those may depend on each other. See docs/ARCHITECTURE.md.
//
// Protocol baseline: RFC 5321 (SMTP, with the ESMTP extension mechanism as
// registered by RFC 5321 §2.2 / RFC 1869), RFC 2033 (LMTP), and RFC 6409
// (Message Submission). The authoritative keyword-to-RFC mapping lives in
// docs/RFC-COVERAGE.md at the module root; every RFC number cited in this
// package's doc comments is checked against that file, not recalled from
// memory.
//
// Scope: smtp and smtpclient speak to caller-supplied endpoints. They do not
// resolve MX records or implement MTA-STS (RFC 8461) or DANE (RFC 7672); those
// transport-policy concerns belong in the post-v1 smtpdeliver package described
// by docs/ARCHITECTURE.md. They also do not compose MIME (RFC 2045–2049) or sign
// DKIM (RFC 6376); callers should use dedicated message-building and signing
// libraries before handing the resulting bytes to smtpclient.
package smtp
