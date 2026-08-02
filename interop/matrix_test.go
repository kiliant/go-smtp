//go:build interop

// Package interop drives the podman-backed server matrix described in
// docs/INTEROP.md. Run separately from ./smtpclient's own interop-tagged
// tests (see that doc for why): each test process here owns an independent
// set of container lifecycles.
//
//	go test -count=1 -race -tags=interop ./interop/...
//	GO_SMTP_INTEROP_SERVERS=postfix,mailpit go test -count=1 -race -tags=interop ./interop/...
//	go test -count=1 -race -tags='interop interop_emulated' ./interop/...
package interop

import (
	"bytes"
	"context"
	"net/smtp"
	"strings"
	"testing"

	"github.com/kiliant/go-smtp/interop/harness"

	// Blank-imported so each server's init() registers its profile as a
	// side effect of import, independent of test execution order.
	_ "github.com/kiliant/go-smtp/interop/servers/dovecot"
	_ "github.com/kiliant/go-smtp/interop/servers/exim"
	_ "github.com/kiliant/go-smtp/interop/servers/greenmail"
	_ "github.com/kiliant/go-smtp/interop/servers/james"
	_ "github.com/kiliant/go-smtp/interop/servers/maddy"
	_ "github.com/kiliant/go-smtp/interop/servers/mailpit"
	_ "github.com/kiliant/go-smtp/interop/servers/postfix"
	_ "github.com/kiliant/go-smtp/interop/servers/stalwart"
)

const recipient = "interop@example.test"

// TestMatrix starts every profile the harness config selects, asserts its
// advertised capabilities against what its profile claims, and — where the
// profile is wired for it — seeds one message independently of go-smtp
// (net/smtp is the standard library's own frozen client, not this
// project's) and proves the sink reads back the same bytes modulo trace
// headers. This is the "smoke transaction" T06 is done when it passes.
//
// It deliberately does not exercise smtpclient's own MAIL/RCPT/DATA: T06
// depends only on T03, and that command surface is T05's. The capability
// assertion (AssertProfile) is what actually exercises smtpclient today.
func TestMatrix(t *testing.T) {
	cfg := harness.LoadConfig()
	profiles := harness.Selected(cfg)
	if len(profiles) == 0 {
		t.Fatal("no profiles selected: check GO_SMTP_INTEROP_SERVERS and the interop_emulated build tag")
	}

	for _, p := range profiles {
		t.Run(p.Name, func(t *testing.T) {
			t.Parallel()
			runProfile(t, cfg, p)
		})
	}
}

