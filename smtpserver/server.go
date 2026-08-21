package smtpserver

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kiliant/go-smtp"
)

const (
	defaultMaxConnections          = 1000
	defaultMaxConnectionsPerSource = 32
	defaultMaxMessageBytes         = 64 << 20
	defaultMaxRecipients           = 100
	defaultMaxTransactions         = 100
	defaultCommandTimeout          = 5 * time.Minute
	defaultDataTimeout             = 10 * time.Minute
)

var defaultAuthMechanismsAfterTLS = []string{
	"SCRAM-SHA-256-PLUS",
	"SCRAM-SHA-256",
	"SCRAM-SHA-1-PLUS",
	"SCRAM-SHA-1",
	"CRAM-MD5",
	"OAUTHBEARER",
	"XOAUTH2",
	"EXTERNAL",
	"PLAIN",
	"LOGIN",
}

// ServerOptions configures one RFC 5321 SMTP or RFC 2033 LMTP server instance.
// Spool and connection bounds are instance-wide, not process-wide: two Server
// values have independent budgets. RFC 3030 BDAT content is accepted into the
// bounded spool in full before Session.Data is called; RFC 5321 DATA streams
// directly to Session.Data and applies TCP backpressure to the peer. Nil means
// defaults, but Listener and Backend remain required and NewServer reports
// their absence.
//
// Callers constructing a ServerOptions literal must use keyed fields.
type ServerOptions struct {
	// Listener supplies accepted connections and fixes the listener address.
	Listener net.Listener
	// Backend supplies the shared, concurrency-safe session factory.
	Backend *Backend
	// Mode selects SMTP or LMTP. The zero value means ModeSMTP.
	Mode Mode
	// TLSConfig enables STARTTLS and supplies implicit-TLS handshakes when
	// ImplicitTLS is true. NewServer clones it before use.
	TLSConfig *tls.Config
	// ImplicitTLS performs TLS before Backend.NewSession and the greeting.
	ImplicitTLS bool
	// EnableCHUNKING enables RFC 3030 CHUNKING and requires every spool bound.
	EnableCHUNKING bool
	// EnableBINARYMIME enables RFC 3030 BINARYMIME and requires CHUNKING.
	EnableBINARYMIME bool
	// MaxSpoolBytes bounds client-supplied octets in one BDAT transaction.
	MaxSpoolBytes int64
	// MaxSpoolMemoryBytes bounds the memory prefix of one BDAT spool.
	MaxSpoolMemoryBytes int64
	// MaxTotalSpoolBytes bounds all live spool bytes in this Server.
	MaxTotalSpoolBytes int64
	// MaxTotalSpoolMemoryBytes bounds all memory-resident spool bytes in this
	// Server.
	MaxTotalSpoolMemoryBytes int64
	// MaxConcurrentSpools bounds live transaction spools in this Server.
	MaxConcurrentSpools int
	// SpoolDir is the spill directory. Empty uses os.TempDir.
	SpoolDir string
	// MaxConnections bounds live connections in this Server. Zero uses a safe
	// default of 1000; it never means unbounded.
	MaxConnections int
	// MaxConnectionsPerSource bounds live connections from one source address.
	// Zero uses a safe default of 32; it never means unbounded. IP addresses are
	// matched exactly after canonicalisation, without DNS or subnet grouping.
	MaxConnectionsPerSource int
	// GreetingIdentity is the RFC 5321 domain or address-literal used in the
	// greeting, EHLO/LHLO replies and Received fields. Empty uses the local
	// hostname, falling back to localhost.
	GreetingIdentity string
	// CommandTimeout bounds one non-content command stage. Zero uses five
	// minutes.
	CommandTimeout time.Duration
	// DataTimeout bounds one complete DATA or BDAT content stage. Zero uses ten
	// minutes.
	DataTimeout time.Duration
	// MaxMessageBytes is advertised through RFC 1870 SIZE and enforced against
	// client-supplied octets independently of a peer declaration. Zero uses a
	// safe 64 MiB default, reduced to MaxSpoolBytes when CHUNKING is enabled
	// with a smaller spool. An explicit value above MaxSpoolBytes is invalid.
	MaxMessageBytes int64
	// MaxRecipients bounds successful RCPT commands per transaction, counting
	// duplicates. Zero uses 100; a non-zero value below RFC 5321's required
	// minimum of 100 is invalid.
	MaxRecipients int
	// MaxTransactions bounds MAIL commands per connection, including commands
	// rejected for syntax, state, policy or by the backend. Zero uses 100. The
	// maximum is RFC 9422's six-digit MAILMAX limit, 999999.
	MaxTransactions int
	// RequireTLS rejects MAIL until the connection is TLS-protected. This is
	// listener policy and is distinct from RFC 8689's per-message REQUIRETLS.
	RequireTLS bool
	// RequireAuth rejects MAIL until RFC 4954 authentication has succeeded.
	RequireAuth bool
	// AuthMechanismsBeforeTLS is an ordered, case-insensitive allowlist. Nil
	// selects the safe default of no plaintext AUTH advertisement; a non-nil
	// empty slice disables it explicitly.
	AuthMechanismsBeforeTLS []string
	// AuthMechanismsAfterTLS is an ordered, case-insensitive allowlist. Nil
	// selects every SASL responder implemented by the framework; a non-nil
	// empty slice disables AUTH after TLS.
	AuthMechanismsAfterTLS []string
	// Trace receives redacted protocol-line events. It never receives SASL
	// payloads or message content.
	Trace func(smtp.TraceEvent)
	// ErrorLog receives framework and backend-contract defects. Nil discards
	// them; protocol outcomes still reach the peer where framing permits.
	ErrorLog func(ErrorEvent)

	_ struct{}
}

