package smtpserver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"

	"github.com/kiliant/go-smtp/internal/smtpwire"
)

var errTLSConfigRequired = errors.New("smtpserver: TLS configuration is required")

// handshakeTLS performs both explicit and implicit server-side handshakes.
// For STARTTLS, plaintext is the retired SMTP decoder; only bytes that decoder
// already prefetched are discarded. For implicit TLS it is nil. The returned
// connection is the only reader the caller may use after success.
func handshakeTLS(
	ctx context.Context,
	conn net.Conn,
	config *tls.Config,
	plaintext *smtpwire.LineReader,
	reportPrefetch func(int),
) (*tls.Conn, error) {
	if config == nil {
		return nil, errTLSConfigRequired
	}
	if plaintext != nil {
		if discarded := plaintext.DiscardBuffered(); discarded > 0 && reportPrefetch != nil {
			reportPrefetch(discarded)
		}
	}
	tlsConn := tls.Server(conn, config)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("smtpserver: TLS handshake: %w", err)
	}
	return tlsConn, nil
}
