package smtpclient

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/kiliant/go-smtp/internal/smtpwire"
)

// ResetOptions configures Reset. A nil *ResetOptions is valid.
//
// Callers constructing a ResetOptions literal must use keyed fields.
type ResetOptions struct{ _ struct{} }

// NoopOptions configures Noop. A nil *NoopOptions is valid.
//
// Callers constructing a NoopOptions literal must use keyed fields.
type NoopOptions struct{ _ struct{} }

// QuitOptions configures Quit. A nil *QuitOptions is valid.
//
// Callers constructing a QuitOptions literal must use keyed fields.
type QuitOptions struct{ _ struct{} }

func (c *Client) command(ctx context.Context, verb string, args []string, timeout func() time.Duration, allowed ...sessionState) (smtpwire.Reply, error) {
	if c == nil || c.conn == nil {
		return smtpwire.Reply{}, errNilClient
	}
	if err := ctx.Err(); err != nil {
		return smtpwire.Reply{}, err
	}
	c.conn.opMu.Lock()
	defer c.conn.opMu.Unlock()
	return c.commandLocked(ctx, verb, args, timeout, allowed...)
}

// commandLocked runs one command while the caller owns conn.opMu.
func (c *Client) commandLocked(ctx context.Context, verb string, args []string, timeout func() time.Duration, allowed ...sessionState) (smtpwire.Reply, error) {
	c.conn.mu.Lock()
	state := c.conn.state
	c.conn.mu.Unlock()
	if err := invalidState(verb, state, allowed...); err != nil {
		return smtpwire.Reply{}, err
	}
	replies, err := c.conn.pipeline.executeLocked(ctx, []queuedCommand{{verb: verb, args: args, syncPoint: isSyncPoint(verb), timeout: timeout()}})
	if err != nil {
		return smtpwire.Reply{}, err
	}
	return replies[0], nil
}

// Reset abandons the current mail transaction using RSET (RFC 5321 §4.1.1.5).
// It cannot recover a DATA transfer that was cancelled after content reached
// the wire; see Client's cancellation contract.
func (c *Client) Reset(ctx context.Context, opts *ResetOptions) error {
	_ = opts
	if c == nil || c.conn == nil {
		return errNilClient
	}
	c.conn.opMu.Lock()
	defer c.conn.opMu.Unlock()
	reply, err := c.commandLocked(ctx, "RSET", nil, c.conn.mailTimeout, stateTransaction)
	if err != nil {
		return err
	}
	if err := unexpectedReply("RSET", reply, c.conn.enhancedStatusCodes(), 250); err != nil {
		return err
	}
	c.conn.mu.Lock()
	if c.conn.state != stateClosed {
		c.conn.state = c.conn.transactionBase
		c.conn.recipients = nil
	}
	c.conn.mu.Unlock()
	return nil
}

// Noop verifies that the server is responsive using NOOP (RFC 5321 §4.1.1.9).
func (c *Client) Noop(ctx context.Context, opts *NoopOptions) error {
	_ = opts
	if c == nil || c.conn == nil {
		return errNilClient
	}
	reply, err := c.command(ctx, "NOOP", nil, c.conn.mailTimeout, stateGreeted, stateTLS, stateAuthenticated, stateTransaction)
	if err != nil {
		return err
	}
	return unexpectedReply("NOOP", reply, c.conn.enhancedStatusCodes(), 250)
}

// Quit sends QUIT (RFC 5321 §4.1.1.10) and closes the connection regardless of
// the reply. Close is equivalent cleanup for callers that need io.Closer.
func (c *Client) Quit(ctx context.Context, opts *QuitOptions) error {
	_ = opts
	if c == nil || c.conn == nil {
		return errNilClient
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.conn.opMu.Lock()
	defer c.conn.opMu.Unlock()
	if c.conn.closed() {
		return errors.New("smtpclient: connection is closed")
	}
	replies, err := c.conn.pipeline.executeLocked(ctx, []queuedCommand{{verb: "QUIT", syncPoint: true, timeout: c.conn.mailTimeout()}})
	if err == nil {
		err = unexpectedReply("QUIT", replies[0], c.conn.enhancedStatusCodes(), 221)
	}
	c.conn.poison()
	return err
}

func validatePath(path string) (string, error) {
	if strings.ContainsAny(path, "\r\n\x00<>") {
		return "", errors.New("smtpclient: path contains SMTP command framing")
	}
	return "<" + path + ">", nil
}
