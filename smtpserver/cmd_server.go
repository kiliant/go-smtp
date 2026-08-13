package smtpserver

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"math"
	"net"
	"sync"
	"time"

	"github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

// ServeOptions configures one call to Server.Serve. Nil means defaults.
// Callers constructing a ServeOptions literal must use keyed fields.
type ServeOptions struct{ _ struct{} }

// Serve accepts connections from the listener supplied to NewServer and
// serves RFC 5321 SMTP or RFC 2033 LMTP until ctx is cancelled, the listener
// is closed, or an accept fails. Only one Serve call may run for a Server.
func (s *Server) Serve(ctx context.Context, opts *ServeOptions) error {
	_ = opts
	if ctx == nil {
		panic("smtpserver: nil context")
	}
	if s == nil || s.listener == nil {
		return errors.New("smtpserver: nil Server")
	}
	if !s.beginServe() {
		return errors.New("smtpserver: Serve called more than once")
	}

	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = s.listener.Close()
		case <-stop:
		}
	}()
	defer close(stop)

	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("smtpserver: accept: %w", err)
		}
		connectionCtx, cancel := context.WithCancel(ctx)
		if !s.connections.register(conn, cancel) {
			cancel()
			writeConnectionLimit(conn)
			_ = conn.Close()
			continue
		}
		go s.serveConnection(connectionCtx, cancel, conn)
	}
}

func (s *Server) beginServe() bool {
	s.serveMu.Lock()
	defer s.serveMu.Unlock()
	if s.served {
		return false
	}
	s.served = true
	return true
}

func writeConnectionLimit(conn net.Conn) {
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = smtpwire.EncodeReply(conn, smtpwire.Reply{Code: 421, Text: "Too many connections"}, smtpwire.ReplyOptions{
		Enhanced: &smtpwire.EnhancedCode{Class: 4, Subject: 3, Detail: 2},
	})
}

type connectionTLSState struct {
	mu    sync.Mutex
	state *tls.ConnectionState
}

func (s *connectionTLSState) set(state tls.ConnectionState) {
	s.mu.Lock()
	s.state = &state
	s.mu.Unlock()
}

func (s *connectionTLSState) get() *tls.ConnectionState {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state == nil {
		return nil
	}
	copy := *s.state
	return &copy
}

func (s *Server) serveConnection(ctx context.Context, cancel context.CancelFunc, raw net.Conn) {
	defer cancel()
	defer s.connections.unregister(raw)
	defer raw.Close()

	transport := raw
	tlsState := &connectionTLSState{}
	if s.implicitTLS {
		handshakeCtx, stop := context.WithTimeout(ctx, s.timeouts.command)
		conn, err := handshakeTLS(handshakeCtx, raw, s.tlsConfig, nil, nil)
		stop()
		if err != nil {
			s.reportError(err, nil)
			return
		}
		transport = conn
		tlsState.set(conn.ConnectionState())
	}

	connInfo := &ConnInfo{
		Mode:       publicMode(s.mode),
		LocalAddr:  raw.LocalAddr(),
		RemoteAddr: raw.RemoteAddr(),
		TLSState:   tlsState.get,
	}
	commandCtx, stop := context.WithTimeout(ctx, s.timeouts.command)
	backendSession, err := s.backend.NewSession(commandCtx, connInfo, nil)
	stop()
	if err != nil {
		s.writeGreetingError(transport, err)
		return
	}
	if err := validateSession(backendSession); err != nil {
		s.reportError(err, connInfo)
		s.writeGreetingError(transport, &smtp.Error{Code: 421, Text: "Service not available"})
		if backendSession != nil && backendSession.Close != nil {
			cleanupCtx, cleanupStop := context.WithTimeout(context.Background(), s.timeouts.command)
			backendSession.Close(cleanupCtx, nil)
			cleanupStop()
		}
		return
	}
	if s.requireAuth && !s.authReachable(backendSession, tlsState) {
		contractErr := fmt.Errorf("%w: RequireAuth has no compatible Session verifier", errBackendAuthContract)
		s.reportError(contractErr, connInfo)
		s.writeGreetingError(transport, &smtp.Error{Code: 421, Enhanced: smtp.EnhancedCode{Class: 4, Subject: 3, Detail: 0}, Text: "Service not available"})
		cleanupCtx, cleanupStop := context.WithTimeout(context.Background(), s.timeouts.command)
		backendSession.Close(cleanupCtx, nil)
		cleanupStop()
		return
	}

	connection := newCommandSession(s, ctx, raw, transport, tlsState, connInfo, backendSession)
	defer connection.close()
	defer func() {
		if recovered := recover(); recovered != nil {
			s.reportError(fmt.Errorf("smtpserver: backend or command panic: %v", recovered), connInfo)
		}
	}()
	if err := connection.writeReply(wireReply{code: 220, text: s.identity + " Service ready", context: smtpwire.ReplyContextGreeting}); err != nil {
		s.reportError(err, connInfo)
		return
	}
	if err := connection.writer.Flush(); err != nil {
		s.reportError(err, connInfo)
		return
	}
	if err := connection.loop.run(ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
		s.reportError(err, connInfo)
	}
}

func (s *Server) writeGreetingError(conn net.Conn, err error) {
	reply := errorReply("greeting", err, 421, "Service not available")
	reply.context = smtpwire.ReplyContextGreeting
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_ = encodeWireReply(conn, reply)
}

func (s *Server) reportError(err error, conn *ConnInfo) {
	if err == nil || s.errorLog == nil {
		return
	}
	s.errorLog(ErrorEvent{Err: err, Connection: conn})
}

func publicMode(mode listenerMode) Mode {
	if mode == modeLMTP {
		return ModeLMTP
	}
	return ModeSMTP
}

func newCommandSession(server *Server, ctx context.Context, raw, transport net.Conn, tlsState *connectionTLSState, info *ConnInfo, backend *Session) *commandSession {
	reader := smtpwire.NewLineReader(transport)
	writer := bufio.NewWriter(transport)
	session := &commandSession{
		server:    server,
		ctx:       ctx,
		raw:       raw,
		transport: transport,
		tlsState:  tlsState,
		info:      info,
		backend:   backend,
		lifecycle: newSessionLifecycle(backend),
		state:     newProtocolState(server.mode),
		reader:    reader,
		writer:    writer,
	}
	if tlsState.get() != nil {
		session.state.tls = true
	}
	limits := smtpwire.Limits{MaxBDATChunkSize: math.MaxInt64}
	session.loop = commandLoop{
		reader:       reader,
		writer:       writer,
		limits:       limits,
		readDeadline: func() time.Time { return session.deadline(server.timeouts.command) },
		execute:      session.execute,
	}
	return session
}
