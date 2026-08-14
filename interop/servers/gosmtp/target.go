package gosmtp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/kiliant/go-smtp/smtpserver"
	"github.com/kiliant/go-smtp/smtpserver/memory"
)

const (
	defaultCommandTimeout = 10 * time.Second
	defaultDataTimeout    = 30 * time.Second
	defaultSpoolBytes     = 256 << 20
	defaultSpoolMemory    = 1 << 20
)

// Target is one running, in-process SMTP interoperability target.
type Target struct {
	listener net.Listener
	server   *smtpserver.Server
	sink     *Sink
	params   *parameterObserver
	cancel   context.CancelFunc
	done     chan error

	stopOnce sync.Once
	stopErr  error
}

// Start binds an ephemeral loopback TCP listener and starts the go-smtp
// server with its in-memory backend. Readiness is deliberately not inferred
// from this function returning: the matrix still health-gates it with a real
// EHLO through its normal profile assertion.
func Start(ctx context.Context) (*Target, error) {
	return startTarget(ctx, smtpserver.ModeSMTP, "127.0.0.1:0")
}

func startTarget(ctx context.Context, mode smtpserver.Mode, address string) (*Target, error) {
	if ctx == nil {
		panic("gosmtp: nil context")
	}
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("gosmtp: listen: %w", err)
	}

	memorySink := memory.New(nil)
	parameters := &parameterObserver{}
	server, err := smtpserver.NewServer(&smtpserver.ServerOptions{
		Listener:                 listener,
		Backend:                  profileBackend(memorySink.Backend(), parameters),
		Mode:                     mode,
		EnableCHUNKING:           true,
		EnableBINARYMIME:         true,
		MaxSpoolBytes:            defaultSpoolBytes,
		MaxSpoolMemoryBytes:      defaultSpoolMemory,
		MaxTotalSpoolBytes:       2 * defaultSpoolBytes,
		MaxTotalSpoolMemoryBytes: 8 * defaultSpoolMemory,
		MaxConcurrentSpools:      8,
		GreetingIdentity:         "gosmtp.example.test",
		CommandTimeout:           defaultCommandTimeout,
		DataTimeout:              defaultDataTimeout,
		MaxMessageBytes:          defaultSpoolBytes,
	})
	if err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("gosmtp: construct server: %w", err)
	}

	serveCtx, cancel := context.WithCancel(ctx)
	target := &Target{
		listener: listener,
		server:   server,
		sink:     newSink(memorySink),
		params:   parameters,
		cancel:   cancel,
		done:     make(chan error, 1),
	}
	go func() {
		target.done <- server.Serve(serveCtx, nil)
	}()
	return target, nil
}

// Addr returns the loopback address accepted by smtpclient.
func (t *Target) Addr() string {
	if t == nil || t.listener == nil {
		return ""
	}
	return t.listener.Addr().String()
}

// Sink returns the message-retrieval side of this target.
func (t *Target) Sink() *Sink {
	if t == nil {
		return nil
	}
	return t.sink
}

// Stop shuts down the server and waits for its Serve loop. It is idempotent.
func (t *Target) Stop(ctx context.Context) error {
	if ctx == nil {
		panic("gosmtp: nil context")
	}
	if t == nil {
		return nil
	}
	t.stopOnce.Do(func() {
		shutdownErr := t.server.Shutdown(ctx, nil)
		t.cancel()
		select {
		case serveErr := <-t.done:
			if serveErr != nil && !errors.Is(serveErr, context.Canceled) && !errors.Is(serveErr, context.DeadlineExceeded) {
				t.stopErr = fmt.Errorf("gosmtp: serve: %w", serveErr)
			}
		case <-ctx.Done():
			t.stopErr = ctx.Err()
		}
		if shutdownErr != nil {
			if t.stopErr == nil {
				t.stopErr = shutdownErr
			} else {
				t.stopErr = errors.Join(t.stopErr, shutdownErr)
			}
		}
	})
	return t.stopErr
}
