// Package james registers the Apache James interop profile: a JVM MTA, a
// different bug class again (docs/INTEROP.md).
//
// Architecture re-confirmed 2026-08-02, per T06's requirement to verify
// rather than inherit the claim: `podman manifest inspect
// docker.io/apache/james:demo-3.8.2` returns no manifest list (a single-arch
// image, not a multi-arch one), and `podman inspect` on the pulled image
// reports Architecture=amd64, Os=linux. Tier 3, emulated-only — this
// profile is registered but Selected() excludes it unless the
// interop_emulated build tag is present (see docs/INTEROP.md).
//
// NOT LIVE-VERIFIED under emulation this session: the demo image's account
// provisioning (via its CLI, james-cli AddDomain/AddUser) is not yet driven
// from this Containerfile.
package james

import (
	"context"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/interop/harness"
)

const smtpPort = 25

func init() {
	harness.Register(harness.Profile{
		Name: "james",
		Tier: harness.Tier3,
		Run: harness.RunConfig{
			Image: "docker.io/apache/james:demo-3.8.2",
			Ports: []int{smtpPort},
		},
		Ports: []harness.Port{
			{Container: smtpPort, Kind: "smtp"},
		},
		ExpectedExtensions: []smtp.Extension{
			smtp.ExtPipelining,
			smtp.Ext8BitMIME,
			smtp.ExtEnhancedStatusCodes,
		},
		NewSink: newSink,
	})
}

func newSink(ctx context.Context, h *harness.Handle) (harness.Sink, error) {
	return harness.MaildirSink{
		Exec: h,
		Dir:  func(recipient string) string { return "/root/james-server-app/mail/" + recipient },
	}, nil
}
