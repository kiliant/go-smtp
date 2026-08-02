package smtpclient

import (
	"context"
	"errors"

	smtp "github.com/kiliant/go-smtp"
)

// StartTLS upgrades a cleartext ESMTP session using STARTTLS (RFC 3207).
// The server must have advertised STARTTLS. On success it discards the entire
// cleartext extension list and sends EHLO again, because RFC 3207 §4.2 treats
// the pre-TLS list as unauthenticated input. See Client's cancellation
// contract for the behavior of a cancelled in-flight upgrade.
func (c *Client) StartTLS(ctx context.Context, opts *StartTLSOptions) error {
	if c == nil || c.conn == nil {
		return errors.New("smtpclient: nil Client")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Hold this lock until the post-TLS EHLO finishes. Releasing it after the
	// STARTTLS reply would let a concurrent caller write a cleartext command
	// into the upgrade gap.
	c.conn.opMu.Lock()
	defer c.conn.opMu.Unlock()
	c.conn.mu.Lock()
	state := c.conn.state
	_, advertised := c.conn.ext[string(smtp.ExtStartTLS)]
	c.conn.mu.Unlock()
	if err := invalidState("STARTTLS", state, stateGreeted); err != nil {
		return err
	}
	if !advertised {
		return errors.New("smtpclient: server did not advertise STARTTLS")
	}
	replies, err := c.conn.pipeline.executeLocked(ctx, []queuedCommand{{
		verb: "STARTTLS", timeout: c.conn.mailTimeout(),
	}})
	if err != nil {
		return err
	}
	if err := unexpectedReply("STARTTLS", replies[0], c.conn.enhancedStatusCodes(), 220); err != nil {
		return err
	}

	var (
		base       = c.conn.options.TLSConfig
		serverName = c.conn.options.TLSServerName
		// A nil STARTTLS options pointer inherits the explicit construction
		// setting, including the deliberately named verification opt-out.
		insecure = c.conn.options.InsecureSkipVerify
	)
	if opts != nil {
		if opts.TLSConfig != nil {
			base = opts.TLSConfig
		}
		if opts.ServerName != "" {
			serverName = opts.ServerName
		}
		if opts.InsecureSkipVerify {
			insecure = true
		}
	}
	// Clear first, before the handshake: callers can never observe an
	// attacker-controlled cleartext capability after an upgrade attempt.
	c.conn.mu.Lock()
	c.conn.ext = make(map[string]string)
	c.conn.mu.Unlock()
	if err := c.conn.upgradeTLS(ctx, base, serverName, insecure, c.conn.mailTimeout()); err != nil {
		c.conn.poison()
		return err
	}
	c.conn.mu.Lock()
	if c.conn.state != stateClosed {
		c.conn.state = stateTLS
	}
	c.conn.mu.Unlock()
	return c.ehloLocked(ctx)
}
