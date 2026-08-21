// Package backendtest provides a reusable RFC 5321 SMTP and RFC 2033 LMTP
// conformance check for smtpserver backends. It drives only the backend
// contract; production command handling remains package smtpserver's
// responsibility.
package backendtest

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/kiliant/go-smtp/smtpserver"
)

// Options supplies one RFC 5321 SMTP or RFC 2033 LMTP transaction the backend
// is expected to accept. Nil selects deterministic defaults. Recipients retain
// order and duplicates so LMTP cardinality is checked exactly.
//
// Callers constructing an Options literal must use keyed fields.
type Options struct {
	// Mode selects SMTP or LMTP. Empty means SMTP.
	Mode smtpserver.Mode
	// ReversePath is the parsed MAIL FROM path used by the check.
	ReversePath string
	// Recipients are parsed RCPT TO paths the backend is expected to accept.
	Recipients []string
	// Message is the transparent content passed to Session.Data.
	Message []byte

	_ struct{}
}

// Run checks RFC 5321 SMTP and RFC 2033 LMTP backend construction, required
// handlers, authentication field pairing, one complete transaction, result
// cardinality and order, every ResetReason, and idempotent Close. It reports
// each independent defect through t instead of stopping at the first.
func Run(ctx context.Context, t *testing.T, backend *smtpserver.Backend, opts *Options) {
	t.Helper()
	for _, problem := range check(ctx, backend, normalizedOptions(opts)) {
		t.Error(problem)
	}
}

type checkOptions struct {
	mode        smtpserver.Mode
	reversePath string
	recipients  []string
	message     []byte
}

func normalizedOptions(opts *Options) checkOptions {
	value := checkOptions{
		mode:        smtpserver.ModeSMTP,
		reversePath: "sender@example.test",
		recipients:  []string{"same@example.test", "same@example.test"},
		message:     []byte("Subject: backendtest\r\n\r\nbody\r\n"),
	}
	if opts == nil {
		return value
	}
	if opts.Mode != "" {
		value.mode = opts.Mode
	}
	if opts.ReversePath != "" {
		value.reversePath = opts.ReversePath
	}
	if opts.Recipients != nil {
		value.recipients = append([]string(nil), opts.Recipients...)
	}
	if opts.Message != nil {
		value.message = append([]byte(nil), opts.Message...)
	}
	return value
}

func check(ctx context.Context, backend *smtpserver.Backend, opts checkOptions) []string {
	var problems []string
	if ctx == nil {
		return []string{"backendtest: ctx is nil"}
	}
	if backend == nil || backend.NewSession == nil {
		return []string{"backendtest: Backend.NewSession is required"}
	}
	if opts.mode != smtpserver.ModeSMTP && opts.mode != smtpserver.ModeLMTP {
		return []string{fmt.Sprintf("backendtest: unsupported Mode %q", opts.mode)}
	}
	if len(opts.recipients) == 0 {
		return []string{"backendtest: at least one accepted recipient fixture is required"}
	}

	session, err := backend.NewSession(ctx, testConnInfo(opts.mode), nil)
	if err != nil {
		return []string{fmt.Sprintf("backendtest: NewSession: %v", err)}
	}
	problems = append(problems, inspectSession(session)...)
	if len(problems) != 0 {
		return problems
	}
	problems = append(problems, checkTransaction(ctx, session, opts)...)
	problems = append(problems, checkCloseIdempotent(ctx, session)...)
	problems = append(problems, checkResetReasons(ctx, backend, opts)...)
	return problems
}

func inspectSession(session *smtpserver.Session) []string {
	if session == nil {
		return []string{"backendtest: NewSession returned a nil Session"}
	}
	var problems []string
	if session.Mail == nil {
		problems = append(problems, "backendtest: Session.Mail is required")
	}
	if session.Rcpt == nil {
		problems = append(problems, "backendtest: Session.Rcpt is required")
	}
	if session.Data == nil {
		problems = append(problems, "backendtest: Session.Data is required")
	}
	if session.Reset == nil {
		problems = append(problems, "backendtest: Session.Reset is required")
	}
	if session.Close == nil {
		problems = append(problems, "backendtest: Session.Close is required")
	}
	if (session.Authenticate != nil || session.ChallengeResponse != nil || session.SCRAMCredentials != nil) && session.CommitAuth == nil {
		problems = append(problems, "backendtest: Session.CommitAuth is required with authentication verification")
	}
	return problems
}

