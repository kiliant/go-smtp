// Package stalwart registers the Stalwart interop profile: the "aggressive
// coverage" server (SMTPUTF8, REQUIRETLS, DSN, CHUNKING) docs/INTEROP.md
// calls out.
//
// NOT RUNNABLE yet — traced, not completed, this session. Unlike the other
// profiles in this matrix, Stalwart 0.16 has no static-file configuration
// entry point: the file --config takes is a JSON "DataStore" registry
// descriptor pointing at the backing store for the server's real settings,
// which live in a database and are edited through the admin HTTP API or web
// UI (crates/store/src/registry/local.rs). Without that file the server
// starts in bootstrap mode and opens only the admin UI on :8080; the SMTP
// listener this profile targets never comes up. See Containerfile for the
// full trace.
//
// Completing this profile means driving the admin API after start (using
// STALWART_RECOVERY_ADMIN to pin credentials, per the server's own startup
// banner) to create the example.test domain, the interop mailbox, and
// enable the SMTP listener — deferred as follow-up work, not attempted
// here. Run() and NewSink below are wired to the expected shape so that
// follow-up is a provisioning step, not a redesign; Run() will start the
// container and AssertProfile will fail loudly (OutcomeEnvironmental) since
// the SMTP port never opens, which is the correct behavior for an
// incomplete profile: it must not silently report a pass.
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
