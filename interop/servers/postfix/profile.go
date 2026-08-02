// Package postfix registers the Postfix interop profile: the ESMTP
// baseline. Built from docker.io/boky/postfix with a provisioned virtual
// mailbox (see Containerfile), verified end to end on 2026-08-02: EHLO
// advertised
//
//	PIPELINING, SIZE, VRFY, ETRN, STARTTLS, ENHANCEDSTATUSCODES, 8BITMIME,
//	DSN, SMTPUTF8, CHUNKING
//
// and a message seeded independently of go-smtp round-tripped through the
// maildir sink byte-for-byte (modulo the Postfix-prepended trace headers),
// including a body line consisting of exactly ".".
package postfix

import (
	"context"
	"path/filepath"
	"runtime"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/interop/harness"
)

const (
	smtpPort = 25
	maildir  = "/var/mail/vhosts/example.test/interop"
)

func init() {
	harness.Register(harness.Profile{
		Name: "postfix",
		Tier: harness.Tier1,
		Run: harness.RunConfig{
			ContainerfileDir: containerfileDir(),
			Ports:            []int{smtpPort},
			Env: map[string]string{
				"ALLOWED_SENDER_DOMAINS": "example.test",
			},
		},
		Ports: []harness.Port{
			{Container: smtpPort, Kind: "smtp"},
		},
		ExpectedExtensions: []smtp.Extension{
			smtp.ExtPipelining,
			smtp.ExtSize,
			smtp.ExtStartTLS,
			smtp.ExtEnhancedStatusCodes,
			smtp.Ext8BitMIME,
			smtp.ExtDSN,
			smtp.ExtSMTPUTF8,
			smtp.ExtChunking,
		},
		NewSink: newSink,
	})
}

// containerfileDir returns the absolute directory holding this profile's
// Containerfile, derived from this source file's own path via
// runtime.Caller so it resolves correctly regardless of the test binary's
// working directory (which "go test" sets to the package under test, not
// the module root).
func containerfileDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(file)
}

func newSink(ctx context.Context, h *harness.Handle) (harness.Sink, error) {
	return harness.MaildirSink{
		Exec: h,
		Dir:  func(recipient string) string { return maildir },
	}, nil
}
