package smtpdeliver

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"slices"
	"strings"

	smtp "github.com/kiliant/go-smtp"
)

// Routing causes. They stay unexported: callers classify outcomes through
// Disposition and RecipientOutcome.Status, and read these only as text.
var (
	errDomainNotFound    = errors.New("smtpdeliver: domain does not exist (NXDOMAIN)")
	errNullMX            = errors.New("smtpdeliver: domain publishes null MX and accepts no mail (RFC 7505)")
	errNullMXMixed       = errors.New("smtpdeliver: null MX published together with other MX records (RFC 7505 §3)")
	errRoutingLoop       = errors.New("smtpdeliver: every remaining MX is this sender (RFC 5321 §5.1 loop)")
	errNoUsableMX        = errors.New("smtpdeliver: no MX host has a usable address (RFC 5321 §5.1)")
	errInvalidMXHost     = errors.New("smtpdeliver: MX target is not a valid host name")
	errNoAddresses       = errors.New("smtpdeliver: MX host has no addresses")
	errNoUsableAddresses = errors.New("smtpdeliver: MX host has no usable addresses")
	errDNSSECFailure     = errors.New("smtpdeliver: DNSSEC validation failed")
)

// Enhanced status codes for local routing outcomes, RFC 3463 §3.2 and §3.5
// and RFC 7505 §4.1.
var (
	statusDomainNotFound = smtp.ParseEnhancedCode("5.1.2")
	statusNullMX         = smtp.ParseEnhancedCode("5.1.10")
	statusRoutingLoop    = smtp.ParseEnhancedCode("5.4.6")
	statusNoRoute        = smtp.ParseEnhancedCode("5.4.4")
	statusNoRouteYet     = smtp.ParseEnhancedCode("4.4.4")
	statusDNSFailure     = smtp.ParseEnhancedCode("4.4.3")
)

// smtpPort is the RFC 5321 §4.5.4 relay port.
const smtpPort = 25

// routeHost is one MX target (RFC 5321 §5.1).
type routeHost struct {
	// name is the MX target as published, lower case without a trailing
	// dot: the TLS identity of every attempt on it, never its expansion.
	name     string
	pref     uint16
	implicit bool
	// security is the DNSSEC state of the MX lookup that produced the host.
	security DNSSECStatus
}

// routeStep is one entry of a route plan, in attempt order: either an address
// to try or an MX host that was skipped before any connection.
type routeStep struct {
	host routeHost
	// addr is the address to dial; the zero value marks a skipped host.
	addr netip.Addr
	// addrSecurity is the DNSSEC state of the address lookup.
	addrSecurity DNSSECStatus
	// skip says why the host was skipped. It is nil for an address step.
	skip error
}

func (s routeStep) skipped() bool { return !s.addr.IsValid() }

// routePlan is the ordered route for one destination.
type routePlan struct {
	steps []routeStep
}

// routeFailure is a destination-level outcome reached before any address
// could be tried. steps holds the hosts skipped on the way, for the record.
type routeFailure struct {
	disposition Disposition
	status      smtp.EnhancedCode
	cause       error
	steps       []routeStep
}

func (f *routeFailure) Error() string { return f.cause.Error() }
func (f *routeFailure) Unwrap() error { return f.cause }

// routePlanner turns a destination domain into a routePlan. shuffle orders
// one equal-preference group; tests replace it with a deterministic order.
type routePlanner struct {
	d       *Deliverer
	shuffle func(hosts []routeHost)
}

func (d *Deliverer) newRoutePlanner() *routePlanner {
	return &routePlanner{d: d, shuffle: shuffleHosts}
}

// shuffleHosts randomises one equal-preference group, as RFC 5321 §5.1
// requires. math/rand/v2's top-level source is seeded from the operating
// system's random source at process start.
func shuffleHosts(hosts []routeHost) {
	rand.Shuffle(len(hosts), func(i, j int) { hosts[i], hosts[j] = hosts[j], hosts[i] })
}