func runProfile(t *testing.T, cfg harness.Config, p harness.Profile) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.StartTimeout+cfg.HealthTimeout+cfg.SinkTimeout)
	defer cancel()

	run := p.Run
	run.Name = harness.ContainerName(p.Name)
	h, err := harness.Run(ctx, run)
	if err != nil {
		t.Fatalf("starting %s: %v", p.Name, err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), cfg.StopTimeout)
		defer stopCancel()
		if err := h.Stop(stopCtx); err != nil {
			t.Errorf("stopping %s: %v", p.Name, err)
		}
	})

	result := harness.AssertProfile(ctx, cfg, h, p)
	logTranscript(t, result)
	switch result.Outcome {
	case harness.OutcomeOK:
		// continue to the sink smoke test below
	case harness.OutcomeProfileViolation:
		t.Fatalf("%s: profile claims an extension it did not advertise: %v", p.Name, result.Err)
	case harness.OutcomeTimeout:
		t.Fatalf("%s: never became ready within the health timeout: %v", p.Name, result.Err)
	case harness.OutcomeEnvironmental:
		t.Fatalf("%s: environment did not allow the assertion: %v", p.Name, result.Err)
	default:
		t.Fatalf("%s: assert-profile: %v", p.Name, result.Err)
	}

	if p.NewSink == nil {
		return
	}
	sink, err := p.NewSink(ctx, h)
	if err != nil {
		t.Fatalf("%s: building sink: %v", p.Name, err)
	}
	// Reset is best-effort here: the container is freshly started, so an
	// empty inbox does not depend on it succeeding, and this profile's sink
	// API details are not all independently confirmed (see per-server doc
	// comments). A scenario suite built on top of this harness (T05) should
	// treat a Reset failure as fatal; a smoke test should not block on it.
	if err := sink.Reset(ctx, recipient); err != nil {
		t.Logf("%s: resetting sink (best-effort): %v", p.Name, err)
	}

	port, ok := p.SMTPPort()
	if !ok {
		t.Logf("%s: no plain SMTP port; skipping the sink smoke transaction", p.Name)
		return
	}
	addr, ok := h.HostAddr(port.Container)
	if !ok {
		t.Fatalf("%s: no host port resolved for container port %d", p.Name, port.Container)
	}

	fixture, ok := harness.FixtureByName("plain-ascii")
	if !ok {
		t.Fatal("plain-ascii fixture missing from harness.Fixtures")
	}
	body := []byte("From: " + recipient + "\r\nTo: " + recipient + "\r\nSubject: go-smtp interop smoke\r\n\r\n")
	body = append(body, fixture.Body...)

	seedCtx, seedCancel := context.WithTimeout(ctx, cfg.CommandTimeout)
	defer seedCancel()
	if err := seedMessage(seedCtx, addr, recipient, body); err != nil {
		t.Fatalf("%s: seeding smoke message: %v", p.Name, err)
	}

	sinkCtx, sinkCancel := context.WithTimeout(ctx, cfg.SinkTimeout)
	defer sinkCancel()
	got, err := harness.WaitForMessage(sinkCtx, sink, recipient)
	if err != nil {
		t.Fatalf("%s: reading back the seeded message: %v", p.Name, err)
	}
	// Two normalisations account for real, observed server behavior rather
	// than loosening the check arbitrarily:
	//   - Local delivery agents commonly rewrite the wire's CRLF to the
	//     host mailbox format's native line ending (confirmed here:
	//     Postfix's and Exim's maildir transports both store LF-only).
	//   - GreenMail's JSON message API drops the final line terminator
	//     (confirmed here: a two-line body loses only its very last CRLF).
	// Neither is data loss in a sense a client bug would produce, so both
	// sides are normalised to LF and right-trimmed before the containment
	// check. This is intentionally looser than the dot-stuffing and
	// line-length fixtures will need to be once T05 lands and drives the
	// full fixture table through this sink.
	want := bytes.TrimRight(normalizeCRLF(fixture.Body), "\n")
	have := bytes.TrimRight(normalizeCRLF(got.Raw), "\n")
	if !bytes.Contains(have, want) {
		t.Fatalf("%s: sink content does not contain the submitted body\nsubmitted: %q\nretrieved: %q",
			p.Name, fixture.Body, got.Raw)
	}
}

func normalizeCRLF(b []byte) []byte {
	return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
}

// seedMessage delivers body to rcpt using net/smtp — the standard library's
// own frozen client, not this project's — so the sink side of the harness
// can be proven independently of the smtpclient command surface T06 does
// not depend on (T05 owns MAIL/RCPT/DATA).
//
// It drives net/smtp.Client directly rather than the smtp.SendMail
// convenience wrapper, which opportunistically attempts STARTTLS whenever a
// server advertises it and has no option to skip certificate verification —
// every self-signed cert in this matrix would otherwise fail the handshake.
// Seeding stays in cleartext deliberately: it is test fixture setup, not a
// TLS assertion.
func seedMessage(ctx context.Context, addr, rcpt string, body []byte) error {
	type result struct{ err error }
	done := make(chan result, 1)
	go func() {
		done <- result{seedMessageSync(addr, rcpt, body)}
	}()
	select {
	case r := <-done:
		return r.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func seedMessageSync(addr, rcpt string, body []byte) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.Hello("go-smtp-interop-seed"); err != nil {
		return err
	}
	if err := c.Mail(rcpt); err != nil {
		return err
	}
	if err := c.Rcpt(rcpt); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func logTranscript(t *testing.T, r *harness.Result) {
	t.Helper()
	if r == nil || r.Transcript == nil {
		return
	}
	lines := r.Transcript.Lines()
	if len(lines) == 0 {
		return
	}
	t.Log(strings.Join(lines, "\n"))
}
