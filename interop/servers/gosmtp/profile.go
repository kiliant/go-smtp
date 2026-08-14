// Package gosmtp provides the in-process go-smtp server target used by the
// interoperability matrix. Unlike every pre-M6 target, it has no image and no
// container lifecycle: Start binds a loopback listener and Sink reads the
// smtpserver/memory backend directly.
package gosmtp

import (
	"context"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/interop/harness"
)

const (
	// Name is the stable server-matrix profile name.
	Name = "gosmtp"
	// SMTPPort is the logical profile port. The in-process target binds an
	// ephemeral host port; a container-less harness handle maps this logical
	// port to Target.Addr.
	SMTPPort = 25
)

var expectedExtensions = []smtp.Extension{
	smtp.ExtPipelining,
	smtp.ExtSize,
	smtp.Ext8BitMIME,
	smtp.ExtEnhancedStatusCodes,
	smtp.ExtSMTPUTF8,
	smtp.ExtChunking,
	smtp.ExtBinaryMIME,
	smtp.ExtDSN,
	smtp.ExtDeliverBy,
	smtp.ExtFutureRelease,
	smtp.ExtMTPriority,
	smtp.ExtRRVS,
	smtp.ExtRequireTLS,
	smtp.ExtNoSoliciting,
	smtp.ExtMTRK,
	smtp.ExtSubmitter,
	smtp.ExtConPerm,
	smtp.ExtConNeg,
	smtp.ExtLimits,
}

// ExpectedExtensions returns the capabilities the default in-process target
// must advertise. A copy is returned so one matrix scenario cannot weaken the
// profile assertion for another.
func ExpectedExtensions() []smtp.Extension {
	return append([]smtp.Extension(nil), expectedExtensions...)
}

func init() {
	harness.Register(harness.Profile{
		Name: Name,
		Tier: harness.Tier1,
		Start: func(ctx context.Context) (*harness.RuntimeConfig, error) {
			target, err := Start(ctx)
			if err != nil {
				return nil, err
			}
			return &harness.RuntimeConfig{
				Addresses: map[int]string{SMTPPort: target.Addr()},
				Sink:      target.Sink(),
				Stop:      target.Stop,
			}, nil
		},
		Ports: []harness.Port{
			{Container: SMTPPort, Kind: "smtp"},
		},
		ExpectedExtensions: ExpectedExtensions(),
	})
}