// plan resolves domain (already normalised) into an ordered route. A non-nil
// error means the caller's context ended; a non-nil *routeFailure is a
// destination outcome.
func (p *routePlanner) plan(ctx context.Context, domain string) (routePlan, *routeFailure, error) {
	hosts, failure, err := p.resolveMX(ctx, domain)
	if err != nil || failure != nil {
		return routePlan{}, failure, err
	}
	hosts = p.order(hosts)

	loopPref, loopByName := p.localPreference(hosts)
	if loopByName {
		hosts = keepBetterThan(hosts, loopPref)
		if len(hosts) == 0 {
			return routePlan{}, loopFailure(nil), nil
		}
	}

	var steps []routeStep
	for _, host := range hosts {
		hostSteps, err := p.resolveHost(ctx, domain, host)
		if err != nil {
			return routePlan{}, nil, err
		}
		steps = append(steps, hostSteps...)
	}

	if pref, local := p.localAddressPreference(steps); local {
		steps = keepStepsBetterThan(steps, pref)
		if len(steps) == 0 {
			return routePlan{}, loopFailure(nil), nil
		}
	}
	steps = p.capAddresses(steps)

	for _, step := range steps {
		if !step.skipped() {
			return routePlan{steps: steps}, nil, nil
		}
	}
	return routePlan{}, noAddressFailure(hosts, steps), nil
}

// resolveMX performs steps 1–3 of DELIVERY-DESIGN.md §4: MX lookup, NXDOMAIN,
// the implicit MX and null MX.
func (p *routePlanner) resolveMX(ctx context.Context, domain string) ([]routeHost, *routeFailure, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, p.d.timeouts.DNS)
	answer, err := p.d.resolver.LookupMX(lookupCtx, &LookupMXRequest{Name: domain})
	cancel()
	p.d.emit(Event{Kind: EventLookup, Domain: domain, Cause: err})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, nil, ctxErr
	}
	if err != nil {
		return nil, &routeFailure{disposition: DispositionTemporary, status: statusDNSFailure, cause: fmt.Errorf("smtpdeliver: MX lookup for %s: %w", domain, err)}, nil
	}
	if bad := p.dnssecProblem(answer.Security); bad != nil {
		// RFC 7672 §2.1.1: a failed MX validation delays the whole destination.
		return nil, &routeFailure{disposition: DispositionTemporary, status: statusDNSFailure, cause: fmt.Errorf("smtpdeliver: MX lookup for %s: %w", domain, bad)}, nil
	}
	switch answer.State {
	case LookupNotFound:
		return nil, &routeFailure{disposition: DispositionPermanent, status: statusDomainNotFound, cause: fmt.Errorf("%w: %s", errDomainNotFound, domain)}, nil
	case LookupFound:
	default:
		return nil, &routeFailure{disposition: DispositionTemporary, status: statusDNSFailure, cause: fmt.Errorf("smtpdeliver: MX lookup for %s returned unknown state %q", domain, answer.State)}, nil
	}

	if len(answer.Records) == 0 {
		// RFC 5321 §5.1: an empty MX list is an implicit MX of preference 0.
		return []routeHost{{name: domain, implicit: true, security: answer.Security}}, nil, nil
	}

	nullCount := 0
	for _, mx := range answer.Records {
		if isNullMXHost(mx.Host) {
			nullCount++
		}
	}
	if nullCount > 0 {
		if len(answer.Records) == 1 && answer.Records[0].Preference == 0 {
			return nil, &routeFailure{disposition: DispositionPermanent, status: statusNullMX, cause: fmt.Errorf("%w: %s", errNullMX, domain)}, nil
		}
		// A malformed null MX is a DNS configuration error. Guessing past it
		// could deliver mail the domain declared it does not accept.
		return nil, &routeFailure{disposition: DispositionTemporary, status: statusNoRouteYet, cause: fmt.Errorf("%w: %s", errNullMXMixed, domain)}, nil
	}

	best := map[string]routeHost{}
	var invalid []routeStep
	for _, mx := range answer.Records {
		name, err := normalizeDomain(mx.Host)
		if err != nil {
			invalid = append(invalid, routeStep{host: routeHost{name: strings.ToLower(mx.Host), pref: mx.Preference, security: answer.Security}, skip: fmt.Errorf("%w: %q: %v", errInvalidMXHost, mx.Host, err)})
			continue
		}
		if prev, ok := best[name]; !ok || mx.Preference < prev.pref {
			best[name] = routeHost{name: name, pref: mx.Preference, security: answer.Security}
		}
	}
	if len(best) == 0 {
		return nil, &routeFailure{disposition: DispositionPermanent, status: statusNoRoute, cause: fmt.Errorf("%w: %s", errNoUsableMX, domain), steps: invalid}, nil
	}
	hosts := make([]routeHost, 0, len(best))
	for _, h := range best {
		hosts = append(hosts, h)
	}
	return hosts, nil, nil
}

