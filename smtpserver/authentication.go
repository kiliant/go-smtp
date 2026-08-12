package smtpserver

import (
	"context"
	"errors"
)

var errBackendAuthContract = errors.New("smtpserver: backend authentication contract violation")

// finalizeAuthentication is the sole backend authentication commit point. The
// AUTH command calls it only after mechanism proof and every required round
// trip have completed. A verifier result alone never mutates backend state.
func finalizeAuthentication(
	ctx context.Context,
	session *Session,
	result *AuthResult,
	verificationErr error,
	exchangeComplete bool,
) (*AuthResult, error) {
	if verificationErr != nil {
		return nil, verificationErr
	}
	if result == nil {
		return nil, errBackendAuthContract
	}
	if result.Failure != nil || !exchangeComplete {
		return result, nil
	}
	if session == nil || session.CommitAuth == nil {
		return nil, errBackendAuthContract
	}
	session.CommitAuth(ctx, result, nil)
	return result, nil
}
