package smtpclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

const (
	defaultBDATChunkSize = 32 << 10
	maxBDATChunkSize     = 64 << 20
)

func init() {
	registerMailExtension("transport", transportMailParams)
	registerDataExtension(transportData)
}

func transportMailParams(c *Client, path string, opts *smtp.MailOptions) ([]smtp.Param, error) {
	clearBinaryMail(c)
	if hasNonASCII(path) && (opts == nil || opts.Transport == nil || !opts.Transport.SMTPUTF8) {
		return nil, errors.New("smtpclient: UTF-8 MAIL path requires SMTPUTF8")
	}
	if opts == nil || opts.Transport == nil {
		return nil, nil
	}

	t := opts.Transport
	params := make([]smtp.Param, 0, 3)
	if t.Size != nil {
		if *t.Size < 0 {
			return nil, errors.New("smtpclient: SIZE must not be negative")
		}
		maximum, stated, err := parseSizeAdvertisement(c)
		if err != nil {
			return nil, err
		}
		if stated && maximum != 0 && *t.Size > maximum {
			return nil, fmt.Errorf("smtpclient: declared SIZE %d exceeds server maximum %d", *t.Size, maximum)
		}
		params = append(params, smtp.Param{Keyword: "SIZE", Value: strconv.FormatInt(*t.Size, 10)})
	}

	if t.Body != "" {
		body := string(t.Body)
		if strings.ContainsAny(body, "\r\n\x00") {
			return nil, errors.New("smtpclient: BODY contains SMTP command framing")
		}
		switch strings.ToUpper(body) {
		case string(smtp.BodyType8BitMIME):
			if !c.advertises(string(smtp.Ext8BitMIME)) {
				return nil, missingExtension(smtp.Ext8BitMIME)
			}
		case string(smtp.BodyTypeBinaryMIME):
			if !c.advertises(string(smtp.ExtBinaryMIME)) {
				return nil, missingExtension(smtp.ExtBinaryMIME)
			}
			if !c.advertises(string(smtp.ExtChunking)) {
				return nil, missingExtension(smtp.ExtChunking)
			}
			c.conn.mu.Lock()
			c.conn.binaryMIME = true
			c.conn.mu.Unlock()
		}
		params = append(params, smtp.Param{Keyword: "BODY", Value: body})
	}

	if t.SMTPUTF8 {
		if !c.advertises(string(smtp.ExtSMTPUTF8)) {
			return nil, missingExtension(smtp.ExtSMTPUTF8)
		}
		if !c.advertises(string(smtp.Ext8BitMIME)) {
			return nil, fmt.Errorf("smtpclient: SMTPUTF8 requires server extension %q", smtp.Ext8BitMIME)
		}
		params = append(params, smtp.Param{Keyword: "SMTPUTF8"})
	}
	return params, nil
}

func hasNonASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return true
		}
	}
	return false
}

func missingExtension(ext smtp.Extension) error {
	return fmt.Errorf("smtpclient: server did not advertise extension %q", ext)
}

// parseSizeAdvertisement distinguishes SIZE's EHLO maximum from its MAIL
// declaration. A bare SIZE keyword is valid and expresses no maximum.
func parseSizeAdvertisement(c *Client) (maximum int64, stated bool, err error) {
	raw, ok := c.Extension(smtp.ExtSize)
	if !ok {
		return 0, false, missingExtension(smtp.ExtSize)
	}
	maximum, err = parseSizeParam(raw)
	return maximum, true, err
}

func parseSizeParam(raw string) (int64, error) {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return 0, nil
	}
	if len(fields) != 1 {
		return 0, fmt.Errorf("smtpclient: malformed SIZE advertisement %q", raw)
	}
	n, parseErr := strconv.ParseUint(fields[0], 10, 63)
	if parseErr != nil {
		return 0, fmt.Errorf("smtpclient: malformed SIZE advertisement %q: %w", raw, parseErr)
	}
	return int64(n), nil
}

