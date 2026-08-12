package smtpserver

import (
	"net"
	"strings"
	"testing"
)

func TestValidateConstructionReportsEveryProblem(t *testing.T) {
	listener := &stubListener{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 25}}
	err := validateConstruction(constructionConfig{
		listener: listener,
		mode:     modeLMTP,
		chunking: true,
	})
	if err == nil {
		t.Fatal("invalid construction succeeded")
	}
	for _, problem := range []string{
		"Backend.NewSession",
		"LMTP",
		"MaxSpoolBytes",
		"MaxSpoolMemoryBytes",
		"MaxTotalSpoolBytes",
		"MaxTotalSpoolMemoryBytes",
		"MaxConcurrentSpools",
	} {
		if !strings.Contains(err.Error(), problem) {
			t.Errorf("error %q does not name %q", err, problem)
		}
	}
}

func TestValidateConstructionRejectsBinaryMIMEWithoutChunking(t *testing.T) {
	listener := &stubListener{addr: testAddr("smtp")}
	err := validateConstruction(constructionConfig{
		listener:          listener,
		backendNewSession: true,
		binaryMIME:        true,
	})
	if err == nil || !strings.Contains(err.Error(), "BINARYMIME requires CHUNKING") {
		t.Fatalf("error = %v", err)
	}
}

func TestValidateConstructionAllowsOversubscribedSpoolProduct(t *testing.T) {
	listener := &stubListener{addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2525}}
	err := validateConstruction(constructionConfig{
		listener:            listener,
		mode:                modeSMTP,
		backendNewSession:   true,
		chunking:            true,
		maxSpoolBytes:       1 << 20,
		maxSpoolMemoryBytes: 1 << 16,
		maxTotalSpoolBytes:  1 << 20,
		maxTotalSpoolMemory: 1 << 16,
		maxConcurrentSpools: 8,
		maxConnections:      100,
	})
	if err != nil {
		t.Fatalf("oversubscribed product rejected: %v", err)
	}
}

func TestValidateConstructionDoesNotRequireSpoolWhenChunkingDisabled(t *testing.T) {
	listener := &stubListener{addr: testAddr("smtp")}
	err := validateConstruction(constructionConfig{listener: listener, backendNewSession: true})
	if err != nil {
		t.Fatal(err)
	}
}

type stubListener struct {
	addr net.Addr
}

func (l *stubListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (l *stubListener) Close() error              { return nil }
func (l *stubListener) Addr() net.Addr            { return l.addr }

type testAddr string

func (a testAddr) Network() string { return string(a) }
func (a testAddr) String() string  { return string(a) }