func isNullMXHost(host string) bool { return host == "" || host == "." }

// order sorts hosts by ascending preference and randomises each
// equal-preference group (RFC 5321 §5.1). The input order of map iteration is
// first made deterministic so a test's shuffle sees a stable group.
func (p *routePlanner) order(hosts []routeHost) []routeHost {
	slices.SortFunc(hosts, func(a, b routeHost) int {
		if a.pref != b.pref {
			return int(a.pref) - int(b.pref)
		}
		return strings.Compare(a.name, b.name)
	})
	for start := 0; start < len(hosts); {
		end := start + 1
		for end < len(hosts) && hosts[end].pref == hosts[start].pref {
			end++
		}
		p.shuffle(hosts[start:end])
		start = end
	}
	return hosts
}

// localPreference finds the best preference of an MX host named as this
// sender (RFC 5321 §5.1).
func (p *routePlanner) localPreference(hosts []routeHost) (uint16, bool) {
	if !p.d.loopElimination {
		return 0, false
	}
	for _, h := range hosts { // ascending preference
		if slices.Contains(p.d.localNames, h.name) {
			return h.pref, true
		}
	}
	return 0, false
}

// localAddressPreference finds the best preference of an MX host with an
// address belonging to this sender. Loopback addresses count as local: an MX
// pointing at 127.0.0.1 or ::1 would deliver to this host or to a local
// service, never to the destination.
func (p *routePlanner) localAddressPreference(steps []routeStep) (uint16, bool) {
	if !p.d.loopElimination {
		return 0, false
	}
	for _, s := range steps { // ascending preference
		if s.skipped() {
			continue
		}
		if s.addr.IsLoopback() || slices.Contains(p.d.localAddresses, s.addr) {
			return s.host.pref, true
		}
	}
	return 0, false
}

// keepBetterThan discards every host at preference pref or worse, the RFC
// 5321 §5.1 loop-elimination rule.
func keepBetterThan(hosts []routeHost, pref uint16) []routeHost {
	var out []routeHost
	for _, h := range hosts {
		if h.pref < pref {
			out = append(out, h)
		}
	}
	return out
}

func keepStepsBetterThan(steps []routeStep, pref uint16) []routeStep {
	var out []routeStep
	for _, s := range steps {
		if s.host.pref < pref {
			out = append(out, s)
		}
	}
	return out
}

