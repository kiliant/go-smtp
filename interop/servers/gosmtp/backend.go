package gosmtp

import (
	"context"
	"sync"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/smtpserver"
)

var parameterExtensions = []smtpserver.ParameterExtension{
	{Keyword: smtp.ExtDSN},
	{Keyword: smtp.ExtDeliverBy, Params: "30"},
	{Keyword: smtp.ExtFutureRelease, Params: "3600 2038-01-19T03:14:07Z"},
	{Keyword: smtp.ExtMTPriority},
	{Keyword: smtp.ExtRRVS},
	{Keyword: smtp.ExtRequireTLS},
	{Keyword: smtp.ExtNoSoliciting},
	{Keyword: smtp.ExtMTRK},
	{Keyword: smtp.ExtSubmitter},
	{Keyword: smtp.ExtConPerm},
	{Keyword: smtp.ExtConNeg},
}

var profileLimits = smtp.Limits{MailMax: 100, RcptMax: 100}

// parameterObserver is test visibility into the backend boundary, not a
// second sink. The memory backend remains authoritative for delivered bytes;
// this records the typed vocabulary passed to its Mail and Rcpt callbacks.
type parameterObserver struct {
	mu   sync.Mutex
	mail *smtp.MailOptions
	rcpt *smtp.RcptOptions
}

func (o *parameterObserver) recordMail(opts *smtp.MailOptions) {
	o.mu.Lock()
	o.mail = opts
	o.mu.Unlock()
}

func (o *parameterObserver) recordRcpt(opts *smtp.RcptOptions) {
	o.mu.Lock()
	o.rcpt = opts
	o.mu.Unlock()
}

func (o *parameterObserver) latest() (*smtp.MailOptions, *smtp.RcptOptions) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.mail, o.rcpt
}

func profileBackend(base *smtpserver.Backend, observer *parameterObserver) *smtpserver.Backend {
	return &smtpserver.Backend{
		NewSession: func(ctx context.Context, conn *smtpserver.ConnInfo, opts *smtpserver.NewSessionOptions) (*smtpserver.Session, error) {
			session, err := base.NewSession(ctx, conn, opts)
			if err != nil {
				return nil, err
			}
			session.ParameterExtensions = append([]smtpserver.ParameterExtension(nil), parameterExtensions...)
			limits := profileLimits
			session.Limits = &limits

			mail := session.Mail
			session.Mail = func(ctx context.Context, reversePath string, params *smtp.MailOptions, opts *smtpserver.MailOptions) error {
				observer.recordMail(params)
				return mail(ctx, reversePath, params, opts)
			}
			rcpt := session.Rcpt
			session.Rcpt = func(ctx context.Context, forwardPath string, params *smtp.RcptOptions, opts *smtpserver.RcptOptions) error {
				observer.recordRcpt(params)
				return rcpt(ctx, forwardPath, params, opts)
			}
			return session, nil
		},
	}
}
