// Command submission runs an RFC 6409 submission listener requiring RFC 3207
// STARTTLS and RFC 4954 AUTH, with an RFC 1870 message-size limit.
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"net"
	"os/signal"
	"syscall"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/smtpserver"
	"github.com/kiliant/go-smtp/smtpserver/memory"
)

func main() {
	address := flag.String("listen", "127.0.0.1:2587", "submission listen address")
	certFile := flag.String("cert", "cert.pem", "TLS certificate")
	keyFile := flag.String("key", "key.pem", "TLS private key")
	flag.Parse()

	certificate, err := tls.LoadX509KeyPair(*certFile, *keyFile)
	if err != nil {
		panic(err)
	}
	listener, err := net.Listen("tcp", *address)
	if err != nil {
		panic(err)
	}

	sink := memory.New(nil)
	storage := sink.Backend()
	backend := &smtpserver.Backend{NewSession: func(ctx context.Context, conn *smtpserver.ConnInfo, opts *smtpserver.NewSessionOptions) (*smtpserver.Session, error) {
		session, err := storage.NewSession(ctx, conn, opts)
		if err != nil {
			return nil, err
		}
		session.Authenticate = authenticate
		session.CommitAuth = func(context.Context, *smtpserver.AuthResult, *smtpserver.CommitAuthOptions) {}
		return session, nil
	}}
	server, err := smtpserver.NewServer(&smtpserver.ServerOptions{
		Listener:               listener,
		Backend:                backend,
		TLSConfig:              &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12},
		RequireTLS:             true,
		RequireAuth:            true,
		AuthMechanismsAfterTLS: []string{"PLAIN"},
		MaxMessageBytes:        10 << 20,
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

// authenticate only verifies credentials. CommitAuth above is the sole place
// where an application would record the authenticated RFC 4954 identity.
func authenticate(_ context.Context, credentials *smtpserver.Credentials, _ *smtpserver.AuthenticateOptions) (*smtpserver.AuthResult, error) {
	if credentials.AuthenticationID == "submitter" && credentials.Password == "change-me" {
		return &smtpserver.AuthResult{Identity: "submitter"}, nil
	}
	return &smtpserver.AuthResult{Failure: &smtpserver.AuthFailure{Err: &smtp.Error{
		Code:     535,
		Enhanced: smtp.EnhancedCode{Class: 5, Subject: 7, Detail: 8},
		Text:     "authentication credentials invalid",
		Command:  "AUTH",
	}}}, nil
}
