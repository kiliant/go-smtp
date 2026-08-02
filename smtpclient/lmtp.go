package smtpclient

import (
	"context"
	"errors"
	"net"
	"time"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

// lmtpExtraReplyProbeTimeout bounds the post-DATA check for an unsolicited
// reply without turning a valid LMTP completion into a second DATA timeout.
const lmtpExtraReplyProbeTimeout = 10 * time.Millisecond

// LMTP uses the same DATA command as SMTP, but RFC 2033 requires one final
// reply for each recipient accepted by RCPT. Registering the handler here
// keeps the transaction core independent of the LMTP reply cardinality.
func init() {
	registerLMTPFinalReplies(readLMTPFinalReplies)
}

func readLMTPFinalReplies(ctx context.Context, c *Client, recipients []string) (smtp.DataResult, bool, error) {
	c.conn.mu.Lock()
	lmtp := c.conn.options.LMTP
	c.conn.mu.Unlock()
	if !lmtp {
		return nil, false, nil
	}

	result := make(smtp.DataResult, len(recipients))
	for i, recipient := range recipients {
		reply, err := c.conn.pipeline.read(ctx, "DATA", c.conn.dataFinalTimeout())
		if err != nil {
			// pipeline.read poisons errors that could desynchronise the reply
			// stream. Keep this explicit for any future alternate reader.
			c.conn.poison()
			return nil, true, err
		}
		errReply := replyError("DATA", reply, c.conn.enhancedStatusCodes())
		result[i] = smtp.RecipientResult{
			Recipient: recipient,
			Command:   "DATA",
			Code:      errReply.Code,
			Enhanced:  errReply.Enhanced,
			Text:      errReply.Text,
		}
	}
	// A complete LMTP DATA exchange has exactly one final reply per accepted
	// RCPT. Bytes already buffered beyond that count cannot belong to a later
	// client command, so retaining the session would misattribute them.
	if err := c.rejectExtraLMTPFinalReply(); err != nil {
		c.conn.poison()
		return nil, true, err
	}

	// An LMTP transaction completes only after every expected final reply has
	// been consumed. A rejected recipient is a result, not a connection error.
	c.conn.mu.Lock()
	if c.conn.state != stateClosed {
		c.conn.state = c.conn.transactionBase
		c.conn.recipients = nil
	}
	c.conn.mu.Unlock()
	return result, true, nil
}

// rejectExtraLMTPFinalReply checks for a reply beyond the RFC 2033 count
// without waiting for another command to misattribute it. Buffered bytes are
// checked first; the zero-deadline read also releases a peer blocked while
// writing an extra reply on a full-duplex connection.
func (c *Client) rejectExtraLMTPFinalReply() error {
	c.conn.mu.Lock()
	reader := c.conn.reader
	raw := c.conn.raw
	buffered := reader.Buffered() > 0
	c.conn.mu.Unlock()
	if buffered {
		return transportError("DATA", errors.New("smtpclient: LMTP sent more final replies than accepted recipients"))
	}
	reply, err := reader.ReadReply(time.Now().Add(lmtpExtraReplyProbeTimeout), smtpwire.Limits{})
	_ = raw.SetReadDeadline(time.Time{})
	if err != nil {
		var networkErr net.Error
		if errors.As(err, &networkErr) && networkErr.Timeout() {
			return nil
		}
		return transportError("DATA", err)
	}
	_ = reply
	return transportError("DATA", errors.New("smtpclient: LMTP sent more final replies than accepted recipients"))
}
