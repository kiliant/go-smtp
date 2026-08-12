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
