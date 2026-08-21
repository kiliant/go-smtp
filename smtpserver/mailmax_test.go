package smtpserver

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kiliant/go-smtp"
)

func TestMaxTransactionsConstruction(t *testing.T) {
	server, err := NewServer(&ServerOptions{
		Listener: &stubListener{addr: testAddr("smtp")},
		Backend:  validBackend(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if server.maxTransactions != defaultMaxTransactions {
		t.Fatalf("default MaxTransactions = %d, want %d", server.maxTransactions, defaultMaxTransactions)
	}

	for _, value := range []int{-1, 1000000} {
		_, err := NewServer(&ServerOptions{
			Listener:        &stubListener{addr: testAddr("smtp")},
			Backend:         validBackend(),
			MaxTransactions: value,
		})
		if err == nil || !strings.Contains(err.Error(), "MaxTransactions") {
			t.Errorf("MaxTransactions %d: error = %v", value, err)
		}
	}

	server, err = NewServer(&ServerOptions{
		Listener:        &stubListener{addr: testAddr("smtp")},
		Backend:         validBackend(),
		MaxTransactions: 999999,
	})
	if err != nil {
		t.Fatal(err)
	}
	if server.maxTransactions != 999999 {
		t.Fatalf("MaxTransactions = %d, want 999999", server.maxTransactions)
	}
}

func TestMaxTransactionsAllowsLimitThenCloses(t *testing.T) {
	backend := &mailmaxTestBackend{}
	harness := newRawTestServer(t, ModeSMTP, backend.backend(), func(opts *ServerOptions) {
		opts.MaxTransactions = 2
	})
	harness.wantCommand("EHLO client.example", 250)
	harness.wantCommand("MAIL FROM:<first@example.test>", 250)
	harness.wantCommand("RSET", 250)
	harness.wantCommand("mail FROM:<second@example.test>", 250)
	harness.wantCommand("RSET", 250)
	if code, text := harness.command("MAIL FROM:<overflow@example.test>"); code != 421 || text != "4.7.0 Transaction limit reached" {
		t.Fatalf("over-limit MAIL = (%d, %q), want (421, %q)", code, text, "4.7.0 Transaction limit reached")
	}
	if calls := backend.mailCallCount(); calls != 2 {
		t.Fatalf("Session.Mail calls = %d, want 2", calls)
	}
	if err := harness.readUntilClose(); err == nil {
		t.Fatal("connection remained open after 421")
	}
}

func TestEveryMailAttemptConsumesTransactionLimit(t *testing.T) {
	tests := []struct {
		name          string
		first         string
		wantFirst     int
		mailError     error
		helloFirst    bool
		requireTLS    bool
		wantMailCalls int
	}{
		{name: "wrong state", first: "MAIL FROM:<sender@example.test>", wantFirst: 503},
		{name: "syntax", first: "MAIL not-a-reverse-path", wantFirst: 501, helloFirst: true},
		{
			name:       "listener policy",
			first:      "MAIL FROM:<sender@example.test>",
			wantFirst:  530,
			helloFirst: true,
			requireTLS: true,
		},
		{
			name:          "backend rejection",
			first:         "MAIL FROM:<sender@example.test>",
			wantFirst:     550,
			mailError:     &smtp.Error{Code: 550, Enhanced: smtp.EnhancedCode{Class: 5, Subject: 7, Detail: 1}, Text: "Rejected"},
			helloFirst:    true,
			wantMailCalls: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := &mailmaxTestBackend{mailError: test.mailError}
			harness := newRawTestServer(t, ModeSMTP, backend.backend(), func(opts *ServerOptions) {
				opts.MaxTransactions = 1
				if test.requireTLS {
					serverTLS, _ := testTLSConfigs(t)
					opts.TLSConfig = serverTLS
					opts.RequireTLS = true
				}
			})
			if test.helloFirst {
				harness.wantCommand("EHLO client.example", 250)
			}
			harness.wantCommand(test.first, test.wantFirst)
			if !test.helloFirst {
				harness.wantCommand("EHLO client.example", 250)
			}
			harness.wantCommand("MAIL FROM:<overflow@example.test>", 421)
			if calls := backend.mailCallCount(); calls != test.wantMailCalls {
				t.Fatalf("Session.Mail calls = %d, want %d", calls, test.wantMailCalls)
			}
		})
	}
}

func TestMaxTransactionsDoesNotResetOnRSETOrEHLO(t *testing.T) {
	backend := &mailmaxTestBackend{}
	harness := newRawTestServer(t, ModeSMTP, backend.backend(), func(opts *ServerOptions) {
		opts.MaxTransactions = 2
	})
	harness.wantCommand("EHLO first.example", 250)
	harness.wantCommand("MAIL FROM:<first@example.test>", 250)
	harness.wantCommand("RSET", 250)
	harness.wantCommand("MAIL FROM:<second@example.test>", 250)
	harness.wantCommand("EHLO second.example", 250)
	harness.wantCommand("MAIL FROM:<overflow@example.test>", 421)
	if calls := backend.mailCallCount(); calls != 2 {
		t.Fatalf("Session.Mail calls = %d, want 2", calls)
	}
}

func TestMaxTransactionsDoesNotResetOnSTARTTLS(t *testing.T) {
	backend := &mailmaxTestBackend{}
	serverTLS, clientTLS := testTLSConfigs(t)
	harness := newRawTestServer(t, ModeSMTP, backend.backend(), func(opts *ServerOptions) {
		opts.MaxTransactions = 1
		opts.TLSConfig = serverTLS
	})
	harness.wantCommand("EHLO cleartext.example", 250)
	harness.wantCommand("MAIL invalid", 501)
	harness.wantCommand("STARTTLS", 220)

	secure := tls.Client(harness.conn, clientTLS)
	if err := secure.Handshake(); err != nil {
		t.Fatal(err)
	}
	secureReader := bufio.NewReader(secure)
	writeTestCommand(t, secure, "EHLO encrypted.example\r\n")
	if code, _ := readTestReplyDetails(t, secureReader); code != 250 {
		t.Fatalf("post-TLS EHLO = %d, want 250", code)
	}
	writeTestCommand(t, secure, "MAIL FROM:<overflow@example.test>\r\n")
	if code, text := readTestReplyDetails(t, secureReader); code != 421 || !strings.Contains(text, "Transaction limit reached") {
		t.Fatalf("over-limit MAIL = (%d, %q), want 421 transaction limit", code, text)
	}
	if calls := backend.mailCallCount(); calls != 0 {
		t.Fatalf("Session.Mail calls = %d, want 0", calls)
	}
}

func TestOverLimitOpenTransactionResetsAtSessionEnd(t *testing.T) {
	backend := &mailmaxTestBackend{}
	harness := newRawTestServer(t, ModeSMTP, backend.backend(), func(opts *ServerOptions) {
		opts.MaxTransactions = 1
	})
	harness.wantCommand("EHLO client.example", 250)
	harness.wantCommand("MAIL FROM:<sender@example.test>", 250)
	harness.wantCommand("MAIL FROM:<overflow@example.test>", 421)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if reasons := backend.resetReasonSnapshot(); len(reasons) == 1 && reasons[0] == ResetSessionEnd {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("Reset reasons = %#v, want ResetSessionEnd", backend.resetReasonSnapshot())
}

func TestNilLimitsOmitsAdvertisementButEnforcesTransactionBound(t *testing.T) {
	backend := &mailmaxTestBackend{}
	harness := newRawTestServer(t, ModeSMTP, backend.backend(), func(opts *ServerOptions) {
		opts.MaxTransactions = 1
	})
	code, text := harness.command("EHLO client.example")
	if code != 250 || strings.Contains(text, "LIMITS") {
		t.Fatalf("EHLO = (%d, %q), want no LIMITS", code, text)
	}
	harness.wantCommand("MAIL FROM:<sender@example.test>", 250)
	harness.wantCommand("RSET", 250)
	harness.wantCommand("MAIL FROM:<overflow@example.test>", 421)
}

func TestLimitsMailMaxCompositionAndCopy(t *testing.T) {
	tests := []struct {
		name       string
		backendMax uint32
		wantMax    int
	}{
		{name: "lower backend wins", backendMax: 2, wantMax: 2},
		{name: "higher backend clamps", backendMax: 5, wantMax: 3},
		{name: "zero inherits", backendMax: 0, wantMax: 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			declared := &smtp.Limits{MailMax: test.backendMax, RcptMax: 77, RcptDomainMax: 9, Extra: "FUTURE=4"}
			backend := &mailmaxTestBackend{limits: declared}
			harness := newRawTestServer(t, ModeSMTP, backend.backend(), func(opts *ServerOptions) {
				opts.MaxTransactions = 3
			})
			code, text := harness.command("EHLO client.example")
			want := "LIMITS MAILMAX=" + strconv.Itoa(test.wantMax) + " RCPTMAX=77 RCPTDOMAINMAX=9 FUTURE=4"
			if code != 250 || !strings.Contains(text, want) {
				t.Fatalf("EHLO = (%d, %q), want %q", code, text, want)
			}
			if declared.MailMax != test.backendMax || declared.RcptMax != 77 || declared.RcptDomainMax != 9 || declared.Extra != "FUTURE=4" {
				t.Fatalf("backend Limits mutated: %+v", declared)
			}
			for i := 0; i < test.wantMax; i++ {
				harness.wantCommand("MAIL FROM:<sender@example.test>", 250)
				harness.wantCommand("RSET", 250)
			}
			harness.wantCommand("MAIL FROM:<overflow@example.test>", 421)
			if calls := backend.mailCallCount(); calls != test.wantMax {
				t.Fatalf("Session.Mail calls = %d, want %d", calls, test.wantMax)
			}
		})
	}
}

type mailmaxTestBackend struct {
	mu           sync.Mutex
	mailCalls    int
	mailError    error
	limits       *smtp.Limits
	resetReasons []ResetReason
}

func (b *mailmaxTestBackend) backend() *Backend {
	return &Backend{NewSession: func(context.Context, *ConnInfo, *NewSessionOptions) (*Session, error) {
		return &Session{
			Mail: func(context.Context, string, *smtp.MailOptions, *MailOptions) error {
				b.mu.Lock()
				defer b.mu.Unlock()
				b.mailCalls++
				return b.mailError
			},
			Rcpt: func(context.Context, string, *smtp.RcptOptions, *RcptOptions) error { return nil },
			Data: func(_ context.Context, r io.Reader, _ *DataOptions) (smtp.DataResult, error) {
				_, err := io.Copy(io.Discard, r)
				return smtp.DataResult{{Command: "DATA", Code: 250, Text: "OK"}}, err
			},
			Reset: func(_ context.Context, reason ResetReason, _ *ResetOptions) {
				b.mu.Lock()
				b.resetReasons = append(b.resetReasons, reason)
				b.mu.Unlock()
			},
			Close:  func(context.Context, *CloseOptions) {},
			Limits: b.limits,
		}, nil
	}}
}

func (b *mailmaxTestBackend) mailCallCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.mailCalls
}

func (b *mailmaxTestBackend) resetReasonSnapshot() []ResetReason {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]ResetReason(nil), b.resetReasons...)
}
