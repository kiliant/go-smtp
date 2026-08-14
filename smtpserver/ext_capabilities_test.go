package smtpserver

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/kiliant/go-smtp"
)

func TestParameterExtensionAndLimitsAdvertisement(t *testing.T) {
	session := extensionTestSession()
	session.ParameterExtensions = []ParameterExtension{
		{Keyword: smtp.ExtDSN},
		{Keyword: smtp.ExtDeliverBy, Params: "30"},
		{Keyword: smtp.ExtFutureRelease, Params: "3600 2030-01-01T00:00:00Z"},
		{Keyword: smtp.Extension("X-FUTURE"), Params: "opaque params"},
	}
	session.Limits = &smtp.Limits{MailMax: 10, RcptMax: 100, RcptDomainMax: 20, Extra: "FUTURE_FLAG FUTURE_VALUE=word=value"}
	harness := newRawTestServer(t, ModeSMTP, backendForSession(session), nil)
	code, text := harness.command("EHLO client.example")
	if code != 250 {
		t.Fatalf("EHLO code = %d", code)
	}
	for _, line := range []string{
		"DSN",
		"DELIVERBY 30",
		"FUTURERELEASE 3600 2030-01-01T00:00:00Z",
		"X-FUTURE opaque params",
		"LIMITS MAILMAX=10 RCPTMAX=100 RCPTDOMAINMAX=20 FUTURE_FLAG FUTURE_VALUE=word=value",
	} {
		if !strings.Contains(text, line) {
			t.Errorf("EHLO reply %q does not contain %q", text, line)
		}
	}
}

func TestSessionRejectsInvalidExtensionDeclarations(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*Session)
		want   string
	}{
		{
			name: "lower-case keyword",
			mutate: func(session *Session) {
				session.ParameterExtensions = []ParameterExtension{{Keyword: smtp.Extension("dsn")}}
			},
			want: "invalid upper-case Keyword",
		},
		{
			name: "framework duplicate",
			mutate: func(session *Session) {
				session.ParameterExtensions = []ParameterExtension{{Keyword: smtp.ExtChunking}}
			},
			want: "framework-owned capability CHUNKING",
		},
		{
			name: "duplicate",
			mutate: func(session *Session) {
				session.ParameterExtensions = []ParameterExtension{{Keyword: smtp.ExtDSN}, {Keyword: smtp.ExtDSN}}
			},
			want: "duplicates DSN",
		},
		{
			name: "framing",
			mutate: func(session *Session) {
				session.ParameterExtensions = []ParameterExtension{{Keyword: smtp.ExtDSN, Params: "bad\r\n250 X"}}
			},
			want: "invalid EHLO framing",
		},
		{
			name: "future release declaration",
			mutate: func(session *Session) {
				session.ParameterExtensions = []ParameterExtension{{Keyword: smtp.ExtFutureRelease, Params: "bad"}}
			},
			want: "must declare FUTURERELEASE",
		},
		{
			name: "limits range",
			mutate: func(session *Session) {
				session.Limits = &smtp.Limits{MailMax: 1000000}
			},
			want: "six-digit maximum",
		},
		{
			name: "invalid open limit",
			mutate: func(session *Session) {
				session.Limits = &smtp.Limits{Extra: "FUTURE=bad;value"}
			},
			want: "invalid RFC 9422 limit value",
		},
		{
			name: "MTRK without DSN",
			mutate: func(session *Session) {
				session.ParameterExtensions = []ParameterExtension{{Keyword: smtp.ExtMTRK}}
			},
			want: "MTRK requires DSN",
		},
		{
			name: "atrn without auth",
			mutate: func(session *Session) {
				session.ATRN = func(context.Context, []string, *ATRNOptions) (*ATRNResult, error) { return nil, nil }
			},
			want: "ATRN requires an authentication verifier",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := extensionTestSession()
			test.mutate(session)
			err := validateSession(session)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateSession = %v, want %q", err, test.want)
			}
		})
	}
}

