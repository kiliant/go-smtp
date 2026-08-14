package smtpserver

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/smtpclient"
)

func TestATRNHandsConnectionToInjectedSMTPClient(t *testing.T) {
	session := extensionTestSession()
	session.Authenticate = func(_ context.Context, credentials *Credentials, _ *AuthenticateOptions) (*AuthResult, error) {
		if credentials.AuthenticationID != "user" || credentials.Password != "pass" {
			return &AuthResult{Failure: &AuthFailure{}}, nil
		}
		return &AuthResult{Identity: "user"}, nil
	}
	session.CommitAuth = func(context.Context, *AuthResult, *CommitAuthOptions) {}
	domainsSeen := make(chan []string, 1)
	takeoverDone := make(chan error, 1)
	session.ATRN = func(_ context.Context, domains []string, _ *ATRNOptions) (*ATRNResult, error) {
		domainsSeen <- append([]string(nil), domains...)
		return &ATRNResult{Takeover: func(ctx context.Context, conn net.Conn, _ *ATRNTakeoverOptions) error {
			client, err := smtpclient.NewClient(ctx, conn, &smtpclient.ClientOptions{Identity: "provider.example"})
			if err == nil {
				err = client.Close()
			}
			takeoverDone <- err
			return err
		}}, nil
	}

	harness := newRawTestServer(t, ModeSMTP, backendForSession(session), func(opts *ServerOptions) {
		opts.AuthMechanismsBeforeTLS = []string{"PLAIN"}
	})
	code, text := harness.command("EHLO customer.example")
	if code != 250 || !strings.Contains(text, "AUTH PLAIN") || !strings.Contains(text, "ATRN") {
		t.Fatalf("EHLO = (%d, %q), want AUTH PLAIN and ATRN", code, text)
	}
	harness.wantCommand("ATRN example.org", 530)
	harness.wantCommand("AUTH PLAIN AHVzZXIAcGFzcw==", 235)
	code, text = harness.command("ATRN example.org,example.com")
	if code != 250 || !strings.Contains(text, "reversing") {
		t.Fatalf("ATRN = (%d, %q)", code, text)
	}
	if got := <-domainsSeen; !reflect.DeepEqual(got, []string{"example.org", "example.com"}) {
		t.Fatalf("ATRN domains = %#v", got)
	}

	writeTestCommand(t, harness.conn, "220 customer.example ready\r\n")
	line, err := harness.reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if line != "EHLO provider.example\r\n" {
		t.Fatalf("reversed command = %q, want EHLO", line)
	}
	writeTestCommand(t, harness.conn, "250 customer.example\r\n")
	line, err = harness.reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if line != "QUIT\r\n" {
		t.Fatalf("reversed command = %q, want QUIT", line)
	}
	writeTestCommand(t, harness.conn, "221 closing\r\n")
	select {
	case err := <-takeoverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("ATRN takeover did not finish")
	}
}

func TestATRNBackendResponseCodesArePreserved(t *testing.T) {
	session := extensionTestSession()
	session.Authenticate = func(context.Context, *Credentials, *AuthenticateOptions) (*AuthResult, error) {
		return &AuthResult{Identity: "user"}, nil
	}
	session.CommitAuth = func(context.Context, *AuthResult, *CommitAuthOptions) {}
	session.ATRN = func(context.Context, []string, *ATRNOptions) (*ATRNResult, error) {
		return nil, &smtp.Error{Code: 453, Enhanced: smtp.EnhancedCode{Class: 4, Subject: 3, Detail: 0}, Text: "You have no mail"}
	}
	harness := newRawTestServer(t, ModeSMTP, backendForSession(session), func(opts *ServerOptions) {
		opts.AuthMechanismsBeforeTLS = []string{"PLAIN"}
	})
	harness.wantCommand("EHLO customer.example", 250)
	harness.wantCommand("AUTH PLAIN AHVzZXIAcGFzcw==", 235)
	harness.wantCommand("ATRN", 453)
}

func TestParseATRNDomains(t *testing.T) {
	for _, test := range []struct {
		argument string
		want     []string
		valid    bool
	}{
		{argument: "", valid: true},
		{argument: "example.org", want: []string{"example.org"}, valid: true},
		{argument: "a.example,b.example", want: []string{"a.example", "b.example"}, valid: true},
		{argument: "localhost"},
		{argument: "example.org, example.com"},
		{argument: "example.org,"},
		{argument: "-bad.example"},
	} {
		got, err := parseATRNDomains(test.argument)
		if test.valid {
			if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Errorf("parseATRNDomains(%q) = (%#v, %v), want %#v", test.argument, got, err, test.want)
			}
		} else if err == nil {
			t.Errorf("parseATRNDomains(%q) succeeded", test.argument)
		}
	}
}

func TestATRNNilTakeoverIsBackendContractFailure(t *testing.T) {
	session := extensionTestSession()
	session.Authenticate = func(context.Context, *Credentials, *AuthenticateOptions) (*AuthResult, error) {
		return &AuthResult{Identity: "user"}, nil
	}
	session.CommitAuth = func(context.Context, *AuthResult, *CommitAuthOptions) {}
	session.ATRN = func(context.Context, []string, *ATRNOptions) (*ATRNResult, error) {
		return &ATRNResult{}, nil
	}
	var logged error
	harness := newRawTestServer(t, ModeSMTP, backendForSession(session), func(opts *ServerOptions) {
		opts.AuthMechanismsBeforeTLS = []string{"PLAIN"}
		opts.ErrorLog = func(event ErrorEvent) { logged = errors.Join(logged, event.Err) }
	})
	harness.wantCommand("EHLO customer.example", 250)
	harness.wantCommand("AUTH PLAIN AHVzZXIAcGFzcw==", 235)
	harness.wantCommand("ATRN example.org", 451)
	if logged == nil || !strings.Contains(logged.Error(), "no takeover callback") {
		t.Fatalf("logged error = %v", logged)
	}
}
