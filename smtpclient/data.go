package smtpclient

import (
	"context"
	"errors"
	"io"
	"time"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

// DataOptions configures Data. A nil *DataOptions is valid.
//
// Callers constructing a DataOptions literal must use keyed fields.
type DataOptions struct {
	// UseChunking selects the CHUNKING/BDAT transfer path. It is rejected
	// locally unless the server advertised CHUNKING.
	UseChunking bool
	// ChunkSize bounds individual BDAT chunks. Zero selects the extension
	// default.
	ChunkSize int
	_         struct{}
}

// Data submits content for all recipients accepted by Rcpt. It streams r
// through RFC 5321 dot transparency without buffering. Its result is one
// smtp.RecipientResult per accepted recipient: SMTP's one final reply is
// copied to each entry so the result shape remains compatible with LMTP.
func (c *Client) Data(ctx context.Context, r io.Reader, opts *DataOptions) (smtp.DataResult, error) {
	if c == nil || c.conn == nil {
		return nil, errNilClient
	}
	if r == nil {
		return nil, errors.New("smtpclient: nil DATA reader")
	}
	if result, handled, err := extensionData(ctx, c, r, opts); handled {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.conn.opMu.Lock()
	defer c.conn.opMu.Unlock()
	c.conn.mu.Lock()
	state := c.conn.state
	recipients := append([]string(nil), c.conn.recipients...)
	c.conn.mu.Unlock()
	if err := invalidState("DATA", state, stateTransaction); err != nil {
		return nil, err
	}
	if len(recipients) == 0 {
		return nil, errors.New("smtpclient: DATA requires an accepted recipient")
	}
	replies, err := c.conn.pipeline.executeLocked(ctx, []queuedCommand{{verb: "DATA", syncPoint: true, timeout: c.conn.dataCommandTimeout()}})
	if err != nil {
		return nil, err
	}
	if err := unexpectedReply("DATA", replies[0], c.conn.enhancedStatusCodes(), 354); err != nil {
		return nil, err
	}

	stop := c.conn.cancelWatcher(ctx)
	defer stop()
	c.conn.mu.Lock()
	raw := c.conn.raw
	c.conn.mu.Unlock()
	dw := smtpwire.NewDotStuffWriter(&deadlineWriter{raw: raw, timeout: c.conn.dataBlockTimeout})
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			c.conn.poison()
			return nil, err
		}
		n, readErr := r.Read(buf)
		if n > 0 {
			if _, err := dw.Write(buf[:n]); err != nil {
				c.conn.poison()
				return nil, transportError("DATA", err)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			c.conn.poison()
			return nil, transportError("DATA", readErr)
		}
	}
	if err := dw.Close(); err != nil {
		c.conn.poison()
		return nil, transportError("DATA", err)
	}
	if err := ctx.Err(); err != nil {
		c.conn.poison()
		return nil, err
	}
	reply, err := c.conn.pipeline.read(ctx, "DATA", c.conn.dataFinalTimeout())
	if err != nil {
		return nil, err
	}
	c.conn.mu.Lock()
	if c.conn.state != stateClosed {
		c.conn.state = c.conn.transactionBase
		c.conn.recipients = nil
	}
	c.conn.mu.Unlock()
	if err := unexpectedReply("DATA", reply, c.conn.enhancedStatusCodes(), 250); err != nil {
		return nil, err
	}
	result := make(smtp.DataResult, len(recipients))
	for i, recipient := range recipients {
		errReply := replyError("DATA", reply, c.conn.enhancedStatusCodes())
		result[i] = smtp.RecipientResult{Recipient: recipient, Command: "DATA", Code: errReply.Code, Enhanced: errReply.Enhanced, Text: errReply.Text}
	}
	return result, nil
}

type deadlineWriter struct {
	raw interface {
		SetWriteDeadline(time.Time) error
		Write([]byte) (int, error)
	}
	timeout func() time.Duration
}

func (w *deadlineWriter) Write(p []byte) (int, error) {
	if err := w.raw.SetWriteDeadline(time.Now().Add(w.timeout())); err != nil {
		return 0, err
	}
	defer w.raw.SetWriteDeadline(time.Time{})
	return w.raw.Write(p)
}
