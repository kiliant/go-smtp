package harness

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/kiliant/go-smtp/smtpclient"
)

// healthCheckIdentity is the EHLO/LHLO identity the harness uses for its own
// readiness probes. Leaving ClientOptions.Identity empty falls back to
// os.Hostname(), which is a dotted, plausible-looking FQDN on a developer's
// machine but a bare single-label name (e.g. "fv-az123-456") on GitHub-hosted
// CI runners — Stalwart rejects that as an invalid EHLO domain, hanging every
// Stalwart health check until it times out. The probe must not depend on the
// ambient hostname.
const healthCheckIdentity = "interop-harness.example.test"

// WaitForEHLO polls addr with smtpclient.Dial until it completes a full
// greeting-and-EHLO negotiation or ctx is done. This is the harness's health
// gate: containers report "running" long before the SMTP service inside is
// accepting connections, and a fixed sleep is exactly the flakiness source
// docs/INTEROP.md and the task spec both call out. Polling on a real EHLO
// instead means the gate is the same negotiation the client will actually
// perform.
//
// It returns the connected Client (caller must Close it) and a
// *Result classifying the last failure if the deadline was reached first.
func WaitForEHLO(ctx context.Context, addr string, opts *smtpclient.ClientOptions) (*smtpclient.Client, error) {
	cfg := smtpclient.ClientOptions{}
	if opts != nil {
		cfg = *opts
	}
	cfg.Address = addr
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		client, err := smtpclient.Dial(attemptCtx, &cfg)
		cancel()
		if err == nil {
			return client, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("harness: server at %s never became ready: %w (last attempt: %v)", addr, ctx.Err(), lastErr)
		case <-pollTick():
		}
	}
}

// AssertProfile connects to profile's SMTP port and verifies every extension
// the profile claims is actually advertised. A missing claimed extension is
// OutcomeProfileViolation — a hard failure, distinct from a test needing an
// extension the profile never claimed, which is a skip the caller decides on
// its own. AssertProfile does not evaluate any extension beyond what the
// profile lists; it is a health check for the matrix, not a general
// capability probe.
//
// LMTP profiles are negotiated with smtpclient in LMTP mode, so the health
// gate proves a real LHLO exchange rather than merely observing a greeting.
func AssertProfile(ctx context.Context, cfg Config, h *Handle, p Profile) *Result {
	if port, ok := p.SMTPPort(); ok {
		return assertSMTPProfile(ctx, cfg, h, p, port)
	}
	if port, ok := p.LMTPPort(); ok {
		return assertLMTPGreeting(ctx, cfg, h, p, port)
	}
	return NewResult(p.Name, "assert-profile", OutcomeHarnessFailure,
		errors.New("profile declares no smtp or lmtp port"), nil)
}

func assertSMTPProfile(ctx context.Context, cfg Config, h *Handle, p Profile, port Port) *Result {
	addr, ok := h.HostAddr(port.Container)
	if !ok {
		return NewResult(p.Name, "assert-profile", OutcomeHarnessFailure,
			fmt.Errorf("no host port resolved for container port %d", port.Container), nil)
	}

	tr := NewTranscript()
	tr.Recordf("dialing %s (container port %d, kind %s)", addr, port.Container, port.Kind)

	healthCtx, cancel := context.WithTimeout(ctx, cfg.HealthTimeout)
	defer cancel()
	client, err := WaitForEHLO(healthCtx, addr, &smtpclient.ClientOptions{
		Identity:           healthCheckIdentity,
		ImplicitTLS:        port.ImplicitTLS,
		InsecureSkipVerify: true,
		GreetingTimeout:    cfg.CommandTimeout,
		MailTimeout:        cfg.CommandTimeout,
	})
	if err != nil {
		outcome := OutcomeEnvironmental
		if errors.Is(err, context.DeadlineExceeded) {
			outcome = OutcomeTimeout
		}
		tr.Recordf("EHLO negotiation failed: %v", err)
		return NewResult(p.Name, "assert-profile", outcome, err, tr)
	}
	defer func() { _ = client.Close() }()
	tr.Recordf("EHLO negotiation succeeded")

	var missing []string
	for _, ext := range p.ExpectedExtensions {
		if params, ok := client.Extension(ext); ok {
			tr.Recordf("advertised %s params=%q", ext, params)
			continue
		}
		missing = append(missing, string(ext))
	}
	if len(missing) > 0 {
		tr.Recordf("profile claimed but did not advertise: %v", missing)
		return NewResult(p.Name, "assert-profile", OutcomeProfileViolation,
			fmt.Errorf("server did not advertise claimed extension(s) %v", missing), tr)
	}
	return NewResult(p.Name, "assert-profile", OutcomeOK, nil, tr)
}