func checkTransaction(ctx context.Context, session *smtpserver.Session, opts checkOptions) []string {
	if err := session.Mail(ctx, opts.reversePath, nil, nil); err != nil {
		return []string{fmt.Sprintf("backendtest: Mail fixture was not accepted: %v", err)}
	}
	for i, recipient := range opts.recipients {
		if err := session.Rcpt(ctx, recipient, nil, nil); err != nil {
			return []string{fmt.Sprintf("backendtest: Rcpt fixture %d was not accepted: %v", i, err)}
		}
	}
	reader := &trackingReader{Reader: strings.NewReader(string(opts.message))}
	result, callErr := session.Data(ctx, reader, nil)
	var problems []string
	if !reader.exhausted {
		problems = append(problems, "backendtest: Session.Data did not consume the complete reader")
	}
	if callErr != nil && len(result) != 0 {
		problems = append(problems, "backendtest: Session.Data returned both a result and an error")
	} else if callErr != nil {
		problems = append(problems, fmt.Sprintf("backendtest: Data fixture failed without an authoritative outcome: %v", callErr))
	}
	want := 1
	if opts.mode == smtpserver.ModeLMTP {
		want = len(opts.recipients)
	}
	if len(result) != want {
		problems = append(problems, fmt.Sprintf("backendtest: Session.Data returned %d results, want %d", len(result), want))
	} else if opts.mode == smtpserver.ModeLMTP {
		for i, recipient := range opts.recipients {
			if result[i].Recipient != recipient {
				problems = append(problems, fmt.Sprintf("backendtest: LMTP result %d names %q, want %q", i, result[i].Recipient, recipient))
			}
		}
	}
	for i, item := range result {
		if item.Code/100 != item.Enhanced.Class && item.Enhanced.String() != "" {
			problems = append(problems, fmt.Sprintf("backendtest: Data result %d reply/enhanced classes disagree", i))
		}
	}
	if problem := callReset(ctx, session, smtpserver.ResetCompleted); problem != "" {
		problems = append(problems, problem)
	}
	return problems
}

func checkCloseIdempotent(ctx context.Context, session *smtpserver.Session) (problems []string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			problems = append(problems, fmt.Sprintf("backendtest: Session.Close is not idempotent: %v", recovered))
		}
	}()
	session.Close(ctx, nil)
	session.Close(ctx, nil)
	return problems
}

func checkResetReasons(ctx context.Context, backend *smtpserver.Backend, opts checkOptions) []string {
	reasons := []smtpserver.ResetReason{
		smtpserver.ResetExplicit,
		smtpserver.ResetNewMail,
		smtpserver.ResetCompleted,
		smtpserver.ResetFailed,
		smtpserver.ResetStartTLS,
		smtpserver.ResetSessionEnd,
	}
	var problems []string
	for _, reason := range reasons {
		session, err := backend.NewSession(ctx, testConnInfo(opts.mode), nil)
		if err != nil {
			problems = append(problems, fmt.Sprintf("backendtest: NewSession for ResetReason(%d): %v", reason, err))
			continue
		}
		if fields := inspectSession(session); len(fields) != 0 {
			problems = append(problems, fields...)
			continue
		}
		if err := session.Mail(ctx, opts.reversePath, nil, nil); err != nil {
			problems = append(problems, fmt.Sprintf("backendtest: Mail before ResetReason(%d): %v", reason, err))
			session.Close(ctx, nil)
			continue
		}
		if problem := callReset(ctx, session, reason); problem != "" {
			problems = append(problems, problem)
		}
		session.Close(ctx, nil)
	}
	return problems
}

func callReset(ctx context.Context, session *smtpserver.Session, reason smtpserver.ResetReason) (problem string) {
	defer func() {
		if recovered := recover(); recovered != nil {
			problem = fmt.Sprintf("backendtest: ResetReason(%d) panicked: %v", reason, recovered)
		}
	}()
	session.Reset(ctx, reason, nil)
	return ""
}

func testConnInfo(mode smtpserver.Mode) *smtpserver.ConnInfo {
	return &smtpserver.ConnInfo{
		Mode:       mode,
		LocalAddr:  testAddr("local"),
		RemoteAddr: testAddr("remote"),
		TLSState:   func() *tls.ConnectionState { return nil },
	}
}

type trackingReader struct {
	io.Reader
	exhausted bool
}

func (r *trackingReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err == io.EOF {
		r.exhausted = true
	}
	return n, err
}

type testAddr string

func (a testAddr) Network() string { return string(a) }
func (a testAddr) String() string  { return string(a) }