// Server owns one RFC 5321 SMTP or RFC 2033 LMTP listener, its active
// connections and its bounded RFC 3030 CHUNKING spool budget. Construct one
// with NewServer.
type Server struct {
	listener        net.Listener
	backend         *Backend
	mode            listenerMode
	tlsConfig       *tls.Config
	implicitTLS     bool
	spools          *spoolManager
	chunking        bool
	binaryMIME      bool
	connections     *connectionRegistry
	trace           func(smtp.TraceEvent)
	errorLog        func(ErrorEvent)
	identity        string
	timeouts        serverTimeouts
	maxMessage      int64
	maxRcpt         int
	maxTransactions int
	requireTLS      bool
	requireAuth     bool
	authBefore      []string
	authAfter       []string

	shutdownOnce sync.Once
	shutdownErr  error
	serveMu      sync.Mutex
	served       bool
}

// NewServer validates opts and constructs one RFC 5321 SMTP or RFC 2033 LMTP
// server instance. It performs no network I/O. Every configuration defect is
// reported in the returned error so an operator can fix one startup failure
// instead of discovering them serially.
func NewServer(opts *ServerOptions) (*Server, error) {
	var value ServerOptions
	if opts != nil {
		value = *opts
	}
	if value.MaxConnections == 0 {
		value.MaxConnections = defaultMaxConnections
	}
	if value.MaxConnectionsPerSource == 0 {
		value.MaxConnectionsPerSource = defaultMaxConnectionsPerSource
	}
	if value.GreetingIdentity == "" {
		value.GreetingIdentity = localHostname()
	}
	if value.CommandTimeout == 0 {
		value.CommandTimeout = defaultCommandTimeout
	}
	if value.DataTimeout == 0 {
		value.DataTimeout = defaultDataTimeout
	}
	messageLimitExplicit := value.MaxMessageBytes != 0
	if value.MaxMessageBytes == 0 {
		value.MaxMessageBytes = defaultMaxMessageBytes
	}
	if value.MaxRecipients == 0 {
		value.MaxRecipients = defaultMaxRecipients
	}
	if value.MaxTransactions == 0 {
		value.MaxTransactions = defaultMaxTransactions
	}
	if value.AuthMechanismsBeforeTLS == nil {
		value.AuthMechanismsBeforeTLS = []string{}
	} else {
		value.AuthMechanismsBeforeTLS = append([]string(nil), value.AuthMechanismsBeforeTLS...)
	}
	if value.AuthMechanismsAfterTLS == nil {
		value.AuthMechanismsAfterTLS = append([]string(nil), defaultAuthMechanismsAfterTLS...)
	} else {
		value.AuthMechanismsAfterTLS = append([]string(nil), value.AuthMechanismsAfterTLS...)
	}
	mode, modeProblem := internalMode(value.Mode)
	config := constructionConfig{
		listener:            value.Listener,
		mode:                mode,
		backendNewSession:   value.Backend != nil && value.Backend.NewSession != nil,
		chunking:            value.EnableCHUNKING,
		binaryMIME:          value.EnableBINARYMIME,
		maxSpoolBytes:       value.MaxSpoolBytes,
		maxSpoolMemoryBytes: value.MaxSpoolMemoryBytes,
		maxTotalSpoolBytes:  value.MaxTotalSpoolBytes,
		maxTotalSpoolMemory: value.MaxTotalSpoolMemoryBytes,
		maxConcurrentSpools: value.MaxConcurrentSpools,
		maxConnections:      value.MaxConnections,
		greetingIdentity:    value.GreetingIdentity,
		commandTimeout:      value.CommandTimeout,
		dataTimeout:         value.DataTimeout,
		maxMessageBytes:     value.MaxMessageBytes,
		maxRecipients:       value.MaxRecipients,
		maxTransactions:     value.MaxTransactions,
		authBefore:          value.AuthMechanismsBeforeTLS,
		authAfter:           value.AuthMechanismsAfterTLS,
	}
	err := validateConstruction(config)
	if modeProblem != "" {
		if err == nil {
			err = errors.New("smtpserver: invalid server options: " + modeProblem)
		} else {
			err = errors.New(err.Error() + "; " + modeProblem)
		}
	}
	if value.ImplicitTLS && value.TLSConfig == nil {
		if err == nil {
			err = errors.New("smtpserver: invalid server options: TLSConfig is required for implicit TLS")
		} else {
			err = errors.New(err.Error() + "; TLSConfig is required for implicit TLS")
		}
	}
	if value.RequireTLS && value.TLSConfig == nil {
		err = appendOptionProblem(err, "TLSConfig is required when RequireTLS is enabled")
	}
	if value.RequireAuth && len(value.AuthMechanismsBeforeTLS) == 0 && (value.TLSConfig == nil || len(value.AuthMechanismsAfterTLS) == 0) {
		err = appendOptionProblem(err, "RequireAuth needs an AUTH mechanism before TLS or an available TLS-authentication path")
	}
	if value.MaxConnections < 0 {
		if err == nil {
			err = errors.New("smtpserver: invalid server options: MaxConnections must not be negative")
		} else {
			err = errors.New(err.Error() + "; MaxConnections must not be negative")
		}
	}
	if value.MaxConnectionsPerSource < 0 {
		if err == nil {
			err = errors.New("smtpserver: invalid server options: MaxConnectionsPerSource must not be negative")
		} else {
			err = errors.New(err.Error() + "; MaxConnectionsPerSource must not be negative")
		}
	}
	if value.EnableCHUNKING && value.MaxSpoolBytes > 0 && value.MaxMessageBytes > value.MaxSpoolBytes {
		if messageLimitExplicit {
			err = appendOptionProblem(err, "MaxMessageBytes must not exceed MaxSpoolBytes when CHUNKING is enabled")
		} else {
			value.MaxMessageBytes = value.MaxSpoolBytes
		}
	}
	if err != nil {
		return nil, err
	}

	var spools *spoolManager
	if value.EnableCHUNKING {
		spools, err = newSpoolManager(spoolOptions{
			MaxBytes:            value.MaxSpoolBytes,
			MaxMemoryBytes:      value.MaxSpoolMemoryBytes,
			MaxTotalBytes:       value.MaxTotalSpoolBytes,
			MaxTotalMemoryBytes: value.MaxTotalSpoolMemoryBytes,
			MaxConcurrent:       value.MaxConcurrentSpools,
			Dir:                 value.SpoolDir,
		})
		if err != nil {
			return nil, err
		}
	}
	var tlsConfig *tls.Config
	if value.TLSConfig != nil {
		tlsConfig = value.TLSConfig.Clone()
	}
	return &Server{
		listener:        value.Listener,
		backend:         value.Backend,
		mode:            mode,
		tlsConfig:       tlsConfig,
		implicitTLS:     value.ImplicitTLS,
		spools:          spools,
		chunking:        value.EnableCHUNKING,
		binaryMIME:      value.EnableBINARYMIME,
		connections:     newConnectionRegistry(value.MaxConnections, value.MaxConnectionsPerSource),
		trace:           value.Trace,
		errorLog:        value.ErrorLog,
		identity:        value.GreetingIdentity,
		timeouts:        serverTimeouts{command: value.CommandTimeout, data: value.DataTimeout},
		maxMessage:      value.MaxMessageBytes,
		maxRcpt:         value.MaxRecipients,
		maxTransactions: value.MaxTransactions,
		requireTLS:      value.RequireTLS,
		requireAuth:     value.RequireAuth,
		authBefore:      value.AuthMechanismsBeforeTLS,
		authAfter:       value.AuthMechanismsAfterTLS,
	}, nil
}

