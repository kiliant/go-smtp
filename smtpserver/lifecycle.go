package smtpserver

import (
	"context"
	"sync"

	"github.com/kiliant/go-smtp"
)

// sessionLifecycle centralizes the ordering contract around a backend Session.
// Command handlers decide protocol replies; this type ensures transaction
// cleanup cannot drift between those commands.
type sessionLifecycle struct {
	session *Session

	transactionOpen bool
	spool           *spool
	closeOnce       sync.Once
}

func newSessionLifecycle(session *Session) *sessionLifecycle {
	return &sessionLifecycle{session: session}
}

func (l *sessionLifecycle) mail(
	ctx context.Context,
	reversePath string,
	params *smtp.MailOptions,
	opts *MailOptions,
) error {
	if l.transactionOpen {
		l.reset(ctx, ResetNewMail)
	}
	if err := l.session.Mail(ctx, reversePath, params, opts); err != nil {
		return err
	}
	l.transactionOpen = true
	return nil
}

func (l *sessionLifecycle) attachSpool(spool *spool) {
	if l.spool != nil && l.spool != spool {
		_ = l.spool.Close()
	}
	l.spool = spool
}

func (l *sessionLifecycle) reset(ctx context.Context, reason ResetReason) {
	spool := l.spool
	l.spool = nil
	l.transactionOpen = false
	if spool != nil {
		_ = spool.Close()
	}
	l.session.Reset(ctx, reason, nil)
}

// resetIfOpen avoids ResetSessionEnd double accounting after a transaction was
// already completed or failed.
func (l *sessionLifecycle) resetIfOpen(ctx context.Context, reason ResetReason) {
	if !l.transactionOpen && l.spool == nil {
		return
	}
	l.reset(ctx, reason)
}

// close calls ResetSessionEnd only for live transaction state and calls Close
// exactly once after that final reset.
func (l *sessionLifecycle) close(ctx context.Context) {
	l.closeOnce.Do(func() {
		l.resetIfOpen(ctx, ResetSessionEnd)
		l.session.Close(ctx, nil)
	})
}