func transportData(ctx context.Context, c *Client, r io.Reader, opts *DataOptions) (smtp.DataResult, bool, error) {
	useChunking := opts != nil && opts.UseChunking
	binary := binaryMailFor(c)
	if !useChunking {
		if binary {
			return nil, true, errors.New("smtpclient: BODY=BINARYMIME requires CHUNKING/BDAT, not DATA")
		}
		return nil, false, nil
	}
	if !c.advertises(string(smtp.ExtChunking)) {
		return nil, true, missingExtension(smtp.ExtChunking)
	}
	chunkSize := defaultBDATChunkSize
	if opts.ChunkSize != 0 {
		chunkSize = opts.ChunkSize
	}
	if chunkSize <= 0 || chunkSize > maxBDATChunkSize {
		return nil, true, fmt.Errorf("smtpclient: invalid BDAT chunk size %d", chunkSize)
	}
	return bdat(ctx, c, r, chunkSize)
}

func binaryMailFor(c *Client) bool {
	c.conn.mu.Lock()
	defer c.conn.mu.Unlock()
	return c.conn.binaryMIME
}

func clearBinaryMail(c *Client) {
	c.conn.mu.Lock()
	c.conn.binaryMIME = false
	c.conn.mu.Unlock()
}

func bdat(ctx context.Context, c *Client, r io.Reader, chunkSize int) (smtp.DataResult, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	c.conn.opMu.Lock()
	defer c.conn.opMu.Unlock()
	c.conn.mu.Lock()
	state := c.conn.state
	recipients := append([]string(nil), c.conn.recipients...)
	c.conn.mu.Unlock()
	if err := invalidState("BDAT", state, stateTransaction); err != nil {
		return nil, true, err
	}
	if len(recipients) == 0 {
		return nil, true, errors.New("smtpclient: BDAT requires an accepted recipient")
	}

	buf := make([]byte, chunkSize)
	for {
		if err := ctx.Err(); err != nil {
			c.conn.poison()
			return nil, true, err
		}
		n, readErr := io.ReadFull(r, buf)
		last := readErr == io.EOF || readErr == io.ErrUnexpectedEOF
		if readErr != nil && !last {
			return nil, true, readErr
		}
		if err := c.writeBDATChunk(ctx, buf[:n], last); err != nil {
			return nil, true, err
		}
		reply, err := c.conn.pipeline.read(ctx, "BDAT", c.conn.dataFinalTimeout())
		if err != nil {
			return nil, true, err
		}
		if last {
			clearBinaryMail(c)
			c.conn.mu.Lock()
			if c.conn.state != stateClosed {
				c.conn.state = c.conn.transactionBase
				c.conn.recipients = nil
			}
			c.conn.mu.Unlock()
			if err := unexpectedReply("BDAT", reply, c.conn.enhancedStatusCodes(), 250); err != nil {
				return nil, true, err
			}
			return bdatResult(recipients, reply, c), true, nil
		}
		if err := unexpectedReply("BDAT", reply, c.conn.enhancedStatusCodes(), 250); err != nil {
			// A non-final BDAT has already changed server-side message state.
			// Keep the local transaction open so RSET is the sole recovery.
			return nil, true, err
		}
	}
}

func (c *Client) writeBDATChunk(ctx context.Context, chunk []byte, last bool) error {
	stop := c.conn.cancelWatcher(ctx)
	defer stop()
	c.conn.mu.Lock()
	raw := c.conn.raw
	c.conn.mu.Unlock()
	w := &deadlineWriter{raw: raw, timeout: c.conn.dataBlockTimeout}
	limits := smtpwire.Limits{MaxBDATChunkSize: maxBDATChunkSize}
	if err := smtpwire.EncodeBDATCommand(w, uint64(len(chunk)), last, limits); err != nil {
		c.conn.poison()
		return transportError("BDAT", err)
	}
	if _, err := smtpwire.CopyBDATChunk(w, bytes.NewReader(chunk), uint64(len(chunk)), limits); err != nil {
		c.conn.poison()
		return transportError("BDAT", err)
	}
	if err := ctx.Err(); err != nil {
		c.conn.poison()
		return err
	}
	return nil
}

func bdatResult(recipients []string, reply smtpwire.Reply, c *Client) smtp.DataResult {
	result := make(smtp.DataResult, len(recipients))
	for i, recipient := range recipients {
		errReply := replyError("BDAT", reply, c.conn.enhancedStatusCodes())
		result[i] = smtp.RecipientResult{Recipient: recipient, Command: "BDAT", Code: errReply.Code, Enhanced: errReply.Enhanced, Text: errReply.Text}
	}
	return result
}
