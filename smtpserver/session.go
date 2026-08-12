package smtpserver

import (
	"context"
	"crypto/tls"
	"io"

	"github.com/kiliant/go-smtp"
)

// Session contains the handlers for one SMTP or LMTP connection. The
// framework never calls two fields concurrently for the same Session, so
// per-session state needs no locking. Shared Backend state must be safe for
// concurrent use.
//
// Each blocking handler receives a context with a per-command deadline.
// Shutdown cancels it immediately. Peer disconnect cancellation is best-effort:
// a handler blocked without I/O learns of it when it returns and the framework
// performs the next network operation.
//
// Mail, Rcpt, Data, Reset and Close are required. CommitAuth is additionally
// required when any authentication verifier is non-nil. Other fields are
// optional; nil means the corresponding capability is unavailable.
//
// Callers constructing a Session literal must use keyed fields.
type Session struct {
	// Mail receives the parsed RFC 5321 reverse-path. reversePath is empty for
	// the null reverse-path. params is wire vocabulary; opts is framework
	// call policy, and the two grow independently.
	Mail func(ctx context.Context, reversePath string, params *smtp.MailOptions, opts *MailOptions) error
	// Rcpt receives the parsed RFC 5321 forward-path. A returned *smtp.Error
	// rejects only this recipient; the transaction remains open.
	Rcpt func(ctx context.Context, forwardPath string, params *smtp.RcptOptions, opts *RcptOptions) error
	// Data receives transparent message content through an io.Reader. DATA
	// streams live from the peer; BDAT is read from the completed bounded
	// spool. The result cardinality is one for SMTP and one per accepted RCPT,
	// in issue order including duplicates, for RFC 2033 LMTP.
	Data func(ctx context.Context, r io.Reader, opts *DataOptions) (smtp.DataResult, error)
	// Reset releases transaction state for every lifecycle path. It cannot
	// change the already-determined protocol outcome and therefore returns
	// no error.
	Reset func(ctx context.Context, reason ResetReason, opts *ResetOptions)
	// Close releases per-connection resources and must be idempotent. It is
	// called exactly once after the final Reset, if any; it is not protocol
	// QUIT.
	Close func(ctx context.Context, opts *CloseOptions)

	// Authenticate verifies PLAIN, LOGIN, EXTERNAL, OAUTHBEARER and XOAUTH2
	// credentials at the mechanism's verification point. Verification does
	// not commit authentication; CommitAuth is the sole commit point.
	Authenticate func(ctx context.Context, cred *Credentials, opts *AuthenticateOptions) (*AuthResult, error)
	// ChallengeResponse verifies a mechanism-specific challenge and response,
	// including CRAM-MD5. Verification does not commit authentication.
	ChallengeResponse func(ctx context.Context, challenge *Challenge, opts *ChallengeOptions) (*AuthResult, error)
	// SCRAMCredentials returns stored SCRAM key material for the supplied
	// authentication and authorization identities. Returning keys is not an
	// authentication event; the framework verifies the proof before CommitAuth.
	SCRAMCredentials func(ctx context.Context, cred *Credentials, opts *SCRAMOptions) (*SCRAMKeys, error)
	// CommitAuth records a successfully completed authentication in backend
	// state. It is called exactly once after all proof and mechanism round
	// trips succeed and before the framework emits 235. It cannot fail.
	CommitAuth func(ctx context.Context, result *AuthResult, opts *CommitAuthOptions)

	// Verify implements RFC 5321 VRFY when non-nil.
	Verify func(ctx context.Context, address string, opts *VerifyOptions) (string, error)
	// Expand implements RFC 5321 EXPN when non-nil.
	Expand func(ctx context.Context, list string, opts *ExpandOptions) ([]string, error)
	// Help implements RFC 5321 HELP when non-nil.
	Help func(ctx context.Context, topic string, opts *HelpOptions) (string, error)
	// ETRN implements RFC 1985 ETRN when non-nil.
	ETRN func(ctx context.Context, domain string, opts *ETRNOptions) error

	_ struct{}
}

// ResetReason identifies why an SMTP/LMTP transaction ended.
type ResetReason uint8

const (
	// ResetExplicit follows RSET.
	ResetExplicit ResetReason = iota
	// ResetNewMail precedes MAIL replacing an open transaction.
	ResetNewMail
	// ResetCompleted follows an authoritative DATA or BDAT outcome after the
	// framework attempted to emit it; write success is not required.
	ResetCompleted
	// ResetFailed follows a failed or poisoned DATA/BDAT transaction.
	ResetFailed
	// ResetStartTLS discards pre-TLS knowledge under RFC 3207 section 4.2.
	ResetStartTLS
	// ResetSessionEnd covers QUIT, disconnect, timeout, shutdown and panic
	// when a transaction is still open.
	ResetSessionEnd
)

// Credentials carries the distinct identities and secret material extracted
// at a SASL mechanism's verification point. Mechanism is an open string so a
// future SASL mechanism can use the generic Authenticate callback.
//
// AuthenticationID is SASL authcid; AuthorizationID is authzid. The framework
// never authorizes one to act as the other. TLS is transport identity and is
// independent of both.
//
// Callers constructing a Credentials literal must use keyed fields.
type Credentials struct {
	// Mechanism is the open SASL mechanism name, for example "PLAIN".
	Mechanism string
	// AuthenticationID is the SASL identity that proved the credential.
	AuthenticationID string
	// AuthorizationID is the identity the session requests to act as.
	AuthorizationID string
	// Password is populated only for password-based mechanisms.
	Password string
	// Token is populated only for token-based mechanisms.
	Token string
	// TLS is a copy of the current transport state and carries EXTERNAL's
	// client-certificate identity. It is nil on plaintext connections.
	TLS *tls.ConnectionState

	_ struct{}
}

