package smtp

// TraceDirection reports which side of the RFC 5321 conversation produced a
// traced line. It is direction-neutral vocabulary: a client names the peer's
// lines "received" and its own "sent", and a server does the same from its own
// end, so one type serves both — see docs/API-STABILITY.md §10.
//
// It is a string type rather than an enumeration for the reason given in
// docs/API-STABILITY.md §1: a caller must be able to switch on the two
// directions that exist today without the type becoming closed to a third.
// Callers must therefore treat an unrecognised direction as "something else
// happened", not as impossible.
type TraceDirection string

const (
	// TraceSent marks an RFC 5321 line the local end wrote to its peer.
	TraceSent TraceDirection = "sent"
	// TraceReceived marks an RFC 5321 line the local end read from its peer.
	TraceReceived TraceDirection = "received"
)

// TraceEvent is one RFC 5321 protocol line exchanged with the peer, as handed
// to a trace hook such as smtpclient.ClientOptions.Trace.
//
// Line never carries a trailing CRLF. A reply keeps its three-digit code and,
// when multiline, carries every line joined by "\n" behind that single code —
// the shape Error's Text uses, with the code restored.
//
// Callers receive TraceEvent rather than build it, but a test may construct
// one: do so with keyed fields. The trailing unexported field makes an
// unkeyed literal a compile error, so a future field can be added without
// breaking callers. See docs/API-STABILITY.md §7.
type TraceEvent struct {
	// Direction reports whether the line was sent or received.
	Direction TraceDirection
	// Line is the protocol line, with SASL payloads already redacted.
	Line string

	_ struct{}
}
