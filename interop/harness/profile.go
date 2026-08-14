package harness

import (
	"context"

	smtp "github.com/kiliant/go-smtp"
)

// Tier groups servers by how directly they can run on this host.
type Tier int

const (
	// Tier1 servers run arm64-native and are the default matrix.
	Tier1 Tier = 1
	// Tier2 servers also run arm64-native but are less widely deployed or
	// exercise a narrower bug class.
	Tier2 Tier = 2
	// Tier3 servers have no confirmed native image and require emulation;
	// they run only under the interop_emulated build tag.
	Tier3 Tier = 3
)

// Port is one service a server profile exposes, tagged with what it speaks
// so the harness dials the right protocol expectations (e.g. LMTP does not
// send AUTH the way submission does).
type Port struct {
	// Container is the profile's logical service port. Container-backed
	// profiles use the actual container port; in-process profiles map the same
	// key to a directly bound address.
	Container int
	// Kind documents what the port serves: "smtp", "submission", "smtps"
	// (implicit TLS), or "lmtp".
	Kind string
	// ImplicitTLS mirrors ClientOptions.ImplicitTLS for this port (true for
	// the 465 convention).
	ImplicitTLS bool
}

// RuntimeConfig describes a started non-container profile. Start callbacks
// return it after binding their listeners; StartProfile wraps it in the same
// concrete Handle used for containers so health checks and scenarios do not
// branch on runtime kind.
//
// Callers constructing a RuntimeConfig literal must use keyed fields.
type RuntimeConfig struct {
	// Addresses maps each logical Profile port to a dialable host address.
	Addresses map[int]string
	// Sink reads back messages accepted by this runtime. Nil means the profile
	// intentionally has no retrieval sink.
	Sink Sink
	// Stop releases the runtime. Nil means no cleanup is necessary.
	Stop func(ctx context.Context) error
	// Logs returns diagnostic output after a failure. Nil means the runtime
	// has no separate log stream.
	Logs func(ctx context.Context) (string, error)

	_ struct{}
}

// Profile describes one interoperability target: how to start it, what it is
// expected to advertise, and how to read back what it received. Construct
// with keyed fields — new servers add fields (a provisioning hook, a
// second account) without breaking existing profiles.
type Profile struct {
	// Name identifies the server, matched against GO_SMTP_INTEROP_SERVERS.
	Name string
	// Tier controls whether the profile runs by default or requires
	// interop_emulated.
	Tier Tier
	// Run is fully populated except for Name, which the registry fills in
	// from Profile.Name so callers do not repeat it. Container-backed profiles
	// set Run; non-container profiles leave it empty and set Start.
	Run RunConfig
	// Start launches a non-container profile. Nil selects the existing Podman
	// Run path. Returning does not establish readiness: AssertProfile still
	// performs the real EHLO/LHLO health gate.
	Start func(ctx context.Context) (*RuntimeConfig, error)
	// Ports are the services this server exposes.
	Ports []Port
	// ExpectedExtensions are the EHLO keywords this profile's target is
	// configured to advertise. The harness treats a missing expected
	// keyword as OutcomeProfileViolation (a broken or downgraded
	// container), and any other unadvertised keyword a test wants as
	// OutcomeIncompatible (skip).
	ExpectedExtensions []smtp.Extension
	// NewSink builds a container-backed server's message-retrieval sink.
	// Non-container profiles supply RuntimeConfig.Sink instead.
	NewSink func(ctx context.Context, h *Handle) (Sink, error)

	_ struct{}
}

// SMTPPort returns the first plain-SMTP (kind "smtp") port, if any.
func (p Profile) SMTPPort() (Port, bool) {
	return p.portByKind("smtp")
}

// LMTPPort returns the first LMTP port, if any.
func (p Profile) LMTPPort() (Port, bool) {
	return p.portByKind("lmtp")
}

func (p Profile) portByKind(kind string) (Port, bool) {
	for _, port := range p.Ports {
		if port.Kind == kind {
			return port, true
		}
	}
	return Port{}, false
}