// Challenge carries exact mechanism challenge and response octets. Mechanism
// is open-ended so future challenge-response SASL mechanisms share the callback
// without changing its signature.
//
// Callers constructing a Challenge literal must use keyed fields.
type Challenge struct {
	// Mechanism is the open SASL mechanism name.
	Mechanism string
	// Challenge contains the exact bytes issued by the framework.
	Challenge []byte
	// Response contains the exact client response bytes.
	Response []byte

	_ struct{}
}

// AuthResult is an authoritative SASL verification outcome. Failure nil means
// success. A non-nil Go error from a verifier instead means that no
// authoritative outcome exists.
//
// Callers constructing an AuthResult literal must use keyed fields.
type AuthResult struct {
	// Identity is the backend-canonicalized authenticated identity.
	Identity string
	// Failure is a refused credential decision. Nil means success.
	Failure *AuthFailure

	_ struct{}
}

// AuthFailure is a refused credential, not an error type. Err defaults to
// 535 5.7.8 when nil. OAuth carries RFC 7628 failure data where applicable.
//
// Callers constructing an AuthFailure literal must use keyed fields.
type AuthFailure struct {
	// Err is the SMTP authentication rejection. Nil defaults to 535 5.7.8.
	Err *smtp.Error
	// OAuth carries an RFC 7628 error document for OAuth mechanisms.
	OAuth *OAuthError

	_ struct{}
}

// OAuthError is the RFC 7628 JSON error document emitted during an OAuth SASL
// exchange before the final authentication failure.
//
// Callers constructing an OAuthError literal must use keyed fields.
type OAuthError struct {
	// Status is the required RFC 7628 status, such as "invalid_token".
	Status string
	// Scope is the optional OAuth scope.
	Scope string
	// OpenIDConfiguration is the optional OpenID configuration URL.
	OpenIDConfiguration string

	_ struct{}
}

// SCRAMKeys is stored SCRAM verifier material. Result remains inert until the
// framework verifies the client proof and passes it to CommitAuth.
//
// Callers constructing a SCRAMKeys literal must use keyed fields.
type SCRAMKeys struct {
	// Salt is the stored SCRAM salt.
	Salt []byte
	// Iterations is the stored SCRAM iteration count.
	Iterations int
	// StoredKey is the stored SCRAM StoredKey.
	StoredKey []byte
	// ServerKey is the stored SCRAM ServerKey.
	ServerKey []byte
	// Result remains inert until proof verification and CommitAuth.
	Result AuthResult

	_ struct{}
}

// MailOptions controls one Session.Mail call. Nil means defaults.
// Callers constructing a MailOptions literal must use keyed fields.
type MailOptions struct{ _ struct{} }

// RcptOptions controls one Session.Rcpt call. Nil means defaults.
// Callers constructing a RcptOptions literal must use keyed fields.
type RcptOptions struct{ _ struct{} }

// DataOptions controls one Session.Data call. Nil means defaults.
// Callers constructing a DataOptions literal must use keyed fields.
type DataOptions struct{ _ struct{} }

// ResetOptions controls one Session.Reset call. Nil means defaults.
// Callers constructing a ResetOptions literal must use keyed fields.
type ResetOptions struct{ _ struct{} }

// CloseOptions controls one Session.Close call. Nil means defaults.
// Callers constructing a CloseOptions literal must use keyed fields.
type CloseOptions struct{ _ struct{} }

// AuthenticateOptions controls one Session.Authenticate call. Nil means
// defaults. Callers constructing an AuthenticateOptions literal must use keyed
// fields.
type AuthenticateOptions struct{ _ struct{} }

// ChallengeOptions controls one Session.ChallengeResponse call. Nil means
// defaults. Callers constructing a ChallengeOptions literal must use keyed
// fields.
type ChallengeOptions struct{ _ struct{} }

// SCRAMOptions controls one Session.SCRAMCredentials call. Nil means defaults.
// Callers constructing a SCRAMOptions literal must use keyed fields.
type SCRAMOptions struct{ _ struct{} }

// CommitAuthOptions controls one Session.CommitAuth call. Nil means defaults.
// Callers constructing a CommitAuthOptions literal must use keyed fields.
type CommitAuthOptions struct{ _ struct{} }

// VerifyOptions controls one Session.Verify call. Nil means defaults.
// Callers constructing a VerifyOptions literal must use keyed fields.
type VerifyOptions struct{ _ struct{} }

// ExpandOptions controls one Session.Expand call. Nil means defaults.
// Callers constructing an ExpandOptions literal must use keyed fields.
type ExpandOptions struct{ _ struct{} }

// HelpOptions controls one Session.Help call. Nil means defaults.
// Callers constructing a HelpOptions literal must use keyed fields.
type HelpOptions struct{ _ struct{} }

// ETRNOptions controls one Session.ETRN call. Nil means defaults.
// Callers constructing an ETRNOptions literal must use keyed fields.
type ETRNOptions struct{ _ struct{} }
