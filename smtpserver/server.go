package smtpserver

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"sync"

	"github.com/kiliant/go-smtp"
)

const defaultMaxConnections = 1000

// ServerOptions configures one SMTP or LMTP server instance. Spool and
// connection bounds are instance-wide, not process-wide: two Server values have
// independent budgets. Nil means defaults, but Listener and Backend remain
// required and NewServer reports their absence.
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
	// Trace receives redacted protocol-line events. It never receives SASL
	// payloads or message content.
	Trace func(smtp.TraceEvent)
	// ErrorLog receives framework and backend-contract defects. Nil discards
	// them; protocol outcomes still reach the peer where framing permits.
	ErrorLog func(ErrorEvent)

	_ struct{}
}

// Server owns one SMTP or LMTP listener, its active connections and its bounded
// CHUNKING spool budget. Construct one with NewServer.
type Server struct {
	listener    net.Listener
	backend     *Backend
	mode        listenerMode
	tlsConfig   *tls.Config
	implicitTLS bool
	spools      *spoolManager
	connections *connectionRegistry
	trace       func(smtp.TraceEvent)
	errorLog    func(ErrorEvent)

	shutdownOnce sync.Once
	shutdownErr  error
}

// NewServer validates opts and constructs one server instance. It performs no
// network I/O. Every configuration defect is reported in the returned error so
// an operator can fix one startup failure instead of discovering them serially.
func NewServer(opts *ServerOptions) (*Server, error) {
	var value ServerOptions
	if opts != nil {
		value = *opts
	}
	if value.MaxConnections == 0 {
		value.MaxConnections = defaultMaxConnections
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
	if value.MaxConnections < 0 {
		if err == nil {
			err = errors.New("smtpserver: invalid server options: MaxConnections must not be negative")
		} else {
			err = errors.New(err.Error() + "; MaxConnections must not be negative")
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
		listener:    value.Listener,
		backend:     value.Backend,
		mode:        mode,
		tlsConfig:   tlsConfig,
		implicitTLS: value.ImplicitTLS,
		spools:      spools,
		connections: newConnectionRegistry(value.MaxConnections),
		trace:       value.Trace,
		errorLog:    value.ErrorLog,
	}, nil
}

// Shutdown stops accepting new connections, cancels active handler contexts,
// and waits for connections to leave at a protocol-legal point. When ctx
// expires, it force-closes remaining transports and returns ctx.Err().
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

// ShutdownOptions controls Server.Shutdown. Nil means defaults.
// Callers constructing a ShutdownOptions literal must use keyed fields.
type ShutdownOptions struct{ _ struct{} }

// ErrorEvent reports one operational framework or backend-contract defect.
// It is not a protocol error model: SMTP/LMTP failures remain *smtp.Error.
// Connection is nil when the defect is not associated with one peer.
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
