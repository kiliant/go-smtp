// Package harness provides the reusable primitives for running SMTP/LMTP
// interoperability tests against real servers under podman: container
// lifecycle, capability profiles, sinks that read back delivered mail,
// fixtures, and failure classification.
//
// This package performs no I/O against smtpclient itself and duplicates
// neither the wire codec (internal/smtpwire) nor connection/reply handling
// (smtpclient). It drives servers as an external black box and, where a
// smoke test is required, uses smtpclient's public Dial/EHLO surface only.
package harness

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

// EnvServerSubset is the environment variable that restricts a run to a
// comma-separated subset of registered server names, e.g.
// "postfix,mailpit". Documented in docs/INTEROP.md.
const EnvServerSubset = "GO_SMTP_INTEROP_SERVERS"

// Config bounds one harness run. Zero value is valid and uses the documented
// defaults; construct via keyed fields only, since new timeout fields are
// expected as the matrix grows.
type Config struct {
	// Subset restricts the run to these server names, matched
	// case-insensitively against Profile.Name. A nil or empty slice runs
	// every registered profile subject to Tier filtering.
	Subset []string
	// StartTimeout bounds container creation and start.
	StartTimeout time.Duration
	// HealthTimeout bounds polling for a real EHLO greeting after the
	// container reports started.
	HealthTimeout time.Duration
	// CommandTimeout bounds any single SMTP command issued by the harness
	// (EHLO, the smoke transaction).
	CommandTimeout time.Duration
	// SinkTimeout bounds a sink read-back, which may itself poll (e.g. an
	// HTTP API that has not yet indexed a just-delivered message).
	SinkTimeout time.Duration
	// StopTimeout bounds container stop/remove during cleanup.
	StopTimeout time.Duration
	// IncludeEmulated allows Tier 3 (non-native-architecture) profiles to
	// run. It exists so the interop_emulated build tag is the only normal
	// gate; tests may still set this explicitly.
	IncludeEmulated bool

	_ struct{}
}

const (
	defaultStartTimeout   = 60 * time.Second
	defaultHealthTimeout  = 45 * time.Second
	emulatedHealthTimeout = 90 * time.Second
	defaultCommandTimeout = 10 * time.Second
	defaultSinkTimeout    = 20 * time.Second
	defaultStopTimeout    = 20 * time.Second
)

// LoadConfig builds a Config from the process environment, applying
// documented defaults for anything unset. It never contacts podman or a
// network endpoint.
func LoadConfig() Config {
	cfg := Config{
		StartTimeout:    defaultStartTimeout,
		HealthTimeout:   defaultHealthTimeout,
		CommandTimeout:  defaultCommandTimeout,
		SinkTimeout:     defaultSinkTimeout,
		StopTimeout:     defaultStopTimeout,
		IncludeEmulated: emulatedBuildTag,
	}
	// Apache James runs an amd64 JVM through qemu on the arm64 development
	// host. Its cold start is observably slower and more variable than every
	// native profile; keep that cost behind the explicit build tag.
	if emulatedBuildTag {
		cfg.HealthTimeout = emulatedHealthTimeout
	}
	if raw := os.Getenv(EnvServerSubset); raw != "" {
		cfg.Subset = splitSubset(raw)
	}
	return cfg
}

func splitSubset(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.ToLower(strings.TrimSpace(p))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Selects reports whether name is included by the subset filter. An empty
// filter selects everything.
func (c Config) Selects(name string) bool {
	if len(c.Subset) == 0 {
		return true
	}
	return slices.Contains(c.Subset, strings.ToLower(name))
}

func (c Config) String() string {
	return fmt.Sprintf("Config{Subset:%v, IncludeEmulated:%v}", c.Subset, c.IncludeEmulated)
}
