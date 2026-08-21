package smtpserver

import (
	"context"
	"crypto/tls"
	"io"
	"net"

	"github.com/kiliant/go-smtp"
)

// Session contains the handlers for one RFC 5321 SMTP or RFC 2033 LMTP
// connection. The framework never calls two fields concurrently for the same
// Session, so per-session state needs no locking. Shared Backend state must be
// safe for concurrent use.
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
	// ATRN implements RFC 2645 On-Demand Mail Relay when non-nil. The
	// framework validates and authorizes the request through this callback,
	// emits and flushes the successful 250 reply, then invokes the returned
	// takeover callback with exclusive use of the current transport.
	ATRN func(ctx context.Context, domains []string, opts *ATRNOptions) (*ATRNResult, error)

	// ParameterExtensions declares optional ESMTP capabilities whose semantics
	// are carried entirely by MAIL/RCPT parameters. Unknown parameters remain
	// available through smtp.MailOptions.Extra and smtp.RcptOptions.Extra, so a
	// backend can enable a future parameter extension before this framework
	// models it with typed fields.
	ParameterExtensions []ParameterExtension
	// Limits declares the RFC 9422 LIMITS capability. Nil omits it while the
	// server's transaction bound remains enforced. For a non-nil value, the
	// framework shallow-copies the declaration and advertises MAILMAX as the
	// lower non-zero value of Limits.MailMax and ServerOptions.MaxTransactions;
	// a zero MailMax inherits the server bound. Other fields and Extra are
	// preserved, and the backend-owned value is never mutated.
	Limits *smtp.Limits

	_ struct{}
}

// ParameterExtension declares one open-ended RFC 5321 §2.2 EHLO capability
// whose wire semantics are carried by MAIL FROM or RCPT TO parameters. Keyword
// is an smtp.Extension rather than a closed enum; Params is the raw EHLO
// parameter text following the keyword. Command extensions need a dedicated
// Session callback and must not be declared through this type.
//
// Callers constructing a ParameterExtension literal must use keyed fields.
type ParameterExtension struct {
	// Keyword is the upper-case EHLO keyword.
	Keyword smtp.Extension
	// Params is the optional raw EHLO parameter text, without CRLF framing.
	Params string

	_ struct{}
}

// ATRNResult is an authoritative successful RFC 2645 ATRN decision. Takeover
// is required: after the framework flushes the 250 reply it receives exclusive
// protocol use of Conn until it returns. A typical callback constructs an
// smtpclient.Client on Conn through the client's connection-injection API.
//
// Callers constructing an ATRNResult literal must use keyed fields.
type ATRNResult struct {
	// Takeover drives the provider-as-client half of the reversed connection.
	// The framework retains shutdown cancellation and closes the transport
	// after Takeover returns. Takeover must not retain conn after returning.
	Takeover func(ctx context.Context, conn net.Conn, opts *ATRNTakeoverOptions) error

	_ struct{}
}

// ResetReason identifies why an RFC 5321 SMTP or RFC 2033 LMTP transaction
// ended.
type ResetReason uint8

const (
	// ResetExplicit follows RFC 5321 RSET.
	ResetExplicit ResetReason = iota
	// ResetNewMail precedes RFC 5321 MAIL replacing an open transaction.
	ResetNewMail
	// ResetCompleted follows an authoritative RFC 5321 DATA or RFC 3030 BDAT
	// outcome after the framework attempted to emit it; write success is not
	// required.
	ResetCompleted
	// ResetFailed follows a failed or poisoned RFC 5321 DATA or RFC 3030 BDAT
	// transaction.
	ResetFailed
	// ResetStartTLS discards pre-TLS knowledge under RFC 3207 section 4.2.
	ResetStartTLS
	// ResetSessionEnd covers RFC 5321 QUIT, disconnect, timeout, shutdown and
	// panic when a transaction is still open.
	ResetSessionEnd
)

// Credentials carries the distinct identities and secret material extracted
// at an RFC 4422 SASL mechanism's RFC 4954 verification point. Mechanism is an
// open string so a future SASL mechanism can use the generic Authenticate
// callback.
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

// Challenge carries exact RFC 4422 SASL mechanism challenge and response
// octets used by RFC 4954 AUTH. Mechanism is open-ended so future
// challenge-response SASL mechanisms share the callback without changing its
// signature.
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