// Shutdown stops accepting new RFC 5321 SMTP or RFC 2033 LMTP connections,
// cancels active handler contexts, and waits for connections to leave at a
// protocol-legal point. When ctx expires, it force-closes remaining transports
// and returns ctx.Err().
func (s *Server) Shutdown(ctx context.Context, opts *ShutdownOptions) error {
	if ctx == nil {
		panic("smtpserver: nil context")
	}
	s.shutdownOnce.Do(func() {
		if err := s.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			s.shutdownErr = err
		}
		if err := s.connections.shutdown(ctx); err != nil && s.shutdownErr == nil {
			s.shutdownErr = err
		}
	})
	return s.shutdownErr
}

// ShutdownOptions controls RFC 5321 or RFC 2033 Server.Shutdown. Nil means
// defaults.
// Callers constructing a ShutdownOptions literal must use keyed fields.
type ShutdownOptions struct{ _ struct{} }

// ErrorEvent reports one operational RFC 5321 SMTP or RFC 2033 LMTP framework
// or backend-contract defect. It is not a protocol error model: SMTP/LMTP
// failures remain *smtp.Error. Connection is nil when the defect is not
// associated with one peer.
//
// Callers receive ErrorEvent values; tests constructing one must use keyed
// fields.
type ErrorEvent struct {
	// Err is the operational or backend-contract cause.
	Err error
	// Connection identifies the affected session when one exists.
	Connection *ConnInfo

	_ struct{}
}

