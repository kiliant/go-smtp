// Package maddy registers the maddy interop profile: an independent Go
// implementation, a different bug class from the C servers (docs/INTEROP.md).
//
// The image provisions interop@example.test at startup and the matrix verifies
// a public smtpclient transaction through maddy's supported management CLI.
// Verified end to end on 2026-08-02.
package maddy

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/interop/harness"
)

const smtpPort = 25

func init() {
	harness.Register(harness.Profile{
		Name: "maddy",
		Tier: harness.Tier2,
		Run: harness.RunConfig{
			ContainerfileDir: containerfileDir(),
			Ports:            []int{smtpPort},
		},
		Ports: []harness.Port{
			{Container: smtpPort, Kind: "smtp"},
		},
		ExpectedExtensions: []smtp.Extension{
			smtp.ExtStartTLS,
			smtp.ExtPipelining,
			smtp.Ext8BitMIME,
			smtp.ExtEnhancedStatusCodes,
		},
		NewSink: newSink,
	})
}

func containerfileDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(file)
}

func newSink(ctx context.Context, h *harness.Handle) (harness.Sink, error) {
	return &sink{exec: h}, nil
}

// maddy stores message metadata in imapsql and bodies in a content-addressed
// blob store, not Maildir. Use its supported management CLI so the sink follows
// the database mapping instead of guessing blob filenames.
type sink struct{ exec harness.Execer }

func (s *sink) Fetch(ctx context.Context, recipient string) ([]harness.Message, error) {
	listed, err := s.exec.Exec(ctx, "/bin/maddy", "-config", "/data/maddy.conf", "imap-msgs", "list", recipient, "INBOX")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(listed)) == "" {
		return nil, nil
	}
	raw, err := s.exec.Exec(ctx, "/bin/maddy", "-config", "/data/maddy.conf", "imap-msgs", "dump", recipient, "INBOX", "1")
	if err != nil {
		return nil, err
	}
	return []harness.Message{{Recipient: recipient, Raw: raw}}, nil
}

func (s *sink) Reset(ctx context.Context, recipient string) error {
	listed, err := s.exec.Exec(ctx, "/bin/maddy", "-config", "/data/maddy.conf", "imap-msgs", "list", recipient, "INBOX")
	if err != nil || strings.TrimSpace(string(listed)) == "" {
		return err
	}
	_, err = s.exec.Exec(ctx, "/bin/maddy", "-config", "/data/maddy.conf", "imap-msgs", "remove", "--yes", recipient, "INBOX", "1:*")
	return err
}
