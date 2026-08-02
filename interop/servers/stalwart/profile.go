// Package stalwart registers the Stalwart interop profile: the "aggressive
// coverage" server (SMTPUTF8, REQUIRETLS, DSN, CHUNKING) docs/INTEROP.md
// calls out.
//
// Verified end to end on 2026-08-02: the image's bootstrap configuration
// exposed its SMTP listener, advertised this profile's expected extensions,
// and accepted a seeded message which the maildir sink read back. The server's
// durable configuration is database-backed; this profile intentionally relies
// only on that verified default SMTP path rather than treating --config as a
// static TOML configuration file.
package stalwart

import (
	"context"
	"path/filepath"
	"runtime"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/interop/harness"
)

const (
	smtpPort  = 25
	adminPort = 8080
)

func init() {
	harness.Register(harness.Profile{
		Name: "stalwart",
		Tier: harness.Tier1,
		Run: harness.RunConfig{
			ContainerfileDir: containerfileDir(),
			Ports:            []int{smtpPort, adminPort},
			Env: map[string]string{
				"STALWART_RECOVERY_ADMIN": "admin:interop-admin-pw",
			},
		},
		Ports: []harness.Port{
			{Container: smtpPort, Kind: "smtp"},
		},
		ExpectedExtensions: []smtp.Extension{
			smtp.ExtStartTLS,
			smtp.ExtSize,
			smtp.Ext8BitMIME,
			smtp.ExtSMTPUTF8,
			smtp.ExtEnhancedStatusCodes,
			smtp.ExtDSN,
			smtp.ExtChunking,
			smtp.ExtRequireTLS,
		},
		NewSink: newSink,
	})
}

func containerfileDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(file)
}

func newSink(ctx context.Context, h *harness.Handle) (harness.Sink, error) {
	return harness.MaildirSink{
		Exec: h,
		Dir:  func(recipient string) string { return "/opt/stalwart/data/mail/" + recipient },
	}, nil
}
