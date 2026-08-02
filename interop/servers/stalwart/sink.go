package stalwart

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/kiliant/go-smtp/interop/harness"
)

// imapSink reads Stalwart's database-backed mailbox through its own IMAP
// service. The server does not use Maildir, so inspecting datastore files
// would couple the harness to RocksDB internals and would not prove that the
// message is actually visible to a mail client.
type imapSink struct {
	addr, username, password string
}

var stalwartDeliveryMailboxes = [...]string{"INBOX", "Junk Mail"}

func (s *imapSink) Fetch(ctx context.Context, recipient string) ([]harness.Message, error) {
	// Stalwart's built-in spam classifier can place synthetic interop messages
	// in Junk Mail. Inspect both delivery mailboxes: the sink is asserting that
	// local delivery completed, not testing Stalwart's spam classification.
	for _, mailbox := range stalwartDeliveryMailboxes {
		c, err := s.connect(ctx, mailbox)
		if err != nil {
			return nil, err
		}
		raw, found, err := c.fetchFirst()
		c.close()
		if err != nil {
			return nil, err
		}
		if found {
			return []harness.Message{{Recipient: recipient, Raw: raw}}, nil
		}
	}
	return nil, nil
}

func (s *imapSink) Reset(ctx context.Context, _ string) error {
	for _, mailbox := range stalwartDeliveryMailboxes {
		c, err := s.connect(ctx, mailbox)
		if err != nil {
			return err
		}
		if err := c.command("a3", "STORE 1:* +FLAGS.SILENT (\\Deleted)"); err != nil {
			c.close()
			return err
		}
		if err := c.command("a4", "EXPUNGE"); err != nil {
			c.close()
			return err
		}
		c.close()
	}
	return nil
}

type imapConn struct {
	net.Conn
	r *bufio.Reader
}

func (s *imapSink) connect(ctx context.Context, mailbox string) (*imapConn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", s.addr)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	}
	c := &imapConn{Conn: conn, r: bufio.NewReader(conn)}
	if line, err := c.r.ReadString('\n'); err != nil || !strings.HasPrefix(line, "* OK") {
		conn.Close()
		return nil, fmt.Errorf("stalwart IMAP greeting %q: %w", strings.TrimSpace(line), err)
	}
	if err := c.command("a1", fmt.Sprintf("LOGIN %s %s", imapQuote(s.username), imapQuote(s.password))); err != nil {
		conn.Close()
		return nil, err
	}
	if err := c.command("a2", "SELECT "+imapQuote(mailbox)); err != nil {
		conn.Close()
		return nil, err
	}
	return c, nil
}

func (c *imapConn) command(tag, command string) error {
	if _, err := fmt.Fprintf(c, "%s %s\r\n", tag, command); err != nil {
		return err
	}
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return err
		}
		if strings.HasPrefix(line, tag+" ") {
			if strings.HasPrefix(line, tag+" OK") {
				return nil
			}
			return fmt.Errorf("stalwart IMAP %s failed: %s", command, strings.TrimSpace(line))
		}
	}
}

func (c *imapConn) fetchFirst() ([]byte, bool, error) {
	if _, err := fmt.Fprint(c, "a3 FETCH 1 BODY.PEEK[]\r\n"); err != nil {
		return nil, false, err
	}
	for {
		line, err := c.r.ReadString('\n')
		if err != nil {
			return nil, false, err
		}
		if strings.HasPrefix(line, "a3 ") {
			if strings.HasPrefix(line, "a3 OK") {
				return nil, false, nil
			}
			return nil, false, fmt.Errorf("stalwart IMAP FETCH failed: %s", strings.TrimSpace(line))
		}
		trimmed := strings.TrimSpace(line)
		open := strings.LastIndexByte(trimmed, '{')
		if open < 0 || !strings.HasSuffix(trimmed, "}") {
			continue
		}
		n, err := strconv.Atoi(trimmed[open+1 : len(trimmed)-1])
		if err != nil || n < 0 || n > 256<<20 {
			return nil, false, fmt.Errorf("stalwart IMAP invalid literal size %q", trimmed[open+1:len(trimmed)-1])
		}
		raw := make([]byte, n)
		if _, err := io.ReadFull(c.r, raw); err != nil {
			return nil, false, err
		}
		// Consume the closing FETCH line and tagged completion before reuse.
		if _, err := c.r.ReadString('\n'); err != nil {
			return nil, false, err
		}
		for {
			line, err = c.r.ReadString('\n')
			if err != nil {
				return nil, false, err
			}
			if strings.HasPrefix(line, "a3 OK") {
				return raw, true, nil
			}
			if strings.HasPrefix(line, "a3 ") {
				return nil, false, fmt.Errorf("stalwart IMAP FETCH failed: %s", strings.TrimSpace(line))
			}
		}
	}
}

func (c *imapConn) close() {
	_, _ = fmt.Fprint(c, "zz LOGOUT\r\n")
	_ = c.Conn.Close()
}

func imapQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
