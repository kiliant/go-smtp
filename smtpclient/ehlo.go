package smtpclient

import (
	"context"
	"strings"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

// Extension reports whether the server advertised ext and, when it did, its
// raw parameter text. The raw text is retained rather than reconstructed from
// split fields so a future extension with space-sensitive parameters is not
// silently made unusable. ext is compared case-insensitively for convenience.
func (c *Client) Extension(ext smtp.Extension) (params string, ok bool) {
	if c == nil || c.conn == nil {
		return "", false
	}
	c.conn.mu.Lock()
	defer c.conn.mu.Unlock()
	params, ok = c.conn.ext[strings.ToUpper(string(ext))]
	return params, ok
}

func (c *Client) ehlo(ctx context.Context) error {
	if c == nil || c.conn == nil {
		return transportError("EHLO", errNilClient)
	}
	c.conn.opMu.Lock()
	defer c.conn.opMu.Unlock()
	return c.ehloLocked(ctx)
}

// ehloLocked runs negotiation while the caller owns opMu. STARTTLS uses it to
// ensure no command can be written between its 220 response, TLS handshake,
// and mandatory replacement EHLO.
func (c *Client) ehloLocked(ctx context.Context) error {
	verb := "EHLO"
	if c.conn.options.LMTP {
		verb = "LHLO"
	}
	replies, err := c.conn.pipeline.executeLocked(ctx, []queuedCommand{{
		verb: verb, args: []string{c.conn.identity()}, syncPoint: true, timeout: c.conn.mailTimeout(),
	}})
	if err != nil {
		return transportError(verb, err)
	}
	reply := replies[0]
	if !c.conn.options.LMTP && (reply.Code == 500 || reply.Code == 502) {
		return c.heloLocked(ctx)
	}
	// EHLO establishes the extension table, so its failure cannot assume
	// ENHANCEDSTATUSCODES even if this is a re-negotiation.
	if err := unexpectedReply(verb, reply, false, 250); err != nil {
		return err
	}
	parsed, err := smtpwire.ParseEHLOReply(reply.Lines)
	if err != nil {
		c.conn.poison()
		return transportError(verb, err)
	}
	exts := make(map[string]string, len(parsed.Extensions))
	for _, ext := range parsed.Extensions {
		// Repeated keywords are non-standard but preserving the last raw form
		// is deterministic and avoids inventing a closed extension structure.
		exts[ext.Keyword] = ext.Raw
	}
	c.conn.mu.Lock()
	if c.conn.state != stateClosed {
		c.conn.ext = exts
	}
	c.conn.mu.Unlock()
	return nil
}

func (c *Client) helo(ctx context.Context) error {
	c.conn.opMu.Lock()
	defer c.conn.opMu.Unlock()
	return c.heloLocked(ctx)
}

func (c *Client) heloLocked(ctx context.Context) error {
	replies, err := c.conn.pipeline.executeLocked(ctx, []queuedCommand{{
		verb: "HELO", args: []string{c.conn.identity()}, syncPoint: true, timeout: c.conn.mailTimeout(),
	}})
	if err != nil {
		return err
	}
	if err := unexpectedReply("HELO", replies[0], false, 250); err != nil {
		return err
	}
	c.conn.mu.Lock()
	if c.conn.state != stateClosed {
		// RFC 5321 HELO predates extensions. Keep this empty even if a hostile
		// server returned a multiline response resembling EHLO.
		c.conn.ext = make(map[string]string)
	}
	c.conn.mu.Unlock()
	return nil
}