// resolveHost performs step 6 of DELIVERY-DESIGN.md §4 for one host: its
// addresses in resolver order, or one skipped step explaining why there are
// none. A failure here skips only this host (RFC 7672 §2.1.2).
func (p *routePlanner) resolveHost(ctx context.Context, domain string, host routeHost) ([]routeStep, error) {
	lookupCtx, cancel := context.WithTimeout(ctx, p.d.timeouts.DNS)
	answer, err := p.d.resolver.LookupIP(lookupCtx, &LookupIPRequest{Name: host.name})
	cancel()
	p.d.emit(Event{Kind: EventLookup, Domain: domain, MX: host.name, Cause: err})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	skip := func(cause error) ([]routeStep, error) {
		return []routeStep{{host: host, addrSecurity: answer.Security, skip: cause}}, nil
	}
	if err != nil {
		return skip(&temporaryCause{fmt.Errorf("smtpdeliver: address lookup for %s: %w", host.name, err)})
	}
	if bad := p.dnssecProblem(answer.Security); bad != nil {
		return skip(&temporaryCause{fmt.Errorf("smtpdeliver: address lookup for %s: %w", host.name, bad)})
	}
	switch answer.State {
	case LookupFound, LookupNotFound:
	default:
		return skip(&temporaryCause{fmt.Errorf("smtpdeliver: address lookup for %s returned unknown state %q", host.name, answer.State)})
	}
	if answer.State == LookupNotFound || len(answer.Addresses) == 0 {
		return skip(fmt.Errorf("%w: %s", errNoAddresses, host.name))
	}
	var steps []routeStep
	seen := map[netip.Addr]bool{}
	for _, addr := range answer.Addresses {
		addr = addr.Unmap()
		if !usableAddress(addr) || seen[addr] {
			continue
		}
		seen[addr] = true
		steps = append(steps, routeStep{host: host, addr: addr, addrSecurity: answer.Security})
	}
	if len(steps) == 0 {
		return skip(fmt.Errorf("%w: %s", errNoUsableAddresses, host.name))
	}
	return steps, nil
}

// usableAddress rejects addresses no SMTP server can be reached at: invalid,
// unspecified (which dials the local host) and multicast addresses.
func usableAddress(a netip.Addr) bool {
	return a.IsValid() && !a.IsUnspecified() && !a.IsMulticast()
}

// capAddresses applies Options.MaxAddresses across the whole route. Skipped
// hosts stay in the record and do not count.
func (p *routePlanner) capAddresses(steps []routeStep) []routeStep {
	if p.d.maxAddresses == 0 {
		return steps
	}
	var out []routeStep
	n := 0
	for _, s := range steps {
		if !s.skipped() {
			if n == p.d.maxAddresses {
				continue
			}
			n++
		}
		out = append(out, s)
	}
	return out
}

// dnssecProblem reports a DNSSEC state that must be treated as a lookup
// failure. Bogus and indeterminate answers are never trusted (RFC 7672
// §2.1.1). With DANE enabled the resolver promised validation, so an
// unvalidated or unknown state is a failure too; without DANE an unvalidated
// answer, such as the standard library's, is ordinary DNS.
func (p *routePlanner) dnssecProblem(s DNSSECStatus) error {
	switch s {
	case DNSSECSecure, DNSSECInsecure:
		return nil
	case DNSSECBogus, DNSSECIndeterminate:
		return fmt.Errorf("%w: %s", errDNSSECFailure, s)
	default:
		if p.d.dane {
			return fmt.Errorf("%w: resolver reported %q under DANE", errDNSSECFailure, s)
		}
		return nil
	}
}

// temporaryCause marks a skipped host whose failure may clear on retry.
type temporaryCause struct{ err error }

func (t *temporaryCause) Error() string { return t.err.Error() }
func (t *temporaryCause) Unwrap() error { return t.err }

func loopFailure(steps []routeStep) *routeFailure {
	return &routeFailure{disposition: DispositionPermanent, status: statusRoutingLoop, cause: errRoutingLoop, steps: steps}
}

// noAddressFailure classifies a route in which every host was skipped. Any
// temporary skip makes the whole destination temporary. Otherwise the
// destination is permanently unroutable: for the implicit MX the domain has no
// address at all (RFC 5321 §5.1 "implicit MX is unusable").
func noAddressFailure(hosts []routeHost, steps []routeStep) *routeFailure {
	for _, s := range steps {
		var temp *temporaryCause
		if errors.As(s.skip, &temp) {
			return &routeFailure{disposition: DispositionTemporary, status: statusDNSFailure, cause: fmt.Errorf("%w: %w", errNoUsableMX, s.skip), steps: steps}
		}
	}
	status := statusNoRoute
	if len(hosts) == 1 && hosts[0].implicit {
		status = statusDomainNotFound
	}
	return &routeFailure{disposition: DispositionPermanent, status: status, cause: errNoUsableMX, steps: steps}
}

// emit sends e to Options.Trace, if set.
func (d *Deliverer) emit(e Event) {
	if d.trace != nil {
		d.trace(e)
	}
}
