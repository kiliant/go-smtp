package smtpclient

import (
	"context"
	"errors"
	"fmt"

	smtp "github.com/kiliant/go-smtp"
)

// Recipient is one RFC 5321 forward-path and its per-recipient ESMTP parameters for
// RcptBatch. Address is supplied without angle brackets.
//
// Callers constructing a Recipient literal must use keyed fields.
type Recipient struct {
	// Address is the RCPT TO forward-path without angle brackets.
	Address string
	// Options carries the ESMTP parameters for this recipient. Nil means
	// defaults, exactly as for Client.Rcpt.
	Options *smtp.RcptOptions
	_       struct{}
}

// RcptBatchOptions configures RFC 5321/RFC 2920 RcptBatch. A nil
// *RcptBatchOptions means defaults.
//
// Callers constructing a RcptBatchOptions literal must use keyed fields.
type RcptBatchOptions struct{ _ struct{} }

// Rcpt adds a forward-path to the current mail transaction with RCPT TO (RFC
// 5321 §4.1.1.3). to is supplied without angle brackets. A rejected recipient
// is returned as an *smtp.Error, while recipients accepted earlier in the
// transaction remain available for Data.
func (c *Client) Rcpt(ctx context.Context, to string, opts *smtp.RcptOptions) error {
	result, err := c.rcptBatch(ctx, []Recipient{{Address: to, Options: opts}})
	if err != nil {
		return err
	}
	if len(result) != 1 {
		return errors.New("smtpclient: internal RCPT result cardinality mismatch")
	}
	if result[0].Accepted() {
		return nil
	}
	return result[0].Err()
}

// RcptBatch adds multiple forward-paths to the current transaction. When the
// server advertises PIPELINING (RFC 2920), the client writes a bounded group of
// RCPT commands before reading their replies; otherwise the same queue runs at
// depth one. The returned result preserves input order and includes rejected
// recipients. Only recipients with 250 or 251 replies are retained for Data.
func (c *Client) RcptBatch(ctx context.Context, recipients []Recipient, opts *RcptBatchOptions) (smtp.RcptResult, error) {
	_ = opts
	return c.rcptBatch(ctx, recipients)
}

func (c *Client) rcptBatch(ctx context.Context, recipients []Recipient) (smtp.RcptResult, error) {
	if c == nil || c.conn == nil {
		return nil, errNilClient
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(recipients) == 0 {
		return nil, errors.New("smtpclient: RcptBatch requires at least one recipient")
	}
	c.conn.opMu.Lock()
	defer c.conn.opMu.Unlock()
	c.conn.mu.Lock()
	state := c.conn.state
	requestedSMTPUTF8 := c.conn.smtpUTF8
	c.conn.mu.Unlock()
	if err := invalidState("RCPT", state, stateTransaction); err != nil {
		return nil, err
	}

	commands := make([]queuedCommand, len(recipients))
	for i, recipient := range recipients {
		if recipient.Address == "" {
			return nil, fmt.Errorf("smtpclient: recipient %d: RCPT forward-path is required", i)
		}
		path, err := validatePath(recipient.Address)
		if err != nil {
			return nil, fmt.Errorf("smtpclient: recipient %d: %w", i, err)
		}
		if hasNonASCII(path) {
			if !requestedSMTPUTF8 {
				return nil, fmt.Errorf("smtpclient: recipient %d: UTF-8 RCPT forward-path requires the transaction's MAIL FROM to have requested SMTPUTF8 (RFC 6531)", i)
			}
			if !c.advertises(string(smtp.ExtSMTPUTF8)) {
				return nil, missingExtension(smtp.ExtSMTPUTF8)
			}
		}
		args := []string{"TO:" + path}
		if recipient.Options != nil {
			extensionParams, err := c.extensionRcptParams(path, recipient.Options)
			if err != nil {
				return nil, fmt.Errorf("smtpclient: recipient %d: %w", i, err)
			}
			params := append(extensionParams, recipient.Options.Extra...)
			encoded, err := c.encodeParams(params, recipient.Options.AllowUnadvertisedParameters)
			if err != nil {
				return nil, fmt.Errorf("smtpclient: recipient %d: %w", i, err)
			}
			args = append(args, encoded...)
		}
		commands[i] = queuedCommand{verb: "RCPT", args: args, timeout: c.conn.rcptTimeout()}
	}

	replies, err := c.conn.pipeline.executeLocked(ctx, commands)
	if err != nil {
		return nil, err
	}
	result := make(smtp.RcptResult, len(replies))
	accepted := make([]string, 0, len(replies))
	for i, reply := range replies {
		replyErr := replyError("RCPT", reply, c.conn.enhancedStatusCodes())
		result[i] = smtp.RecipientResult{
			Recipient: recipients[i].Address,
			Command:   "RCPT",
			Code:      replyErr.Code,
			Enhanced:  replyErr.Enhanced,
			Text:      replyErr.Text,
		}
		if reply.Code == 250 || reply.Code == 251 {
			accepted = append(accepted, recipients[i].Address)
		}
	}
	c.conn.mu.Lock()
	if c.conn.state != stateClosed {
		c.conn.recipients = append(c.conn.recipients, accepted...)
	}
	c.conn.mu.Unlock()
	return result, nil
}
