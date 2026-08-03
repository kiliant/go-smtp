package smtpclient

import (
	"context"
	"errors"
	"strings"

	smtp "github.com/kiliant/go-smtp"
)

// BURLOptions configures RFC 4468 BURL. Last ends the mail transaction. A nil
// options pointer means defaults and sends LAST because a standalone BURL submission normally
// supplies the whole body.
// Callers constructing a BURLOptions literal must use keyed fields.
type BURLOptions struct {
	// Last sends the RFC 4468 LAST marker and completes content submission.
	Last bool
	_    struct{}
}

// BURL submits message content by reference to an IMAP URL (RFC 4468). It is
// an alternative to DATA after at least one accepted recipient. The submission
// server, rather than this client, resolves the URL. A BURL carrying LAST
// returns one result per accepted recipient; SMTP's single final reply is
// copied to every result. A non-LAST BURL returns an empty result and leaves
// the transaction open for another BURL or BDAT command.
//
// RFC 4468 defines BURL only for SMTP Message Submission. BURL is rejected
// locally in LMTP mode even if the peer advertises the extension token.
func (c *Client) BURL(ctx context.Context, url string, opts *BURLOptions) (smtp.DataResult, error) {
	if c == nil || c.conn == nil {
		return nil, errNilClient
	}
	if strings.TrimSpace(url) != url || url == "" || strings.ContainsAny(url, "\r\n") {
		return nil, errors.New("smtpclient: BURL requires one non-empty URI argument")
	}
	c.conn.mu.Lock()
	lmtp := c.conn.options.LMTP
	c.conn.mu.Unlock()
	if lmtp {
		return nil, errors.New("smtpclient: BURL is not supported in LMTP mode")
	}
	if !c.advertises(string(smtp.ExtBURL)) {
		return nil, errors.New("smtpclient: server did not advertise extension \"BURL\"")
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
		return nil, errors.New("smtpclient: BURL requires an accepted recipient")
	}
	reply, err := c.commandLocked(ctx, "BURL", args, c.conn.dataCommandTimeout, stateTransaction)
	if err != nil {
		return nil, err
	}
	if last {
		c.conn.mu.Lock()
		if c.conn.state != stateClosed {
			c.conn.state = c.conn.transactionBase
			c.conn.recipients = nil
			c.conn.smtpUTF8 = false
			c.conn.binaryMIME = false
		}
		c.conn.mu.Unlock()
	}
	if err := unexpectedReply("BURL", reply, c.conn.enhancedStatusCodes(), 250); err != nil {
		return nil, err
	}
	if !last {
		return nil, nil
	}
	result := make(smtp.DataResult, len(recipients))
	final := replyError("BURL", reply, c.conn.enhancedStatusCodes())
	for i, recipient := range recipients {
		result[i] = smtp.RecipientResult{
			Recipient: recipient,
			Command:   "BURL",
			Code:      final.Code,
			Enhanced:  final.Enhanced,
			Text:      final.Text,
		}
	}
	return result, nil
}
