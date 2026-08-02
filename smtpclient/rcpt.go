package smtpclient

import (
	"context"
	"errors"

	smtp "github.com/kiliant/go-smtp"
)

// Rcpt adds a forward-path to the current mail transaction with RCPT TO (RFC
// 5321 §4.1.1.3). to is supplied without angle brackets. A rejected recipient
// is returned as an *smtp.Error, while recipients accepted earlier in the
// transaction remain available for Data.
func (c *Client) Rcpt(ctx context.Context, to string, opts *smtp.RcptOptions) error {
	if c == nil || c.conn == nil {
		return errNilClient
	}
	if to == "" {
		return errors.New("smtpclient: RCPT forward-path is required")
	}
	path, err := validatePath(to)
	if err != nil {
		return err
	}
	c.conn.opMu.Lock()
	defer c.conn.opMu.Unlock()
	args := []string{"TO:" + path}
	if opts != nil {
		extensionParams, err := c.extensionRcptParams(path, opts)
		if err != nil {
			return err
		}
		params := append(extensionParams, opts.Extra...)
		encoded, err := c.encodeParams(params, opts.AllowUnadvertisedParameters)
		if err != nil {
			return err
		}
		args = append(args, encoded...)
	}
	reply, err := c.commandLocked(ctx, "RCPT", args, c.conn.rcptTimeout, stateTransaction)
	if err != nil {
		return err
	}
	if reply.Code != 250 && reply.Code != 251 {
		return replyError("RCPT", reply, c.conn.enhancedStatusCodes())
	}
	c.conn.mu.Lock()
	if c.conn.state != stateClosed {
		c.conn.recipients = append(c.conn.recipients, to)
	}
	c.conn.mu.Unlock()
	return nil
}
