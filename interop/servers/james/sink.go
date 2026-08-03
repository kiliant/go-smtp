package james

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/kiliant/go-smtp/interop/harness"
)

const (
	interopDomain    = "example.test"
	interopRecipient = "interop@example.test"
	interopPassword  = "interop-pw"
)

// imapSink reads James's JPA-backed mailbox through the protocol the image
// actually exposes. The old profile used a nonexistent maildir path and could
// therefore never verify delivery.
type imapSink struct {
	addr     string
	password string
}

func (s *imapSink) Fetch(ctx context.Context, recipient string) ([]harness.Message, error) {
	c, err := s.dial(ctx, recipient)
	if err != nil {
		return nil, err
	}
	defer c.close()

	status, _, err := c.command("SELECT INBOX")
	if err != nil {
		return nil, fmt.Errorf("james sink: select INBOX: %w", err)
	}
	if status == "NO" { // James creates INBOX lazily on first delivery.
		return nil, nil
	}
	if status != "OK" {
		return nil, fmt.Errorf("james sink: select INBOX: server returned %s", status)
	}
	if c.exists == 0 {
		return nil, nil
	}

	status, literals, err := c.command("FETCH 1:* BODY.PEEK[]")
	if err != nil {
		return nil, fmt.Errorf("james sink: fetch INBOX: %w", err)
	}
	if status != "OK" {
		return nil, fmt.Errorf("james sink: fetch INBOX: server returned %s", status)
	}
	msgs := make([]harness.Message, 0, len(literals))
	for _, raw := range literals {
		msgs = append(msgs, harness.Message{Recipient: recipient, Raw: raw})
	}
	return msgs, nil
}

func (s *imapSink) Reset(ctx context.Context, recipient string) error {
	c, err := s.dial(ctx, recipient)
	if err != nil {
		return err
	}
	defer c.close()

	status, _, err := c.command("SELECT INBOX")
	if err != nil {
		return fmt.Errorf("james sink: select INBOX for reset: %w", err)
	}
	if status == "NO" { // No delivery has created the mailbox yet.
		return nil
	}
	if status != "OK" {
		return fmt.Errorf("james sink: select INBOX for reset: server returned %s", status)
	}
	if c.exists == 0 {
		return nil
	}
	if status, _, err = c.command(`STORE 1:* +FLAGS.SILENT (\Deleted)`); err != nil {
		return fmt.Errorf("james sink: mark messages deleted: %w", err)
	} else if status != "OK" {
		return fmt.Errorf("james sink: mark messages deleted: server returned %s", status)
	}
	if status, _, err = c.command("EXPUNGE"); err != nil {
		return fmt.Errorf("james sink: expunge messages: %w", err)
	} else if status != "OK" {
		return fmt.Errorf("james sink: expunge messages: server returned %s", status)
	}
	return nil
}

func (s *imapSink) dial(ctx context.Context, recipient string) (*imapConn, error) {
	dialer := &tls.Dialer{
		NetDialer: &net.Dialer{},
		Config: &tls.Config{
			MinVersion: tls.VersionTLS12,
			// The disposable interop image creates a new self-signed certificate
			// at every start; identity validation is not under test here.
			InsecureSkipVerify: true, //nolint:gosec
		},
	}
	raw, err := dialer.DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return nil, fmt.Errorf("james sink: dialing IMAPS: %w", err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(deadline)
	}
	stopCancel := context.AfterFunc(ctx, func() { _ = raw.SetDeadline(time.Now()) })
	c := &imapConn{conn: raw, r: bufio.NewReader(raw), stopCancel: stopCancel}
	line, err := c.r.ReadString('\n')
	if err != nil {
		c.close()
		return nil, fmt.Errorf("james sink: reading IMAP greeting: %w", err)
	}
	if !strings.HasPrefix(line, "* OK ") {
		c.close()
		return nil, fmt.Errorf("james sink: unexpected IMAP greeting %q", strings.TrimRight(line, "\r\n"))
	}
	status, _, err := c.command("LOGIN " + quoteIMAP(recipient) + " " + quoteIMAP(s.password))
	if err != nil {
		c.close()
		return nil, fmt.Errorf("james sink: login: %w", err)
	}
	if status != "OK" {
		c.close()
		return nil, fmt.Errorf("james sink: login: server returned %s", status)
	}
	return c, nil
}

type imapConn struct {
	conn       net.Conn
	r          *bufio.Reader
	nextTag    int
	exists     int
	stopCancel func() bool
}

func (c *imapConn) close() {
	if c.stopCancel != nil {
		c.stopCancel()
	}
	_ = c.conn.Close()
}

// command reads through the tagged completion response. Any IMAP literal in
// an untagged response is returned without line-based parsing, so embedded
// NULs and CRLFs remain byte exact.
func (c *imapConn) command(command string) (string, [][]byte, error) {
	c.nextTag++
	tag := fmt.Sprintf("a%d", c.nextTag)
	if _, err := fmt.Fprintf(c.conn, "%s %s\r\n", tag, command); err != nil {
		return "", nil, err
	}
	var literals [][]byte
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return "", nil, err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if fields := strings.Fields(trimmed); len(fields) == 3 && fields[0] == "*" && strings.EqualFold(fields[2], "EXISTS") {
			if n, err := strconv.Atoi(fields[1]); err == nil && n >= 0 {
				c.exists = n
			}
		}
		if n, ok := literalSize(trimmed); ok {
			raw := make([]byte, n)
			if _, err := io.ReadFull(c.r, raw); err != nil {
				return "", nil, err
			}
			literals = append(literals, raw)
			continue
		}
		if !strings.HasPrefix(trimmed, tag+" ") {
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) < 2 {
			return "", nil, fmt.Errorf("malformed tagged response %q", trimmed)
		}
		status := strings.ToUpper(fields[1])
		if status == "BAD" {
			return status, literals, fmt.Errorf("server returned %q", trimmed)
		}
		return status, literals, nil
	}
}

func literalSize(line string) (int, bool) {
	if !strings.HasSuffix(line, "}") {
		return 0, false
	}
	open := strings.LastIndexByte(line, '{')
	if open < 0 || open == len(line)-2 {
		return 0, false
	}
	n, err := strconv.Atoi(line[open+1 : len(line)-1])
	return n, err == nil && n >= 0
}

func quoteIMAP(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}
