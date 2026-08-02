// Package stalwart registers the Stalwart interop profile: the "aggressive
// coverage" server (SMTPUTF8, REQUIRETLS, DSN, CHUNKING) docs/INTEROP.md
// calls out.
//
// It uses a pinned image and a checked-in static configuration so startup is
// reproducible and does not depend on an interactive bootstrap wizard.
package stalwart

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/interop/harness"
)

const (
	smtpPort = 25
	imapPort = 143
)

func init() {
	harness.Register(harness.Profile{
		Name: "stalwart",
		Tier: harness.Tier1,
		Run: harness.RunConfig{
			ContainerfileDir: containerfileDir(),
			Ports:            []int{smtpPort, imapPort},
		},
		Ports: []harness.Port{
			{Container: smtpPort, Kind: "smtp"},
			{Container: imapPort, Kind: "imap"},
		},
		ExpectedExtensions: []smtp.Extension{
			smtp.ExtStartTLS,
			smtp.ExtSize,
			smtp.Ext8BitMIME,
			smtp.ExtSMTPUTF8,
			smtp.ExtEnhancedStatusCodes,
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
	addr, ok := h.HostAddr(imapPort)
	if !ok {
		return nil, fmt.Errorf("stalwart: no host port resolved for IMAP (container port %d)", imapPort)
	}
	return &imapSink{addr: addr, username: "interop@example.test", password: "interop-pw"}, nil
}
