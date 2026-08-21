// Package smtpserver implements RFC 5321 SMTP and RFC 2033 LMTP server
// protocol handling.
//
// The package is a nested v0.x module so its backend contract can mature
// independently of the stable v1 client module. It provides protocol framing,
// state management, TLS, capability advertisement, bounded CHUNKING storage,
// and exact SMTP/LMTP result handling. Storage and delivery policy remain the
// caller's responsibility.
package smtpserver
