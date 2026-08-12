package smtpserver

import (
	"context"
	"crypto/tls"
	"net"
)

// Mode identifies the protocol spoken by one listener. SMTP and LMTP are the
// two modes defined today. Mode is string-backed so a caller receiving a future
// mode can preserve and report it rather than treating it as impossible.
type Mode string

const (
	// ModeSMTP selects RFC 5321 SMTP.
	ModeSMTP Mode = "smtp"
	// ModeLMTP selects RFC 2033 LMTP.
	ModeLMTP Mode = "lmtp"
)

// Backend is shared by every connection and must be safe for concurrent use.
// NewSession is required. Future ESMTP extensions add fields to Backend or
// Session; they never add methods to an exported interface.
//
// Callers constructing a Backend literal must use keyed fields.
type Backend struct {
	// NewSession is called once for each accepted connection, before the
	// protocol greeting is written. Returning an error refuses that
	// connection; a protocol rejection should be an *smtp.Error.
	NewSession func(ctx context.Context, conn *ConnInfo, opts *NewSessionOptions) (*Session, error)

	_ struct{}
}

// ConnInfo describes one accepted connection without exposing its net.Conn.
// Exposing the transport would let a backend interfere with protocol framing.
// TLSState reports the current state and therefore changes after STARTTLS; it
// returns nil while the connection is plaintext.
//
// Callers constructing a ConnInfo literal must use keyed fields.
type ConnInfo struct {
	// Mode is the listener's fixed SMTP or LMTP mode.
	Mode Mode
	// LocalAddr is the server-side transport address.
	LocalAddr net.Addr
	// RemoteAddr is the peer transport address.
	RemoteAddr net.Addr
	// TLSState returns a copy of the current TLS state. It is always non-nil
	// as a function on values supplied by the framework; its result is nil
	// before TLS is active.
	TLSState func() *tls.ConnectionState

	_ struct{}
}

// NewSessionOptions controls one Backend.NewSession call. Nil means defaults.
//
// Callers constructing a NewSessionOptions literal must use keyed fields.
type NewSessionOptions struct {
	_ struct{}
}