func internalMode(mode Mode) (listenerMode, string) {
	switch mode {
	case "", ModeSMTP:
		return modeSMTP, ""
	case ModeLMTP:
		return modeLMTP, ""
	default:
		return modeSMTP, "Mode must be smtp or lmtp"
	}
}

type serverTimeouts struct {
	command time.Duration
	data    time.Duration
}

func localHostname() string {
	hostname, err := os.Hostname()
	if err == nil && validGreetingIdentity(hostname) {
		return hostname
	}
	return "localhost"
}

func validGreetingIdentity(identity string) bool {
	if identity == "" || len(identity) > 255 {
		return false
	}
	for i := 0; i < len(identity); i++ {
		if identity[i] < 0x21 || identity[i] > 0x7e {
			return false
		}
	}
	if identity[0] == '[' {
		if len(identity) < 3 || identity[len(identity)-1] != ']' {
			return false
		}
		inside := identity[1 : len(identity)-1]
		if strings.HasPrefix(strings.ToUpper(inside), "IPV6:") {
			address, parseErr := netip.ParseAddr(inside[len("IPv6:"):])
			return parseErr == nil && address.Is6()
		}
		if address, parseErr := netip.ParseAddr(inside); parseErr == nil {
			return address.Is4()
		}
		tag, value, ok := strings.Cut(inside, ":")
		return ok && validGreetingLabel(tag) && value != "" && !strings.ContainsAny(value, "[]\\")
	}
	for _, label := range strings.Split(identity, ".") {
		if !validGreetingLabel(label) || len(label) > 63 {
			return false
		}
	}
	return true
}

func validGreetingLabel(label string) bool {
	if label == "" || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for i := 0; i < len(label); i++ {
		c := label[i]
		if (c < 'A' || c > 'Z') && (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}
	return true
}

func appendOptionProblem(current error, problem string) error {
	if current == nil {
		return errors.New("smtpserver: invalid server options: " + problem)
	}
	return errors.New(current.Error() + "; " + problem)
}