// AuthResult is an authoritative RFC 4954 SASL verification outcome. Failure
// nil means success. A non-nil Go error from a verifier instead means that no
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

// AuthFailure is a refused RFC 4954 credential, not an error type. Err defaults
// to 535 5.7.8 when nil. OAuth carries RFC 7628 failure data where applicable.
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

// SCRAMKeys is stored RFC 5802 or RFC 7677 SCRAM verifier material. Result
// remains inert until the framework verifies the client proof and passes it to
// CommitAuth.
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

// MailOptions controls one RFC 5321 Session.Mail call. Nil means defaults.
// Callers constructing a MailOptions literal must use keyed fields.
type MailOptions struct{ _ struct{} }

// RcptOptions controls one RFC 5321 Session.Rcpt call. Nil means defaults, and
// a backend handler must accept nil. The production framework supplies a
// non-nil value so a backend may append success continuation lines for an
// extension such as RFC 4141 CONNEG; each line is emitted under the normal 250
// reply after the framework's recipient-accepted first line.
// Callers constructing a RcptOptions literal must use keyed fields.
type RcptOptions struct {
	// SuccessLines contains extension-specific successful RCPT reply lines.
	// Lines must not contain CR, LF, or NUL. The slice is
	// open-ended so a future reply-bearing RCPT extension can use this seam.
	SuccessLines []string

	_ struct{}
}

// DataOptions controls one RFC 5321 DATA, RFC 3030 BDAT, or RFC 2033 LMTP
// Session.Data call. Nil means defaults.
// Callers constructing a DataOptions literal must use keyed fields.
type DataOptions struct{ _ struct{} }

// ResetOptions controls one RFC 5321 or RFC 2033 Session.Reset call. Nil means
// defaults.
// Callers constructing a ResetOptions literal must use keyed fields.
type ResetOptions struct{ _ struct{} }

// CloseOptions controls one RFC 5321 or RFC 2033 Session.Close call. Nil means
// defaults.
// Callers constructing a CloseOptions literal must use keyed fields.
type CloseOptions struct{ _ struct{} }

// AuthenticateOptions controls one RFC 4954 Session.Authenticate call. Nil
// means defaults. Callers constructing an AuthenticateOptions literal must use
// keyed fields.
type AuthenticateOptions struct{ _ struct{} }

// ChallengeOptions controls one RFC 4954 Session.ChallengeResponse call. Nil
// means defaults. Callers constructing a ChallengeOptions literal must use
// keyed fields.
type ChallengeOptions struct{ _ struct{} }

// SCRAMOptions controls one RFC 5802 or RFC 7677 Session.SCRAMCredentials call.
// Nil means defaults.
// Callers constructing a SCRAMOptions literal must use keyed fields.
type SCRAMOptions struct{ _ struct{} }

// CommitAuthOptions controls the RFC 4954 Session.CommitAuth point. Nil means
// defaults.
// Callers constructing a CommitAuthOptions literal must use keyed fields.
type CommitAuthOptions struct{ _ struct{} }

// VerifyOptions controls one RFC 5321 Session.Verify call. Nil means defaults.
// Callers constructing a VerifyOptions literal must use keyed fields.
type VerifyOptions struct{ _ struct{} }

// ExpandOptions controls one RFC 5321 Session.Expand call. Nil means defaults.
// Callers constructing an ExpandOptions literal must use keyed fields.
type ExpandOptions struct{ _ struct{} }

// HelpOptions controls one RFC 5321 Session.Help call. Nil means defaults.
// Callers constructing a HelpOptions literal must use keyed fields.
type HelpOptions struct{ _ struct{} }

// ETRNOptions controls one RFC 1985 Session.ETRN call. Nil means defaults.
// Callers constructing an ETRNOptions literal must use keyed fields.
type ETRNOptions struct{ _ struct{} }

// ATRNOptions controls one RFC 2645 Session.ATRN authorization and queue
// decision. Nil means defaults.
// Callers constructing an ATRNOptions literal must use keyed fields.
type ATRNOptions struct{ _ struct{} }

// ATRNTakeoverOptions controls one successful RFC 2645 ATRNResult.Takeover
// call. Nil means defaults.
// Callers constructing an ATRNTakeoverOptions literal must use keyed fields.
type ATRNTakeoverOptions struct{ _ struct{} }
