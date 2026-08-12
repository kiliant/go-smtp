package backendtest

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/smtpserver"
	"github.com/kiliant/go-smtp/smtpserver/memory"
)

func TestCheckPassesMemoryInBothModes(t *testing.T) {
	for _, mode := range []smtpserver.Mode{smtpserver.ModeSMTP, smtpserver.ModeLMTP} {
		sink := memory.New(nil)
		if problems := check(context.Background(), sink.Backend(), normalizedOptions(&Options{Mode: mode})); len(problems) != 0 {
			t.Fatalf("mode %s problems: %v", mode, problems)
		}
	}
}

func TestCheckFailsAgainstEachBrokenContractItChecks(t *testing.T) {
	tests := []struct {
		name string
		make func() *smtpserver.Backend
		want string
	}{
		{name: "missing NewSession", make: func() *smtpserver.Backend { return &smtpserver.Backend{} }, want: "NewSession"},
		{name: "nil Session", make: func() *smtpserver.Backend {
			return &smtpserver.Backend{NewSession: func(context.Context, *smtpserver.ConnInfo, *smtpserver.NewSessionOptions) (*smtpserver.Session, error) {
				return nil, nil
			}}
		}, want: "nil Session"},
		{name: "missing required handler", make: func() *smtpserver.Backend {
			backend := conformingBackend()
			base := backend.NewSession
			backend.NewSession = func(ctx context.Context, conn *smtpserver.ConnInfo, opts *smtpserver.NewSessionOptions) (*smtpserver.Session, error) {
				session, err := base(ctx, conn, opts)
				session.Rcpt = nil
				return session, err
			}
			return backend
		}, want: "Session.Rcpt"},
		{name: "auth without commit", make: func() *smtpserver.Backend {
			backend := conformingBackend()
			base := backend.NewSession
			backend.NewSession = func(ctx context.Context, conn *smtpserver.ConnInfo, opts *smtpserver.NewSessionOptions) (*smtpserver.Session, error) {
				session, err := base(ctx, conn, opts)
				session.Authenticate = func(context.Context, *smtpserver.Credentials, *smtpserver.AuthenticateOptions) (*smtpserver.AuthResult, error) {
					return nil, nil
				}
				return session, err
			}
			return backend
		}, want: "CommitAuth"},
		{name: "reader not consumed", make: func() *smtpserver.Backend {
			return dataBackend(func(context.Context, io.Reader, *smtpserver.DataOptions) (smtp.DataResult, error) {
				return smtp.DataResult{{Code: 550}}, nil
			})
		}, want: "complete reader"},
		{name: "result and error", make: func() *smtpserver.Backend {
			return dataBackend(func(_ context.Context, reader io.Reader, _ *smtpserver.DataOptions) (smtp.DataResult, error) {
				_, _ = io.Copy(io.Discard, reader)
				return smtp.DataResult{{Code: 250}}, errors.New("ambiguous")
			})
		}, want: "both a result and an error"},
		{name: "wrong cardinality", make: func() *smtpserver.Backend {
			return dataBackend(func(_ context.Context, reader io.Reader, _ *smtpserver.DataOptions) (smtp.DataResult, error) {
				_, _ = io.Copy(io.Discard, reader)
				return nil, nil
			})
		}, want: "returned 0 results"},
		{name: "class disagreement", make: func() *smtpserver.Backend {
			return dataBackend(func(_ context.Context, reader io.Reader, _ *smtpserver.DataOptions) (smtp.DataResult, error) {
				_, _ = io.Copy(io.Discard, reader)
				return smtp.DataResult{{Code: 550, Enhanced: smtp.EnhancedCode{Class: 4, Subject: 7, Detail: 1}}}, nil
			})
		}, want: "classes disagree"},
		{name: "reset panic", make: func() *smtpserver.Backend {
			backend := conformingBackend()
			base := backend.NewSession
			backend.NewSession = func(ctx context.Context, conn *smtpserver.ConnInfo, opts *smtpserver.NewSessionOptions) (*smtpserver.Session, error) {
				session, err := base(ctx, conn, opts)
				session.Reset = func(context.Context, smtpserver.ResetReason, *smtpserver.ResetOptions) { panic("broken reset") }
				return session, err
			}
			return backend
		}, want: "panicked"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			problems := check(context.Background(), test.make(), normalizedOptions(nil))
			if !containsProblem(problems, test.want) {
				t.Fatalf("problems = %v, want containing %q", problems, test.want)
			}
		})
	}
}

func conformingBackend() *smtpserver.Backend {
	return dataBackend(func(_ context.Context, reader io.Reader, _ *smtpserver.DataOptions) (smtp.DataResult, error) {
		_, _ = io.Copy(io.Discard, reader)
		return smtp.DataResult{{Code: 250, Enhanced: smtp.EnhancedCode{Class: 2}}}, nil
	})
}

func dataBackend(data func(context.Context, io.Reader, *smtpserver.DataOptions) (smtp.DataResult, error)) *smtpserver.Backend {
	return &smtpserver.Backend{NewSession: func(context.Context, *smtpserver.ConnInfo, *smtpserver.NewSessionOptions) (*smtpserver.Session, error) {
		return &smtpserver.Session{
			Mail:  func(context.Context, string, *smtp.MailOptions, *smtpserver.MailOptions) error { return nil },
			Rcpt:  func(context.Context, string, *smtp.RcptOptions, *smtpserver.RcptOptions) error { return nil },
			Data:  data,
			Reset: func(context.Context, smtpserver.ResetReason, *smtpserver.ResetOptions) {},
			Close: func(context.Context, *smtpserver.CloseOptions) {},
		}, nil
	}}
}

func containsProblem(problems []string, want string) bool {
	for _, problem := range problems {
		if strings.Contains(problem, want) {
			return true
		}
	}
	return false
}
