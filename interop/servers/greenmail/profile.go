// Package greenmail registers the GreenMail interop profile: a JVM
// implementation with a minimal extension set (docs/INTEROP.md).
//
// Verified running on 2026-08-02 against docker.io/greenmail/standalone:2.1.9,
// pinned below to that tag's multi-arch index digest. GREENMAIL_OPTS enables
// the SMTP service, a static
// interop:interop-pw@example.test user and disabled auth enforcement. EHLO
// advertised
//
//	AUTH PLAIN LOGIN XOAUTH2, SMTPUTF8
//
// and a message seeded independently of go-smtp round-tripped through
// GET /api/user/{email}/messages, whose "mimeMessage" field carries the raw
// content (confirmed byte-preserving for a body line of exactly ".").
//
// Reset: GET /api/user/{email}/messages only ever advertises
// "Allow: HEAD, GET, OPTIONS" (confirmed live with an OPTIONS probe against a
// running container on 2026-08-03) — there is no per-recipient delete, so a
// DELETE against that URL is a guaranteed 405, not a transient failure. The
// JAX-RS resource embedded in the standalone jar
// (com/icegreen/greenmail/standalone/GreenMailApiResource.class, inspected
// directly since the image ships no separate API docs) does expose a real
// purge, just scoped to the whole instance rather than one mailbox:
//
//	POST /api/mail/purge      -> purgeEmailFromAllMailboxes(), {"message":"Purged mails"}
//	POST /api/service/reset   -> re-provisions from GreenMailConfiguration, {"message":"Performed reset"}
//
// Both were confirmed live: send a probe message, call the endpoint, refetch
// (empty), send another probe, refetch (delivered) — so the configured
// account survives either call. /api/mail/purge is used here because it only
// touches mail, not the whole managed-server/user state /api/service/reset
// reinitializes. It purges every mailbox, not just recipient's, which is
// broader than the Sink.Reset(recipient) contract technically asks for; that
// is harmless for this harness because every profile provisions exactly one
// interop account and Reset is only ever called once, right after container
// start, before any recipient has mail. A scenario suite exercising multiple
// recipients concurrently in one GreenMail container would need to stop
// relying on this method scoping by recipient at all.
package greenmail

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/interop/harness"
)

const (
	smtpPort  = 3025
	httpPort  = 8080
	greenOpts = "-Dgreenmail.setup.test.smtp -Dgreenmail.hostname=0.0.0.0 " +
		"-Dgreenmail.users=interop:interop-pw@example.test -Dgreenmail.auth.disabled " +
		"-Dgreenmail.api.cors.origin=*"
)

func init() {
	harness.Register(harness.Profile{
		Name: "greenmail",
		Tier: harness.Tier2,
		Run: harness.RunConfig{
			Image: "docker.io/greenmail/standalone@sha256:3ac5a83dd6727cf95e4d50e18907fb8ee7bbf5f67e8534714dee2fb1b5b2e1d4",
			Ports: []int{smtpPort, httpPort},
			Env: map[string]string{
				"GREENMAIL_OPTS": greenOpts,
			},
		},
		Ports: []harness.Port{
			{Container: smtpPort, Kind: "smtp"},
		},
		ExpectedExtensions: []smtp.Extension{
			smtp.ExtAuth,
			smtp.ExtSMTPUTF8,
		},
		NewSink: newSink,
	})
}

func newSink(ctx context.Context, h *harness.Handle) (harness.Sink, error) {
	addr, ok := h.HostAddr(httpPort)
	if !ok {
		return nil, fmt.Errorf("greenmail: no host port resolved for API (container port %d)", httpPort)
	}
	s := &sink{baseURL: "http://" + addr}
	// The JVM opens the API socket before the HTTP handler is ready; a TCP-only
	// readiness check races with that startup window and produces EOF here.
	for {
		var messages []apiMessage
		if err := harness.GetJSON(ctx, s.messagesURL("interop@example.test"), &messages); err == nil {
			return s, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("greenmail: API never became ready: %w", ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

type sink struct {
	baseURL string
}

type apiMessage struct {
	UID         string `json:"uid"`
	MimeMessage string `json:"mimeMessage"`
}

func (s *sink) Fetch(ctx context.Context, recipient string) ([]harness.Message, error) {
	var resp []apiMessage
	if err := harness.GetJSON(ctx, s.messagesURL(recipient), &resp); err != nil {
		return nil, err
	}
	msgs := make([]harness.Message, 0, len(resp))
	for _, m := range resp {
		msgs = append(msgs, harness.Message{Recipient: recipient, Raw: []byte(m.MimeMessage)})
	}
	return msgs, nil
}

// Reset purges every message GreenMail is holding, for every mailbox, via
// POST /api/mail/purge. recipient is unused: GreenMail's REST API (inspected
// directly from the standalone jar's GreenMailApiResource, see the package
// doc comment) advertises no per-recipient delete on
// /api/user/{email}/messages — only HEAD, GET and OPTIONS — so a DELETE
// there always fails with 405, and there is no narrower purge to call
// instead. See the package doc comment for why an instance-wide purge is
// still correct for how this harness uses Reset.
func (s *sink) Reset(ctx context.Context, recipient string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/api/mail/purge", nil)
	if err != nil {
		return fmt.Errorf("greenmail: building purge request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("greenmail: POST /api/mail/purge: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("greenmail: POST /api/mail/purge: unexpected status %s: %s", resp.Status, body)
	}
	return nil
}

func (s *sink) messagesURL(recipient string) string {
	return s.baseURL + "/api/user/" + recipient + "/messages"
}