func assertLMTPGreeting(ctx context.Context, cfg Config, h *Handle, p Profile, port Port) *Result {
	addr, ok := h.HostAddr(port.Container)
	if !ok {
		return NewResult(p.Name, "assert-profile", OutcomeHarnessFailure,
			fmt.Errorf("no host port resolved for container port %d", port.Container), nil)
	}
	tr := NewTranscript()
	tr.Recordf("dialing %s (container port %d, kind lmtp)", addr, port.Container)
	healthCtx, cancel := context.WithTimeout(ctx, cfg.HealthTimeout)
	defer cancel()
	client, err := WaitForEHLO(healthCtx, addr, &smtpclient.ClientOptions{
		Identity:        healthCheckIdentity,
		LMTP:            true,
		GreetingTimeout: cfg.CommandTimeout,
		MailTimeout:     cfg.CommandTimeout,
	})
	if err != nil {
		outcome := OutcomeEnvironmental
		if errors.Is(err, context.DeadlineExceeded) {
			outcome = OutcomeTimeout
		}
		tr.Recordf("LHLO negotiation failed: %v", err)
		return NewResult(p.Name, "assert-profile", outcome, err, tr)
	}
	defer func() { _ = client.Close() }()
	tr.Recordf("LHLO negotiation succeeded")
	var missing []string
	for _, ext := range p.ExpectedExtensions {
		if _, ok := client.Extension(ext); !ok {
			missing = append(missing, string(ext))
		}
	}
	if len(missing) > 0 {
		return NewResult(p.Name, "assert-profile", OutcomeProfileViolation,
			fmt.Errorf("server did not advertise claimed extension(s) %v", missing), tr)
	}
	return NewResult(p.Name, "assert-profile", OutcomeOK, nil, tr)
}

// GreetingCheck polls addr until it reads a line starting "220 " or "220-"
// (RFC 5321 §3.1 / RFC 2033), or ctx is done. It reads the wire directly
// rather than through smtpclient because a protocol (LMTP) this library does
// not yet speak still needs a readiness gate; it never parses beyond the
// greeting line and is not a substitute for AssertProfile's EHLO-based
// capability check.
func GreetingCheck(ctx context.Context, addr string) error {
	d := net.Dialer{}
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := probeGreeting(attemptCtx, d, addr)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return fmt.Errorf("harness: %s never sent a 220 greeting: %w (last attempt: %v)", addr, ctx.Err(), lastErr)
		case <-pollTick():
		}
	}
}

func probeGreeting(ctx context.Context, d net.Dialer, addr string) error {
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return err
	}
	if string(buf) != "220 " && string(buf) != "220-" {
		return fmt.Errorf("unexpected greeting prefix %q", buf)
	}
	return nil
}

// WaitTCP polls addr until a TCP connection succeeds or ctx is done. It is
// the readiness gate for a sink's own service (e.g. an HTTP API) that does
// not speak SMTP, so server profiles do not each reimplement a poll loop.
func WaitTCP(ctx context.Context, addr string) error {
	d := net.Dialer{}
	var lastErr error
	for {
		attemptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		conn, err := d.DialContext(attemptCtx, "tcp", addr)
		cancel()
		if err == nil {
			return conn.Close()
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return fmt.Errorf("harness: %s never became reachable: %w (last attempt: %v)", addr, ctx.Err(), lastErr)
		case <-pollTick():
		}
	}
}
