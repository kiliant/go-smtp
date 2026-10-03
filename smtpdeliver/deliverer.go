package smtpdeliver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"time"
)

const (
	defaultMaxDestinations = 100
	defaultDNSTimeout      = 30 * time.Second
	defaultConnectTimeout  = 30 * time.Second
	// maxPolicyFetchTimeout is RFC 8461 §3.3's suggested upper bound for a
	// policy fetch, which DELIVERY-DESIGN.md §5 makes a hard ceiling.
	maxPolicyFetchTimeout = time.Minute
)

// Deliverer makes RFC 5321 delivery attempts with one immutable
// configuration. It owns MTA-STS (RFC 8461) cache coordination but no message
// queue and no connection pool: unless configured otherwise, every Deliver
// call dials, uses and closes its own connections.
//
// A Deliverer is safe for concurrent use. Callbacks (Resolver, PolicyCache,
// Dial, MessageSource.Open, Trace) may run concurrently across concurrent
// Deliver calls; within one call they run sequentially, destination by
// destination, unless an option enabling parallel destinations is set.
type Deliverer struct {
	resolver        Resolver
	cache           *PolicyCache
	mtasts          *mtastsState // non-nil when MTA-STS is enabled (T27)
	daneMode        DANEMode
	dane            bool
	dial            func(ctx context.Context, req *DialRequest) (net.Conn, error)
	httpClient      *http.Client
	tlsConfig       *tls.Config
	identity        string
	localNames      []string
	localAddresses  []netip.Addr
	loopElimination bool
	maxAddresses    int
	maxDestinations int
	timeouts        Timeouts
	trace           func(Event)
}

// New validates opts and returns a Deliverer for RFC 5321 delivery attempts.
// A nil opts means defaults; because RFC 5321 §5.1 loop elimination needs a
// local identity, New(nil) fails unless that is supplied. New copies or clones
// every caller value it keeps and never mutates opts.
func New(opts *Options) (*Deliverer, error) {
	if opts == nil {
		opts = &Options{}
	}
	d := &Deliverer{
		identity:        opts.Identity,
		loopElimination: !opts.DisableLoopElimination,
		trace:           opts.Trace,
	}

	if opts.DANE != nil {
		r := opts.Resolver
		if r.LookupMX == nil || r.LookupIP == nil || r.LookupTLSA == nil {
			return nil, errors.New("smtpdeliver: DANE requires Resolver.LookupMX, LookupIP and LookupTLSA from one DNSSEC-aware resolver")
		}
		switch mode := opts.DANE.Mode; mode {
		case "", DANEOpportunistic:
			d.daneMode = DANEOpportunistic
		case DANEMandatory, DANEAudit:
			d.daneMode = mode
		default:
			return nil, fmt.Errorf("smtpdeliver: unsupported DANE mode %q", mode)
		}
		d.dane = true
	}
	if opts.MTASTS != nil {
		cache := opts.MTASTS.Cache
		if cache.Load == nil || cache.Store == nil {
			return nil, errors.New("smtpdeliver: MTA-STS requires a PolicyCache with both Load and Store; there is no default cache")
		}
		d.cache = &cache
	}

	if opts.MaxAddresses < 0 {
		return nil, fmt.Errorf("smtpdeliver: negative MaxAddresses %d", opts.MaxAddresses)
	}
	if opts.MaxAddresses == 1 && !opts.AllowSingleAddress {
		return nil, errors.New("smtpdeliver: MaxAddresses of 1 forgoes RFC 5321 §5.1 failover; set AllowSingleAddress to choose that explicitly")
	}
	d.maxAddresses = opts.MaxAddresses
	switch {
	case opts.MaxDestinations < 0:
		return nil, fmt.Errorf("smtpdeliver: negative MaxDestinations %d", opts.MaxDestinations)
	case opts.MaxDestinations == 0:
		d.maxDestinations = defaultMaxDestinations
	default:
		d.maxDestinations = opts.MaxDestinations
	}

	timeouts, err := resolveTimeouts(opts.Timeouts)
	if err != nil {
		return nil, err
	}
	d.timeouts = timeouts

	for i, name := range opts.LocalNames {
		normalized, err := normalizeDomain(name)
		if err != nil {
			return nil, fmt.Errorf("smtpdeliver: LocalNames[%d]: %w", i, err)
		}
		d.localNames = append(d.localNames, normalized)
	}
	for i, addr := range opts.LocalAddresses {
		if !addr.IsValid() {
			return nil, fmt.Errorf("smtpdeliver: LocalAddresses[%d] is not a valid address", i)
		}
		d.localAddresses = append(d.localAddresses, addr.Unmap())
	}
	if d.loopElimination && len(d.localNames) == 0 && len(d.localAddresses) == 0 {
		return nil, errors.New("smtpdeliver: RFC 5321 §5.1 loop elimination needs LocalNames or LocalAddresses; set DisableLoopElimination only for tests or explicitly routed deployments")
	}

	d.resolver = opts.Resolver
	std := standardResolver(net.DefaultResolver)
	if d.resolver.LookupMX == nil {
		d.resolver.LookupMX = std.LookupMX
	}
	if d.resolver.LookupIP == nil {
		d.resolver.LookupIP = std.LookupIP
	}
	if d.resolver.LookupTXT == nil {
		d.resolver.LookupTXT = std.LookupTXT
	}

	d.dial = opts.Dial
	if d.dial == nil {
		dialer := &net.Dialer{Timeout: d.timeouts.Connect}
		d.dial = func(ctx context.Context, req *DialRequest) (net.Conn, error) {
			return dialer.DialContext(ctx, req.Network, req.Address.String())
		}
	}
	if opts.HTTPClient != nil {
		client := *opts.HTTPClient
		d.httpClient = &client
	} else {
		d.httpClient = &http.Client{}
	}
	if opts.TLSConfig != nil {
		d.tlsConfig = opts.TLSConfig.Clone()
	}
	if d.cache != nil {
		d.mtasts = newMTASTSState(d, *d.cache)
	}
	return d, nil
}

func resolveTimeouts(t Timeouts) (Timeouts, error) {
	if t.DNS < 0 || t.Connect < 0 || t.PolicyFetch < 0 {
		return Timeouts{}, errors.New("smtpdeliver: negative timeout")
	}
	if t.PolicyFetch > maxPolicyFetchTimeout {
		return Timeouts{}, fmt.Errorf("smtpdeliver: Timeouts.PolicyFetch %v exceeds the %v maximum", t.PolicyFetch, maxPolicyFetchTimeout)
	}
	if t.DNS == 0 {
		t.DNS = defaultDNSTimeout
	}
	if t.Connect == 0 {
		t.Connect = defaultConnectTimeout
	}
	if t.PolicyFetch == 0 {
		t.PolicyFetch = maxPolicyFetchTimeout
	}
	return t, nil
}
