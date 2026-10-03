package smtpdeliver

import (
	"context"
	"errors"
	"net"
	"strings"
)

// standardResolver adapts a *net.Resolver to Resolver for MX, IP and TXT
// lookups. It cannot validate DNSSEC, so every answer reports
// DNSSECUnvalidated, and it has no TLSA lookup.
//
// The standard library reports NXDOMAIN and an empty answer with the same
// IsNotFound error. For addresses and TXT both mean "nothing usable" and are
// reported as LookupNotFound. For MX the difference matters: NXDOMAIN is a
// permanent routing failure while an empty answer selects the RFC 5321 §5.1
// implicit MX. The adapter reports an empty MX answer, so a nonexistent domain
// reaches the implicit MX and then fails permanently at its address lookup
// instead of being misreported as having no MX.
//
// A lookup whose context ended is always an error, never an answer: an
// interrupted lookup must not be mistaken for an empty RRset and route to the
// implicit MX.
func standardResolver(r *net.Resolver) Resolver {
	return Resolver{
		LookupMX: func(ctx context.Context, req *LookupMXRequest) (MXLookup, error) {
			records, err := r.LookupMX(ctx, absolute(req.Name))
			if ctxErr := ctx.Err(); ctxErr != nil {
				return MXLookup{}, ctxErr
			}
			if isNotFound(err) {
				return MXLookup{State: LookupFound, Security: DNSSECUnvalidated}, nil
			}
			if err != nil && len(records) == 0 {
				return MXLookup{}, err
			}
			out := MXLookup{State: LookupFound, Security: DNSSECUnvalidated}
			for _, mx := range records {
				out.Records = append(out.Records, MX{Host: strings.TrimSuffix(mx.Host, "."), Preference: mx.Pref})
			}
			return out, nil
		},
		LookupIP: func(ctx context.Context, req *LookupIPRequest) (IPLookup, error) {
			addrs, err := r.LookupNetIP(ctx, "ip", absolute(req.Name))
			if ctxErr := ctx.Err(); ctxErr != nil {
				return IPLookup{}, ctxErr
			}
			if isNotFound(err) {
				return IPLookup{State: LookupNotFound, Security: DNSSECUnvalidated}, nil
			}
			if err != nil {
				return IPLookup{}, err
			}
			out := IPLookup{State: LookupFound, Security: DNSSECUnvalidated}
			for _, addr := range addrs {
				out.Addresses = append(out.Addresses, addr.Unmap())
			}
			return out, nil
		},
		LookupTXT: func(ctx context.Context, req *LookupTXTRequest) (TXTLookup, error) {
			records, err := r.LookupTXT(ctx, absolute(req.Name))
			if ctxErr := ctx.Err(); ctxErr != nil {
				return TXTLookup{}, ctxErr
			}
			if isNotFound(err) {
				return TXTLookup{State: LookupNotFound, Security: DNSSECUnvalidated}, nil
			}
			if err != nil {
				return TXTLookup{}, err
			}
			return TXTLookup{State: LookupFound, Records: records, Security: DNSSECUnvalidated}, nil
		},
	}
}

// absolute appends the root label so the resolver never applies search
// suffixes to a routing domain.
func absolute(name string) string {
	if strings.HasSuffix(name, ".") {
		return name
	}
	return name + "."
}

func isNotFound(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && dnsErr.IsNotFound
}
