package smtpserver

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"strings"
	"testing"

	"github.com/kiliant/go-smtp"
)

func TestNewServerAggregatesPublicOptionDefects(t *testing.T) {
	listener := &stubListener{addr: &net.TCPAddr{Port: 25}}
	_, err := NewServer(&ServerOptions{
		Listener:         listener,
		Mode:             ModeLMTP,
		ImplicitTLS:      true,
		EnableCHUNKING:   true,
		EnableBINARYMIME: true,
		MaxConnections:   -1,
	})
	if err == nil {
		t.Fatal("NewServer accepted invalid options")
	}
	for _, want := range []string{
		"Backend.NewSession",
		"LMTP",
		"MaxSpoolBytes",
		"MaxSpoolMemoryBytes",
		"MaxTotalSpoolBytes",
		"MaxTotalSpoolMemoryBytes",
		"MaxConcurrentSpools",
		"TLSConfig",
		"MaxConnections",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

func TestNewServerClonesTLSConfigAndBuildsInstanceSpool(t *testing.T) {
	listener := &stubListener{addr: testAddr("smtp")}
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	server, err := NewServer(&ServerOptions{
		Listener:                 listener,
		Backend:                  validBackend(),
		TLSConfig:                config,
		EnableCHUNKING:           true,
		MaxSpoolBytes:            1024,
		MaxSpoolMemoryBytes:      64,
		MaxTotalSpoolBytes:       4096,
		MaxTotalSpoolMemoryBytes: 256,
		MaxConcurrentSpools:      4,
		SpoolDir:                 t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if server.tlsConfig == config || server.spools == nil {
		t.Fatalf("server TLS/spool construction = (%p, %p)", server.tlsConfig, server.spools)
	}
	if err := server.Shutdown(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestNewServerRejectsUnknownMode(t *testing.T) {
	_, err := NewServer(&ServerOptions{
		Listener: &stubListener{addr: testAddr("smtp")},
		Backend:  validBackend(),
		Mode:     Mode("future"),
	})
	if err == nil || !strings.Contains(err.Error(), "Mode") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewServerDefaultsAndClonesCommandConfiguration(t *testing.T) {
	after := []string{"plain", "SCRAM-SHA-256"}
	server, err := NewServer(&ServerOptions{
		Listener:               &stubListener{addr: testAddr("smtp")},
		Backend:                validBackend(),
		AuthMechanismsAfterTLS: after,
	})
	if err != nil {
		t.Fatal(err)
	}
	after[0] = "changed"
	if server.identity == "" || server.timeouts.command <= 0 || server.timeouts.data <= 0 {
		t.Fatalf("defaults were not applied: %+v", server)
	}
	if server.maxMessage != defaultMaxMessageBytes || server.maxRcpt != 100 {
		t.Fatalf("limits = (%d, %d)", server.maxMessage, server.maxRcpt)
	}
	if len(server.authBefore) != 0 || len(server.authAfter) != 2 || server.authAfter[0] != "PLAIN" {
		t.Fatalf("AUTH lists = before %#v after %#v", server.authBefore, server.authAfter)
	}
}

func TestNewServerRejectsCommandConfigurationDefects(t *testing.T) {
	_, err := NewServer(&ServerOptions{
		Listener:                &stubListener{addr: testAddr("smtp")},
		Backend:                 validBackend(),
		GreetingIdentity:        "bad identity",
		CommandTimeout:          -1,
		DataTimeout:             -1,
		MaxMessageBytes:         -1,
		MaxRecipients:           99,
		AuthMechanismsBeforeTLS: []string{"PLAIN", "plain", "bad name"},
	})
	if err == nil {
		t.Fatal("NewServer accepted invalid command configuration")
	}
	for _, want := range []string{"GreetingIdentity", "CommandTimeout", "DataTimeout", "MaxMessageBytes", "MaxRecipients", "duplicate mechanism", "invalid mechanism"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

func TestNewServerRejectsUnreachableSubmissionPolicies(t *testing.T) {
	for _, test := range []struct {
		name string
		opts ServerOptions
		want string
	}{
		{name: "required TLS", opts: ServerOptions{RequireTLS: true}, want: "TLSConfig"},
		{name: "required auth", opts: ServerOptions{RequireAuth: true}, want: "AUTH mechanism"},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.opts.Listener = &stubListener{addr: testAddr("smtp")}
			test.opts.Backend = validBackend()
			_, err := NewServer(&test.opts)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %s", err, test.want)
			}
		})
	}
}

func validBackend() *Backend {
	return &Backend{NewSession: func(context.Context, *ConnInfo, *NewSessionOptions) (*Session, error) {
		return &Session{
			Mail:  func(context.Context, string, *smtp.MailOptions, *MailOptions) error { return nil },
			Rcpt:  func(context.Context, string, *smtp.RcptOptions, *RcptOptions) error { return nil },
			Data:  func(context.Context, io.Reader, *DataOptions) (smtp.DataResult, error) { return nil, nil },
			Reset: func(context.Context, ResetReason, *ResetOptions) {},
			Close: func(context.Context, *CloseOptions) {},
		}, nil
	}}
}
