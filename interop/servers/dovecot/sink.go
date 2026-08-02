package dovecot

import (
	"bytes"
	"context"
	"fmt"

	"github.com/kiliant/go-smtp/interop/harness"
)

// dovecotSink uses doveadm rather than assuming an on-disk mailbox layout.
// Dovecot 2.4's mailbox_list_layout setting can change that layout without
// changing LMTP behavior, while doveadm reads through the configured storage
// driver and therefore verifies that the message is visible to Dovecot itself.
type dovecotSink struct {
	exec harness.Execer
}

func (s dovecotSink) Fetch(ctx context.Context, recipient string) ([]harness.Message, error) {
	out, err := s.exec.Exec(ctx, "doveadm", "fetch", "-u", recipient, "text", "mailbox", "INBOX")
	if err != nil {
		return nil, fmt.Errorf("dovecot sink: fetch %s: %w", recipient, err)
	}
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil, nil
	}
	const prefix = "text:"
	if !bytes.HasPrefix(out, []byte(prefix)) {
		return nil, fmt.Errorf("dovecot sink: unexpected doveadm output %q", out)
	}
	raw := bytes.TrimPrefix(out, []byte(prefix))
	raw = bytes.TrimPrefix(raw, []byte("\r\n"))
	raw = bytes.TrimPrefix(raw, []byte("\n"))
	return []harness.Message{{Recipient: recipient, Raw: raw}}, nil
}

func (s dovecotSink) Reset(ctx context.Context, recipient string) error {
	_, err := s.exec.Exec(ctx, "doveadm", "expunge", "-u", recipient, "mailbox", "INBOX", "all")
	if err != nil {
		return fmt.Errorf("dovecot sink: reset %s: %w", recipient, err)
	}
	return nil
}
