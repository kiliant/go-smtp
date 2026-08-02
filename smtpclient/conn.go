package smtpclient

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kiliant/go-smtp/internal/smtpwire"
)

const (
	defaultGreetingTimeout    = 5 * time.Minute
	defaultMailTimeout        = 5 * time.Minute
	defaultDataCommandTimeout = 2 * time.Minute
	defaultDataBlockTimeout   = 3 * time.Minute
	defaultDataFinalTimeout   = 10 * time.Minute
)

type connection struct {
	mu     sync.Mutex // protects state, extensions, raw and reader
	opMu   sync.Mutex // makes every pipeline group atomic to a Client caller
	raw    net.Conn
	reader *smtpwire.LineReader
	state  sessionState
	// transactionBase is the reusable session state captured when MAIL opens a
	// transaction. Transaction commands restore it after RSET or a completed
	// DATA exchange, preserving whether the session is TLS-protected and/or
	// authenticated without expanding the public state model.
	transactionBase sessionState
	ext             map[string]string
	// recipients holds the accepted RCPT forward paths for the active SMTP
	// transaction. Transaction commands maintain it while holding mu; it is
	// intentionally connection state so DATA can produce the stable
	// per-recipient result shape without exposing protocol internals.
	recipients []string
	options    ClientOptions
	pipeline   pipeline
}

// Dial connects to opts.Address, reads the greeting, and negotiates ESMTP.
// It uses cleartext unless opts.ImplicitTLS is set.
func Dial(ctx context.Context, opts *ClientOptions) (*Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var cfg ClientOptions
	if opts != nil {
		cfg = *opts
	}
	if cfg.Address == "" {
		return nil, errors.New("smtpclient: ClientOptions.Address is required")
	}
	dial := cfg.DialContext
	if dial == nil {
		d := net.Dialer{}
		dial = d.DialContext
	}
	raw, err := dial(ctx, "tcp", cfg.Address)
	if err != nil {
		return nil, err
	}
	client, err := newClient(ctx, raw, cfg)
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	return client, nil
}

// NewClient starts an SMTP session on an established connection. It preserves
// connection injection for callers such as the future delivery layer. When
// opts.ImplicitTLS is set, conn is first wrapped in TLS before its greeting is
// read.
func NewClient(ctx context.Context, raw net.Conn, opts *ClientOptions) (*Client, error) {
	if raw == nil {
		return nil, errors.New("smtpclient: nil connection")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var cfg ClientOptions
	if opts != nil {
		cfg = *opts
	}
	client, err := newClient(ctx, raw, cfg)
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	return client, nil
}

func newClient(ctx context.Context, raw net.Conn, opts ClientOptions) (*Client, error) {
	c := &connection{
		raw:     raw,
		reader:  smtpwire.NewLineReader(raw),
		state:   stateConnected,
		ext:     make(map[string]string),
		options: opts,
	}
	c.pipeline.conn = c
	client := &Client{conn: c}

	if opts.ImplicitTLS {
		if err := c.upgradeTLS(ctx, opts.TLSConfig, opts.TLSServerName, opts.InsecureSkipVerify, c.greetingTimeout()); err != nil {
			return nil, err
		}
	}
	greeting, err := c.readGreeting(ctx)
	if err != nil {
		return nil, err
	}
	if err := unexpectedReply("greeting", greeting, false, 220); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if opts.ImplicitTLS {
		c.state = stateTLS
	} else {
		c.state = stateGreeted
	}
	c.mu.Unlock()
	if err := client.ehlo(ctx); err != nil {
		return nil, err
	}
	return client, nil
}

func (c *connection) identity() string {
	if c.options.Identity != "" {
		return c.options.Identity
	}
	if host, err := os.Hostname(); err == nil && host != "" {
		return host
	}
	return "localhost"
}

func (c *connection) timeout(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

func (c *connection) greetingTimeout() time.Duration {
	return c.timeout(c.options.GreetingTimeout, defaultGreetingTimeout)
}

func (c *connection) mailTimeout() time.Duration {
	return c.timeout(c.options.MailTimeout, defaultMailTimeout)
}

func (c *connection) rcptTimeout() time.Duration {
	return c.timeout(c.options.RCPTTimeout, defaultMailTimeout)
}

func (c *connection) dataCommandTimeout() time.Duration {
	return c.timeout(c.options.DataCommandTimeout, defaultDataCommandTimeout)
}

func (c *connection) dataBlockTimeout() time.Duration {
	return c.timeout(c.options.DataBlockTimeout, defaultDataBlockTimeout)
}

func (c *connection) dataFinalTimeout() time.Duration {
	return c.timeout(c.options.DataFinalTimeout, defaultDataFinalTimeout)
}

func (c *connection) readGreeting(ctx context.Context) (smtpwire.Reply, error) {
	c.opMu.Lock()
	defer c.opMu.Unlock()
	return c.pipeline.readOnly(ctx, "greeting", c.greetingTimeout())
}

func (c *connection) tlsConfig(base *tls.Config, serverName string, insecure bool) *tls.Config {
	if base == nil {
		base = c.options.TLSConfig
	}
	var cfg *tls.Config
	if base != nil {
		cfg = base.Clone()
	} else {
		cfg = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	if serverName == "" {
		serverName = c.options.TLSServerName
	}
	if serverName == "" {
		host, _, err := net.SplitHostPort(c.options.Address)
		if err == nil {
			serverName = strings.Trim(host, "[]")
		}
	}
	if cfg.ServerName == "" {
		cfg.ServerName = serverName
	}
	// Explicitly assigning the boolean makes the secure default visible even
	// when base was nil. A caller-supplied tls.Config may itself opt out.
	if insecure {
		cfg.InsecureSkipVerify = true // #nosec G402 -- explicit option only.
	}
	return cfg
}

func (c *connection) upgradeTLS(ctx context.Context, base *tls.Config, serverName string, insecure bool, timeout time.Duration) error {
	c.mu.Lock()
	raw := c.raw
	c.mu.Unlock()
	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	// TLS handshakes happen outside smtpwire's reply reader, so they need their
	// own deadline. In particular implicit TLS precedes the greeting read and
	// otherwise an unresponsive peer could hold NewClient forever.
	if err := raw.SetDeadline(deadline); err != nil {
		return transportError("TLS", err)
	}
	defer raw.SetDeadline(time.Time{})
	handshakeCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	tlsConn := tls.Client(raw, c.tlsConfig(base, serverName, insecure))
	stop := c.cancelWatcher(handshakeCtx)
	err := tlsConn.HandshakeContext(handshakeCtx)
	stop()
	if err != nil {
		if ctx.Err() != nil {
			c.poison()
			return ctx.Err()
		}
		return transportError("TLS", err)
	}
	c.mu.Lock()
	c.raw = tlsConn
	c.reader = smtpwire.NewLineReader(tlsConn)
	c.mu.Unlock()
	return nil
}

func (c *connection) cancelWatcher(ctx context.Context) func() {
	done := make(chan struct{})
	var stopped atomic.Bool
	go func() {
		select {
		case <-ctx.Done():
			if !stopped.Load() {
				c.poison()
			}
		case <-done:
		}
	}()
	return func() {
		stopped.Store(true)
		close(done)
	}
}

func (c *connection) poison() {
	c.mu.Lock()
	if c.state != stateClosed {
		c.state = stateClosed
		_ = c.raw.Close()
	}
	c.mu.Unlock()
}

func (c *connection) closed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state == stateClosed
}
