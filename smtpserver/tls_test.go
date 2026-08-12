package smtpserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/kiliant/go-smtp/internal/smtpwire"
)

func TestSTARTTLSDiscardsOnlyPrefetchedPlaintext(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()

	serverConfig, clientConfig := testTLSConfigs(t)
	reader := smtpwire.NewLineReader(serverConn)
	clientDone := make(chan error, 1)
	go func() {
		if _, err := clientConn.Write([]byte("STARTTLS\r\nNOOP\r\n")); err != nil {
			clientDone <- err
			return
		}
		tlsClient := tls.Client(clientConn, clientConfig)
		if err := tlsClient.HandshakeContext(context.Background()); err != nil {
			clientDone <- err
			return
		}
		_, err := tlsClient.Write([]byte("EHLO encrypted.example\r\n"))
		clientDone <- err
	}()

	command, err := reader.ReadCommand(time.Now().Add(time.Second), smtpwire.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if command.Verb != "STARTTLS" {
		t.Fatalf("command = %+v", command)
	}
	var reported int
	tlsConn, err := handshakeTLS(context.Background(), serverConn, serverConfig, reader, func(n int) {
		reported = n
	})
	if err != nil {
		t.Fatal(err)
	}
	if reported != len("NOOP\r\n") {
		t.Fatalf("reported prefetch = %d, want %d", reported, len("NOOP\r\n"))
	}
	encryptedReader := smtpwire.NewLineReader(tlsConn)
	command, err = encryptedReader.ReadCommand(time.Now().Add(time.Second), smtpwire.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if command.Verb != "EHLO" || command.Argument != "encrypted.example" {
		t.Fatalf("encrypted command = %+v", command)
	}
	if err := <-clientDone; err != nil {
		t.Fatal(err)
	}
}

func TestHandshakeTLSRequiresConfiguration(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	if _, err := handshakeTLS(context.Background(), serverConn, nil, nil, nil); !errors.Is(err, errTLSConfigRequired) {
		t.Fatalf("error = %v, want errTLSConfigRequired", err)
	}
}

func testTLSConfigs(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "server.example"},
		DNSNames:     []string{"server.example"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	pool := x509.NewCertPool()
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool.AddCert(parsed)
	return &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}, &tls.Config{
		RootCAs:    pool,
		ServerName: "server.example",
		MinVersion: tls.VersionTLS12,
	}
}
