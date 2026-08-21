// Command lmtp runs an RFC 2033 listener and constructs one final result per
// accepted recipient.
package main

import (
	"context"
	"flag"
	"io"
	"net"
	"os/signal"
	"syscall"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/smtpserver"
)

func main() {
	address := flag.String("listen", "127.0.0.1:2424", "LMTP listen address")
	flag.Parse()
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		panic(err)
	}
	backend := &smtpserver.Backend{NewSession: newLMTPSession}
	server, err := smtpserver.NewServer(&smtpserver.ServerOptions{
		Listener: listener,
		Backend:  backend,
		Mode:     smtpserver.ModeLMTP,
	})
	if err != nil {
		panic(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := server.Serve(ctx, nil); err != nil && ctx.Err() == nil {
		panic(err)
	}
}

func newLMTPSession(context.Context, *smtpserver.ConnInfo, *smtpserver.NewSessionOptions) (*smtpserver.Session, error) {
	var recipients []string
	return &smtpserver.Session{
		Mail: func(context.Context, string, *smtp.MailOptions, *smtpserver.MailOptions) error {
			recipients = nil
			return nil
		},
		Rcpt: func(_ context.Context, recipient string, _ *smtp.RcptOptions, _ *smtpserver.RcptOptions) error {
			recipients = append(recipients, recipient)
			return nil
		},
		Data: func(_ context.Context, message io.Reader, _ *smtpserver.DataOptions) (smtp.DataResult, error) {
			if _, err := io.Copy(io.Discard, message); err != nil {
				return nil, err
			}
			result := make(smtp.DataResult, len(recipients))
			for i, recipient := range recipients {
				result[i] = smtp.RecipientResult{
					Recipient: recipient,
					Command:   "DATA",
					Code:      250,
					Enhanced:  smtp.EnhancedCode{Class: 2, Subject: 0, Detail: 0},
					Text:      "delivered",
				}
			}
			return result, nil
		},
		Reset: func(context.Context, smtpserver.ResetReason, *smtpserver.ResetOptions) {
			recipients = nil
		},
		Close: func(context.Context, *smtpserver.CloseOptions) {},
	}, nil
}
