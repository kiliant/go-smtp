package smtpdeliver

import (
	"context"
	"errors"
	"fmt"
)

var (
	errTLSALookup     = errors.New("smtpdeliver: TLSA lookup failed; the MX is unreachable (RFC 7672 §2.1.2)")
	errDANEMandatory  = errors.New("smtpdeliver: mandatory DANE but no usable secure TLSA records (RFC 7672 §6)")
	errDANEInsecureMX = errors.New("smtpdeliver: mandatory DANE but the MX lookup is not DNSSEC-secure (RFC 7672 §2.2.1)")
)

// daneState says how RFC 7672 applies to one MX host.
type daneState int

const (
	// daneNone: no secure TLSA records; DANE does not apply (RFC 7672 §2.2).
	daneNone daneState = iota
	// daneUnusable: a secure TLSA RRset exists but none of its records is
	// usable. TLS is required, authentication is not (RFC 7672 §2.2).
	daneUnusable
	// daneUsable: secure usable TLSA records; TLS and DANE authentication are
	// required (RFC 7672 §3.2).
	daneUsable
)

// daneResult is the RFC 7672 evaluation of one MX host.
type daneResult struct {
	state daneState
	// baseDomain is the TLSA base domain, the primary DANE-TA reference
	// identifier (RFC 7672 §3.2.2).
	baseDomain string
	// records are the usable records of a secure TLSA RRset.
	records []TLSA
	// refIDs are every DANE-TA reference identifier, baseDomain first.
	refIDs []string
	// err is a lookup failure: the MX must be treated as unreachable.
	err error
}

// lookupDANE performs RFC 7672 §2.2 TLS discovery for one address step of
// domain. DANE applies only to MX hosts obtained securely and whose address
// records are secure; TLSA is not even queried otherwise (§2.2.2), which
// avoids broken nameservers delaying mail for unsigned zones. A non-nil
// error means the caller's context ended.
func (d *Deliverer) lookupDANE(ctx context.Context, domain string, step routeStep) (daneResult, error) {
	if !d.dane || step.host.security != DNSSECSecure || step.addrSecurity != DNSSECSecure {
		return daneResult{state: daneNone}, nil
	}
	// §2.2.3: a securely expanded alias is tried first, then the name itself.
	// Intermediate alias names are never candidates.
	candidates := []string{step.host.name}
	if step.addrCanonical != "" {
		candidates = []string{step.addrCanonical, step.host.name}
	}
	for _, base := range candidates {
		lookupCtx, cancel := context.WithTimeout(ctx, d.timeouts.DNS)
		answer, err := d.resolver.LookupTLSA(lookupCtx, &LookupTLSARequest{Name: fmt.Sprintf("_%d._tcp.%s", smtpPort, base)})
		cancel()
		d.emit(Event{Kind: EventLookup, Domain: domain, MX: step.host.name, Cause: err})
		if ctxErr := ctx.Err(); ctxErr != nil {
			return daneResult{}, ctxErr
		}
		if err != nil {
			return daneResult{err: fmt.Errorf("%w: %s: %w", errTLSALookup, base, err)}, nil
		}
		switch answer.Security {
		case DNSSECSecure:
		case DNSSECInsecure:
			continue // §2.2.3: insecure records → try the next candidate
		default:
			// Bogus, indeterminate, unvalidated or unknown: a lookup failure,
			// never permission to fall back (§2.1.2).
			return daneResult{err: fmt.Errorf("%w: %s: DNSSEC state %q", errTLSALookup, base, answer.Security)}, nil
		}
		if answer.State != LookupFound && answer.State != LookupNotFound {
			return daneResult{err: fmt.Errorf("%w: %s: unknown state %q", errTLSALookup, base, answer.State)}, nil
		}
		if answer.State == LookupNotFound || len(answer.Records) == 0 {
			continue // authenticated denial of existence
		}
		result := daneResult{state: daneUnusable, baseDomain: base, refIDs: daneReferenceIDs(domain, base, step)}
		for _, r := range answer.Records {
			if usableTLSA(r) {
				r.Association = append([]byte(nil), r.Association...)
				result.records = append(result.records, r)
			}
		}
		if len(result.records) > 0 {
			result.state = daneUsable
		}
		return result, nil
	}
	return daneResult{state: daneNone}, nil
}

// daneReferenceIDs lists the DANE-TA reference identifiers of RFC 7672
// §3.2.2: the TLSA base domain, then the original next-hop domain, then its
// CNAME expansion when different. lookupDANE runs only for a secure MX
// lookup, so the next-hop names always qualify.
func daneReferenceIDs(domain, base string, step routeStep) []string {
	ids := []string{base}
	add := func(name string) {
		if name == "" {
			return
		}
		for _, existing := range ids {
			if existing == name {
				return
			}
		}
		ids = append(ids, name)
	}
	add(domain)
	add(step.host.nextHopCanonical)
	return ids
}

// usableTLSA reports whether r is a record RFC 7672 SMTP clients use:
// DANE-TA(2) or DANE-EE(3) (§3.1), selector Cert(0) or SPKI(1), matching type
// Full(0), SHA2-256(1) or SHA2-512(2) (RFC 6698 §2.1), with association data
// of the length its matching type implies. Anything else, including the PKIX
// usages, is unusable rather than an error (§3.1.3).
func usableTLSA(r TLSA) bool {
	if r.Usage != 2 && r.Usage != 3 {
		return false
	}
	if r.Selector != 0 && r.Selector != 1 {
		return false
	}
	switch r.MatchingType {
	case 0:
		return len(r.Association) > 0
	case 1:
		return len(r.Association) == 32
	case 2:
		return len(r.Association) == 64
	}
	return false
}
