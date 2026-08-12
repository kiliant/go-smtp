package smtpclient

import (
	"strconv"
	"strings"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

// TraceDirection reports which side of the RFC 5321 conversation produced a
// traced line. It is an alias for smtp.TraceDirection: an observability shape
// is not direction-specific vocabulary, and two incompatible direction types in
// one process — one from this package, one from a server — would be a tax on
// anyone running both halves (docs/API-STABILITY.md §10). The alias preserves
// type identity, so existing callers are unaffected.
type TraceDirection = smtp.TraceDirection

// The RFC 5321 trace directions, aliased from package smtp for the reason given
// on TraceDirection. Aliasing constants preserves both type and value identity.
const (
	// TraceSent marks an RFC 5321 line this client wrote to the server.
	TraceSent = smtp.TraceSent
	// TraceReceived marks an RFC 5321 line this client read from the server.
	TraceReceived = smtp.TraceReceived
)

// TraceEvent is one RFC 5321 protocol line exchanged with the server, as handed
// to ClientOptions.Trace. It is an alias for smtp.TraceEvent; see
// TraceDirection for why the type lives there.
//
// Line never carries a trailing CRLF. A reply keeps its three-digit code and,
// when multiline, carries every line joined by "\n" behind that single code —
// the shape smtp.Error's Text uses, with the code restored.
type TraceEvent = smtp.TraceEvent

// redactedPayload replaces every SASL payload in a trace. It is a fixed
// string rather than a length-preserving mask because the length of a
// credential is itself information.
const redactedPayload = "[redacted]"

// traceCommandLine renders cmd as it appears on the wire, with any SASL
// payload removed.
//
// The mechanism name survives — it is not a secret, and it is the single most
// useful thing in an AUTH trace — but every argument after it is redacted.
// That covers the initial response carried in the AUTH command itself, which
// is where PLAIN puts the password.
func traceCommandLine(cmd queuedCommand) string {
	var b strings.Builder
	b.WriteString(cmd.verb)
	redact := strings.EqualFold(cmd.verb, "AUTH")
	for i, arg := range cmd.args {
		b.WriteByte(' ')
		// The mechanism name is args[0]; everything after it is payload.
		if redact && i > 0 {
			b.WriteString(redactedPayload)
			continue
		}
		b.WriteString(arg)
	}
	return b.String()
}

// traceReplyLine renders reply as it appears on the wire, with any SASL
// payload removed.
//
// A 334 carries the server's challenge. It is not a client credential, but it
// is part of the AUTH exchange: for the SCRAM family it echoes the client
// nonce and carries the server signature, and redacting the whole exchange is
// the conservative reading of the rule that AUTH payloads never reach a log.
func traceReplyLine(reply smtpwire.Reply) string {
	if reply.Code == 334 {
		return "334 " + redactedPayload
	}
	if len(reply.Lines) == 0 {
		return strconv.Itoa(reply.Code) + " " + reply.Text
	}
	var b strings.Builder
	// The code is written once, at the front: smtpwire strips the per-line
	// "nnn-"/"nnn " prefix, and a protocol trace without the code is not much
	// of a protocol trace.
	b.WriteString(strconv.Itoa(reply.Code))
	b.WriteByte(' ')
	for i, line := range reply.Lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	return b.String()
}

// trace delivers one event to the caller's hook, if any.
//
// Message content is deliberately never traced: DATA and BDAT payloads are
// the caller's mail, they are unbounded, and a trace hook is not a place to
// stream them. Only commands and replies pass through here.
func (c *connection) trace(direction TraceDirection, line string) {
	hook := c.options.Trace
	if hook == nil {
		return
	}
	hook(TraceEvent{Direction: direction, Line: line})
}
