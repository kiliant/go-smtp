package smtpdeliver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
)

// dialRequest describes the connection for one address step. The dial target
// is the literal address on the RFC 5321 SMTP port; the MX host name travels
// separately as the TLS identity (API-STABILITY.md §9).
func dialRequest(domain string, step routeStep) *DialRequest {
	return &DialRequest{
		Network: "tcp",
		Address: netip.AddrPortFrom(step.addr, smtpPort),
		MX:      step.host.name,
		Domain:  domain,
	}
}

// tlsServerName is the certificate identity for an attempt on step: the
// unexpanded MX host name, never the dialled address (DELIVERY-DESIGN.md §4).
func (s routeStep) tlsServerName() string { return s.host.name }

// connect dials one address step through Options.Dial, bounded by
// Timeouts.Connect. The returned connection outlives the dial deadline.
func (d *Deliverer) connect(ctx context.Context, req *DialRequest) (net.Conn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, d.timeouts.Connect)
	defer cancel()
	conn, err := d.dial(dialCtx, req)
	d.emit(Event{Kind: EventConnect, Domain: req.Domain, MX: req.MX, Address: req.Address, Cause: err})
	if err != nil {
		if conn != nil {
			_ = conn.Close()
		}
		return nil, fmt.Errorf("smtpdeliver: connect to %s (%s): %w", req.Address, req.MX, err)
	}
	if conn == nil {
		return nil, errors.New("smtpdeliver: Dial returned neither a connection nor an error")
	}
	return conn, nil
}
