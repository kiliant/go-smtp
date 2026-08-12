package smtpserver

import (
	"context"
	"io"
	"testing"

	"github.com/kiliant/go-smtp"
)

func TestBackendAndSessionCorrectedSignaturesCompile(t *testing.T) {
	backend := Backend{
		NewSession: func(context.Context, *ConnInfo, *NewSessionOptions) (*Session, error) {
			return &Session{
				Mail: func(context.Context, string, *smtp.MailOptions, *MailOptions) error { return nil },
				Rcpt: func(context.Context, string, *smtp.RcptOptions, *RcptOptions) error { return nil },
				Data: func(context.Context, io.Reader, *DataOptions) (smtp.DataResult, error) {
					return smtp.DataResult{{Code: 250}}, nil
				},
				Reset: func(context.Context, ResetReason, *ResetOptions) {},
				Close: func(context.Context, *CloseOptions) {},
				SCRAMCredentials: func(context.Context, *Credentials, *SCRAMOptions) (*SCRAMKeys, error) {
					return nil, nil
				},
			}, nil
		},
	}
	if backend.NewSession == nil {
		t.Fatal("NewSession is nil")
	}
}
