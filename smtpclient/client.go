// Package smtpclient implements an ESMTP and LMTP client.
//
// A Client speaks to a caller-supplied endpoint. It intentionally does not
// perform MX lookup, MTA-STS, DANE, or any other transport-policy work; those
// belong to the post-v1 delivery layer described in docs/ARCHITECTURE.md.
package smtpclient

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"time"
)

// ClientOptions configures connection construction. A nil *ClientOptions is
// valid and uses the documented defaults.
//
// Callers constructing a ClientOptions literal must use keyed fields.
type ClientOptions struct {
	// LMTP selects RFC 2033 mode. The initial extension negotiation uses LHLO
	// and never falls back to HELO; an SMTP peer is therefore rejected rather
	// than silently treated as LMTP.
	LMTP bool
	// Address is the network address passed to DialContext, normally
	// "host:port". It is deliberately separate from TLSServerName: delivery
	// code may connect to an address selected for an MX while verifying the
	// certificate for that MX hostname.
	Address string
	// TLSServerName is the TLS certificate identity. If empty, the hostname
	// part of Address is used when it is available.
	TLSServerName string
	// TLSConfig supplies additional TLS configuration. It is cloned before
	// use, so constructing or using a Client never mutates caller state.
	TLSConfig *tls.Config
	// ImplicitTLS performs TLS before reading the server greeting (the RFC
	// 8314 submission service convention, commonly port 465).
	ImplicitTLS bool
	// InsecureSkipVerify disables TLS certificate verification. It defaults
	// to false and should be used only for controlled test infrastructure.
	InsecureSkipVerify bool
	// Identity is the client name sent in EHLO. Empty selects the local host
	// name, falling back to "localhost".
	Identity string
	// DialContext, when non-nil, establishes the connection. It exists so a
	// caller that selected an endpoint can supply its own dialer.
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	// GreetingTimeout bounds reading the initial 220 greeting. Zero uses RFC
	// 5321's five-minute minimum.
	GreetingTimeout time.Duration
	// MailTimeout bounds MAIL replies. Zero uses RFC 5321's five-minute
	// minimum.
	MailTimeout time.Duration
	// RCPTTimeout bounds RCPT replies. Zero uses RFC 5321's five-minute
	// minimum. It is separate from MailTimeout so either command's timeout can
	// be overridden without changing the other.
	RCPTTimeout time.Duration
	// DataCommandTimeout bounds the 354 response to DATA. Zero uses two
	// minutes.
	DataCommandTimeout time.Duration
	// DataBlockTimeout bounds each blocked read/write while transferring DATA
	// content. Zero uses three minutes.
	DataBlockTimeout time.Duration
	// DataFinalTimeout bounds the final DATA reply. Zero uses ten minutes.
	DataFinalTimeout time.Duration
	// Trace, when non-nil, receives every command sent and every reply
	// received, in order, as the conversation happens. It exists so a caller
	// can log or diagnose a session without this package choosing a logging
	// library — see docs/API-STABILITY.md §4 on why this is a callback and
	// not an exported interface.
	//
	// SASL payloads are redacted before the hook is called: the arguments
	// after the mechanism name in AUTH, every bare SASL continuation
	// response, and the server's 334 challenges. A caller cannot switch that
	// off, because a trace hook that can leak a password is a trace hook that
	// eventually does.
	//
	// Message content is never traced — DATA and BDAT payloads do not pass
	// through here.
	//
	// The hook is called synchronously on the goroutine driving the
	// connection, while that goroutine holds the operation lock. It must not
	// block and must not call back into the Client, which would deadlock.
	Trace func(TraceEvent)

	_ struct{}
}

// StartTLSOptions configures a STARTTLS upgrade. A nil *StartTLSOptions is
// valid and inherits TLS configuration from ClientOptions.
//
// Callers constructing a StartTLSOptions literal must use keyed fields.
type StartTLSOptions struct {
	// TLSConfig overrides the TLS configuration supplied at construction.
	TLSConfig *tls.Config
	// ServerName overrides ClientOptions.TLSServerName for this handshake.
	ServerName string
	// InsecureSkipVerify explicitly disables certificate verification for
	// this handshake. It defaults to false.
	InsecureSkipVerify bool

	_ struct{}
}

// Client is one SMTP session. It is safe for concurrent callers: commands are
// serialized through one FIFO reply queue because SMTP replies are ordered,
// not multiplexed.
//
// Cancellation: SMTP has no command abort. If a context is cancelled after a
// command has reached the wire, Client closes and poisons the connection and
// returns context.Canceled rather than retaining a desynchronized session.
// Commands added by later protocol tasks refer to this contract.
type Client struct {
	conn *connection
}

var errNilClient = errors.New("smtpclient: nil Client")

// Close sends QUIT when the session is usable, then closes its underlying
// connection. It is safe to call more than once and implements io.Closer.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	c.conn.opMu.Lock()
	defer c.conn.opMu.Unlock()
	if c.conn.closed() {
		return nil
	}
	// Close has no context because it matches io.Closer. A short best-effort
	// QUIT avoids turning cleanup into an RFC-sized wait while still sending
	// the protocol shutdown in normal operation.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	replies, err := c.conn.pipeline.executeLocked(ctx, []queuedCommand{{
		verb: "QUIT", syncPoint: true, timeout: 5 * time.Second,
	}})
	if err == nil && len(replies) == 1 {
		err = unexpectedReply("QUIT", replies[0], c.conn.enhancedStatusCodes(), 221)
	}
	c.conn.poison()
	return err
}
