// Command custom-backend runs an RFC 5321 SMTP server whose Session implements
// exactly the five required backend handlers.
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
	address := flag.String("listen", "127.0.0.1:2526", "SMTP listen address")
	flag.Parse()
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		panic(err)
	}
	server, err := smtpserver.NewServer(&smtpserver.ServerOptions{
		Listener: listener,
		Backend:  &smtpserver.Backend{NewSession: newSession},
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

func newSession(context.Context, *smtpserver.ConnInfo, *smtpserver.NewSessionOptions) (*smtpserver.Session, error) {
	return &smtpserver.Session{
		Mail: func(context.Context, string, *smtp.MailOptions, *smtpserver.MailOptions) error {
			return nil
		},
		Rcpt: func(context.Context, string, *smtp.RcptOptions, *smtpserver.RcptOptions) error {
			return nil
		},
		Data: func(_ context.Context, message io.Reader, _ *smtpserver.DataOptions) (smtp.DataResult, error) {
			if _, err := io.Copy(io.Discard, message); err != nil {
				return nil, err
			}
			return smtp.DataResult{{
				Command:  "DATA",
				Code:     250,
				Enhanced: smtp.EnhancedCode{Class: 2, Subject: 0, Detail: 0},
				Text:     "accepted",
			}}, nil
		},
		Reset: func(context.Context, smtpserver.ResetReason, *smtpserver.ResetOptions) {},
		Close: func(context.Context, *smtpserver.CloseOptions) {},
	}, nil
}
