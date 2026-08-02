// Package exim registers the Exim interop profile.
//
// Exim publishes no maintained multi-arch image under a well-known name;
// docker.io/exim/exim4 returns "requested access to the resource is denied"
// (confirmed 2026-08-02). This profile builds arm64-native from Debian
// packages instead (see Containerfile), mirroring how the sibling repo
// handles Cyrus.
//
// Verified end to end on 2026-08-02 against exim4-daemon-heavy 4.96-15
// (Debian bookworm): EHLO advertised
//
//	SIZE, 8BITMIME, PIPELINING, PIPECONNECT, CHUNKING, STARTTLS, SMTPUTF8
//
// and a message seeded independently of go-smtp round-tripped through the
// maildir sink, including a body line consisting of exactly ".".
package exim

import (
	"context"
	"path/filepath"
	"runtime"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/interop/harness"
)

const (
	smtpPort = 25
	maildir  = "/home/interop/Maildir"
)

func init() {
	harness.Register(harness.Profile{
		Name: "exim",
		Tier: harness.Tier2,
		Run: harness.RunConfig{
			ContainerfileDir: containerfileDir(),
			Ports:            []int{smtpPort},
		},
		Ports: []harness.Port{
			{Container: smtpPort, Kind: "smtp"},
		},
		ExpectedExtensions: []smtp.Extension{
			smtp.ExtSize,
			smtp.Ext8BitMIME,
			smtp.ExtPipelining,
			smtp.ExtChunking,
			smtp.ExtStartTLS,
			smtp.ExtSMTPUTF8,
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
		Dir:  func(recipient string) string { return maildir },
	}, nil
}
