package smtpclient

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"
)

// fakeStep is one immutable scripted server interaction. Later task cases
// append independent tables using this type; setup is per-server and has no
// package-global mutable state.
type fakeStep struct {
	command string
	// commands is an optional pipelined group. The fake reads every command
	// before it writes any reply. This verifies reply-count correlation rather
	// than merely exercising a lockstep conversation.
	commands             []string
	replies              []string
	startTLS             bool
	implicitTLS          bool
	allowTLSHandshakeErr bool
	hold                 bool
}

// startFakeServer runs script over one end of net.Pipe. Each reply includes
// its own CRLF. A startTLS step writes its reply in cleartext and upgrades the
// server side before the next expected command.
func startFakeServer(t *testing.T, script []fakeStep, tlsConfig *tls.Config) (net.Conn, func()) {
	t.Helper()
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer server.Close()
		var reader = bufio.NewReader(server)
		var writer net.Conn = server
		if len(script) > 0 && script[0].implicitTLS {
			if tlsConfig == nil {
				t.Error("fake implicit-TLS script needs TLS config")
				return
			}
			tlsConn := tls.Server(server, tlsConfig)
			if err := tlsConn.Handshake(); err != nil {
				t.Errorf("fake server implicit TLS handshake: %v", err)
				return
			}
			writer = tlsConn
			reader = bufio.NewReader(tlsConn)
		}
		if _, err := writer.Write([]byte("220 fake.test ready\r\n")); err != nil {
			t.Errorf("fake server writing greeting: %v", err)
			return
		}
		for _, step := range script {
			expected := step.commands
			if len(expected) == 0 {
				expected = []string{step.command}
			}
			for _, command := range expected {
				line, err := reader.ReadString('\n')
				if err != nil {
					t.Errorf("fake server reading %q: %v", command, err)
					return
				}
				if line != command+"\r\n" {
					t.Errorf("fake server command = %q, want %q", line, command+"\r\n")
					return
				}
			}
			for _, reply := range step.replies {
				if _, err := writer.Write([]byte(reply)); err != nil {
					t.Errorf("fake server writing reply: %v", err)
					return
				}
			}
			if step.startTLS {
				if tlsConfig == nil {
					t.Error("fake STARTTLS step needs TLS config")
					return
				}
				tlsConn := tls.Server(server, tlsConfig)
				if err := tlsConn.Handshake(); err != nil && !step.allowTLSHandshakeErr {
					t.Errorf("fake server TLS handshake: %v", err)
					return
				} else if err != nil {
					return
				}
				writer = tlsConn
				reader = bufio.NewReader(tlsConn)
			}
			if step.hold {
				// Wait for the client to close its end. This models a peer that
				// has accepted a command but never sends its reply.
				_, _ = reader.ReadByte()
				return
			}
		}
	}()
	return client, func() {
		t.Helper()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("fake server did not finish")
		}
	}
}

func fakeTLSConfig(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "server.test"},
		DNSNames:     []string{"server.test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	pemCert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemCert) {
		t.Fatal("adding fake certificate to roots")
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}}, &tls.Config{RootCAs: pool, ServerName: "server.test"}
}

func fakeReplies(lines ...string) []string { return lines }
