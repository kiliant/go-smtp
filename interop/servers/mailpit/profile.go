// Package mailpit registers the Mailpit interop profile.
//
// Mailpit (docker.io/axllent/mailpit) is a deliberately minimal SMTP sink
// with an HTTP retrieval API. Its EHLO reply, captured from a live container
// on 2026-08-02:
//
//	250-<host> greets test.local
//	250-SIZE 52428800
//	250-ENHANCEDSTATUSCODES
//	250-8BITMIME
//	250 SMTPUTF8
//
// No PIPELINING and no STARTTLS by default, which is exactly the point: it
// catches client assumptions about optional extensions that the "aggressive
// coverage" servers (Stalwart) would never expose.
package mailpit

import (
	"context"
	"fmt"
	"net/url"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/interop/harness"
)

const (
	smtpPort = 1025
	httpPort = 8025
)

func init() {
	harness.Register(harness.Profile{
		Name: "mailpit",
		Tier: harness.Tier1,
		Run: harness.RunConfig{
			Image: "docker.io/axllent/mailpit@sha256:e427cc84ef7b68b656a80093f677767d5eafdde67ec871238a670f0bd4d89ad2",
			Ports: []int{smtpPort, httpPort},
		},
		Ports: []harness.Port{
			{Container: smtpPort, Kind: "smtp"},
		},
		ExpectedExtensions: []smtp.Extension{
			smtp.ExtSize,
			smtp.ExtEnhancedStatusCodes,
			smtp.Ext8BitMIME,
			smtp.ExtSMTPUTF8,
		},
		NewSink: newSink,
	})
}

func newSink(ctx context.Context, h *harness.Handle) (harness.Sink, error) {
	addr, ok := h.HostAddr(httpPort)
	if !ok {
		return nil, fmt.Errorf("mailpit: no host port resolved for HTTP API (container port %d)", httpPort)
	}
	if err := harness.WaitTCP(ctx, addr); err != nil {
		return nil, err
	}
	return &sink{baseURL: "http://" + addr}, nil
}

type sink struct {
	baseURL string
}

type searchResponse struct {
	Messages []struct {
		ID string `json:"ID"`
	} `json:"messages"`
}

func (s *sink) Fetch(ctx context.Context, recipient string) ([]harness.Message, error) {
	query := url.QueryEscape("to:" + recipient)
	var resp searchResponse
	if err := harness.GetJSON(ctx, s.baseURL+"/api/v1/search?query="+query, &resp); err != nil {
		return nil, err
	}
	msgs := make([]harness.Message, 0, len(resp.Messages))
	for _, m := range resp.Messages {
		raw, err := harness.GetBytes(ctx, s.baseURL+"/api/v1/message/"+m.ID+"/raw")
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, harness.Message{Recipient: recipient, Raw: raw})
	}
	return msgs, nil
}

// Reset deletes only the messages currently matching recipient, by ID.
// Mailpit's bulk-delete endpoint deletes the entire inbox when given no
// body, which would silently wipe another recipient's or scenario's mail
// mid-run — every call here must pass an explicit ID list.
func (s *sink) Reset(ctx context.Context, recipient string) error {
	query := url.QueryEscape("to:" + recipient)
	var resp searchResponse
	if err := harness.GetJSON(ctx, s.baseURL+"/api/v1/search?query="+query, &resp); err != nil {
		return err
	}
	if len(resp.Messages) == 0 {
		return nil
	}
	ids := make([]string, 0, len(resp.Messages))
	for _, m := range resp.Messages {
		ids = append(ids, m.ID)
	}
	return harness.DeleteJSON(ctx, s.baseURL+"/api/v1/messages", struct {
		IDs []string `json:"IDs"`
	}{IDs: ids})
}
