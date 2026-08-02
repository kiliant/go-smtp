// Package greenmail registers the GreenMail interop profile: a JVM
// implementation with a minimal extension set (docs/INTEROP.md).
//
// Verified running on 2026-08-02 against docker.io/greenmail/standalone:2.1.9
// with GREENMAIL_OPTS enabling the SMTP service, a static
// interop:interop-pw@example.test user and disabled auth enforcement. EHLO
// advertised
//
//	AUTH PLAIN LOGIN XOAUTH2, SMTPUTF8
//
// and a message seeded independently of go-smtp round-tripped through
// GET /api/user/{email}/messages, whose "mimeMessage" field carries the raw
// content (confirmed byte-preserving for a body line of exactly ".").
package greenmail

import (
	"context"
	"fmt"
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
			Image: "docker.io/greenmail/standalone:2.1.9",
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

func (s *sink) Reset(ctx context.Context, recipient string) error {
	return harness.DeleteURL(ctx, s.messagesURL(recipient))
}

func (s *sink) messagesURL(recipient string) string {
	return s.baseURL + "/api/user/" + recipient + "/messages"
}