func TestUnadvertisedKnownParametersReceive501(t *testing.T) {
	harness := newRawTestServer(t, ModeSMTP, backendForSession(extensionTestSession()), nil)
	harness.wantCommand("EHLO client.example", 250)
	for _, parameter := range []string{
		"RET=FULL",
		"ENVID=id",
		"BY=60;N",
		"HOLDFOR=60",
		"HOLDUNTIL=2029-01-01T00:00:00Z",
		"MT-PRIORITY=1",
		"REQUIRETLS",
		"SOLICIT=bulk",
		"MTRK=YWJjMTIz",
		"SUBMITTER=user@example.test",
		"CONPERM",
	} {
		harness.wantCommand("MAIL FROM:<sender@example.test> "+parameter, 501)
	}
	harness.wantCommand("MAIL FROM:<sender@example.test>", 250)
	for _, parameter := range []string{
		"NOTIFY=FAILURE",
		"ORCPT=rfc822;user@example.test",
		"RRVS=2029-01-01T00:00:00Z",
		"CONNEG",
	} {
		harness.wantCommand("RCPT TO:<recipient@example.test> "+parameter, 501)
	}
}

func TestCONNEGBackendAddsMultilineRcptSuccess(t *testing.T) {
	session := extensionTestSession()
	session.ParameterExtensions = []ParameterExtension{{Keyword: smtp.ExtConNeg}}
	session.Rcpt = func(_ context.Context, _ string, params *smtp.RcptOptions, opts *RcptOptions) error {
		if params == nil || params.Legacy == nil || !params.Legacy.ConNeg {
			t.Fatal("backend did not receive CONNEG request")
		}
		opts.SuccessLines = []string{"CONNEG (color=Binary)", "CONNEG (paper-size=A4)"}
		return nil
	}
	harness := newRawTestServer(t, ModeSMTP, backendForSession(session), nil)
	harness.wantCommand("EHLO client.example", 250)
	harness.wantCommand("MAIL FROM:<sender@example.test>", 250)
	code, reply := harness.command("RCPT TO:<recipient@example.test> CONNEG")
	if code != 250 {
		t.Fatalf("RCPT code = %d, reply = %q", code, reply)
	}
	for _, line := range []string{"Recipient OK", "CONNEG (color=Binary)", "CONNEG (paper-size=A4)"} {
		if !strings.Contains(reply, line) {
			t.Errorf("RCPT reply %q does not contain %q", reply, line)
		}
	}
}

func TestFutureReleaseIsNeitherAdvertisedNorAcceptedInLMTP(t *testing.T) {
	session := extensionTestSession()
	session.ParameterExtensions = []ParameterExtension{{Keyword: smtp.ExtFutureRelease, Params: "3600 2030-01-01T00:00:00Z"}}
	harness := newRawTestServer(t, ModeLMTP, backendForSession(session), nil)
	code, reply := harness.command("LHLO client.example")
	if code != 250 || strings.Contains(reply, string(smtp.ExtFutureRelease)) {
		t.Fatalf("LHLO = (%d, %q), want no FUTURERELEASE", code, reply)
	}
	harness.wantCommand("MAIL FROM:<sender@example.test> HOLDFOR=60", 501)
}

func extensionTestSession() *Session {
	var recipients []string
	return &Session{
		Mail: func(context.Context, string, *smtp.MailOptions, *MailOptions) error {
			recipients = nil
			return nil
		},
		Rcpt: func(_ context.Context, recipient string, _ *smtp.RcptOptions, _ *RcptOptions) error {
			recipients = append(recipients, recipient)
			return nil
		},
		Data: func(_ context.Context, reader io.Reader, _ *DataOptions) (smtp.DataResult, error) {
			_, _ = io.Copy(io.Discard, reader)
			return smtp.DataResult{{Command: "DATA", Code: 250, Text: "OK"}}, nil
		},
		Reset: func(context.Context, ResetReason, *ResetOptions) { recipients = nil },
		Close: func(context.Context, *CloseOptions) {},
	}
}

func backendForSession(session *Session) *Backend {
	return &Backend{NewSession: func(context.Context, *ConnInfo, *NewSessionOptions) (*Session, error) {
		return session, nil
	}}
}
