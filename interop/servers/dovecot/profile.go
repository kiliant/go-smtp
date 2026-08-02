// Package dovecot registers the Dovecot LMTP interop profile: the RFC 2033
// reference implementation, and the only Tier 1 server that exercises
// per-recipient DATA replies against a real LMTP service.
//
// The base image (docker.io/dovecot/dovecot:2.4.3) ships only an
// implicit-TLS LMTP listener on port 31024 and no userdb. The Containerfile
// adds a plain listener on the conventional port 24 (binding it needs
// CAP_NET_BIND_SERVICE, since the image runs as the non-root "vmail" user)
// and a static userdb so any recipient resolves to a maildir under
// /srv/vmail. Verified running on 2026-08-02.
//
// smtpclient does not send LHLO until T07 lands, so this profile's assertion
// step is a greeting check rather than an EHLO capability check — see
// harness.AssertProfile.
package dovecot

import (
	"context"
	"path/filepath"
	"runtime"

	"github.com/kiliant/go-smtp/interop/harness"
)

const lmtpPort = 24

func init() {
	harness.Register(harness.Profile{
		Name: "dovecot",
		Tier: harness.Tier1,
		Run: harness.RunConfig{
			ContainerfileDir: containerfileDir(),
			Ports:            []int{lmtpPort},
			CapAdd:           []string{"NET_BIND_SERVICE"},
		},
		Ports: []harness.Port{
			{Container: lmtpPort, Kind: "lmtp"},
		},
		// No ExpectedExtensions: LMTP capability assertion is deferred to
		// T07 (see package doc).
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
		Dir: func(recipient string) string {
			return "/srv/vmail/" + recipient + "/mail"
		},
	}, nil
}
