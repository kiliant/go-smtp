// Package maddy registers the maddy interop profile: an independent Go
// implementation, a different bug class from the C servers (docs/INTEROP.md).
//
// NOT LIVE-VERIFIED this session. maddy.conf is a from-scratch minimal
// configuration and the account (interop@example.test) is not yet
// provisioned inside the image — that needs `maddy creds create` /
// `maddy imap-acct create` run against the running instance, which this
// Containerfile does not yet do. AssertProfile will fail loudly rather than
// silently pass if the listener or account is not actually there.
package maddy

import (
	"context"
	"path/filepath"
	"runtime"

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
	return harness.MaildirSink{
		Exec: h,
		Dir:  func(recipient string) string { return "/data/mail/" + recipient },
	}, nil
}
