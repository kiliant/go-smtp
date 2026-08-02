package smtpclient

import (
	"context"
	"errors"
	"strings"

	smtp "github.com/kiliant/go-smtp"
)

// BURLOptions configures BURL. Last ends the mail transaction. A nil options
// pointer sends LAST because a standalone BURL submission normally supplies
// the whole body.
// Callers constructing a BURLOptions literal must use keyed fields.
type BURLOptions struct {
	Last bool
	_    struct{}
}

// BURL submits message content by reference to an IMAP URL (RFC 4468). It is
// an alternative to DATA after at least one accepted recipient. The submission
// server, rather than this client, resolves the URL.
func (c *Client) BURL(ctx context.Context, url string, opts *BURLOptions) error {
	if c == nil || c.conn == nil {
		return errNilClient
	}
	if strings.TrimSpace(url) != url || url == "" || strings.ContainsAny(url, "\r\n") {
		return errors.New("smtpclient: BURL requires one non-empty URI argument")
	}
	if !c.advertises(string(smtp.ExtBURL)) {
		return errors.New("smtpclient: server did not advertise extension \"BURL\"")
	}
	last := opts == nil || opts.Last
	args := []string{url}
	if last {
		args = append(args, "LAST")
	}
	c.conn.opMu.Lock()
	defer c.conn.opMu.Unlock()
	c.conn.mu.Lock()
	recipients := append([]string(nil), c.conn.recipients...)
	c.conn.mu.Unlock()
	if len(recipients) == 0 {
		return errors.New("smtpclient: BURL requires an accepted recipient")
	}
	reply, err := c.commandLocked(ctx, "BURL", args, c.conn.dataCommandTimeout, stateTransaction)
	if err != nil {
		return err
	}
	if last {
		c.conn.mu.Lock()
		if c.conn.state != stateClosed {
			c.conn.state = c.conn.transactionBase
			c.conn.recipients = nil
		}
		c.conn.mu.Unlock()
	}
	return unexpectedReply("BURL", reply, c.conn.enhancedStatusCodes(), 250)
}
