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
// The profile provisions example.test/interop@example.test through the image's
// bundled james-cli after SMTP becomes ready, then reads delivery back over
// implicit-TLS IMAP. Verified live under qemu emulation on 2026-08-03.
package james

import (
	"context"
	"fmt"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/interop/harness"
)

const (
	smtpPort  = 25
	imapsPort = 993
)

func init() {
	harness.Register(harness.Profile{
		Name: "james",
		Tier: harness.Tier3,
		Run: harness.RunConfig{
			Image: "docker.io/apache/james:demo-3.8.2",
			Ports: []int{smtpPort, imapsPort},
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
	// The demo image only seeds james.local. Use its supported CLI rather than
	// weakening SMTP relay checks or pretending the JPA mailbox is a maildir.
	if _, err := h.Exec(ctx, "james-cli", "AddDomain", interopDomain); err != nil {
		return nil, fmt.Errorf("james: provisioning domain: %w", err)
	}
	if _, err := h.Exec(ctx, "james-cli", "AddUser", interopRecipient, interopPassword); err != nil {
		return nil, fmt.Errorf("james: provisioning user: %w", err)
	}
	addr, ok := h.HostAddr(imapsPort)
	if !ok {
		return nil, fmt.Errorf("james: no host port resolved for IMAPS (container port %d)", imapsPort)
	}
	return &imapSink{addr: addr, password: interopPassword}, nil
}
