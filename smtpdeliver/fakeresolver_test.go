package smtpdeliver

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
)

// fakeResolver is the shared scripted DNS for smtpdeliver tests. T25 owns its
// structure; T26–T30 append cases through the set* methods and must not
// delete another task's helpers (docs/tasks/BOARD.md, shared test
// infrastructure). It has no package-global state: each test builds its own.
//
// A name with no scripted answer is NXDOMAIN with the fake's default
// security, so a test states every name it expects to exist.
type fakeResolver struct {
	mu       sync.Mutex
	security DNSSECStatus
	mx       map[string]fakeAnswer[MXLookup]
	ip       map[string]fakeAnswer[IPLookup]
	txt      map[string]fakeAnswer[TXTLookup]
	tlsa     map[string]fakeAnswer[TLSALookup]
	queries  []string
}

type fakeAnswer[T any] struct {
	value T
	err   error
}

func newFakeResolver(security DNSSECStatus) *fakeResolver {
	return &fakeResolver{
		security: security,
		mx:       map[string]fakeAnswer[MXLookup]{},
		ip:       map[string]fakeAnswer[IPLookup]{},
		txt:      map[string]fakeAnswer[TXTLookup]{},
		tlsa:     map[string]fakeAnswer[TLSALookup]{},
	}
}

func (f *fakeResolver) setMX(name string, answer MXLookup, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mx[strings.ToLower(name)] = fakeAnswer[MXLookup]{answer, err}
}

func (f *fakeResolver) setIP(name string, answer IPLookup, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ip[strings.ToLower(name)] = fakeAnswer[IPLookup]{answer, err}
}

func (f *fakeResolver) setTXT(name string, answer TXTLookup, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.txt[strings.ToLower(name)] = fakeAnswer[TXTLookup]{answer, err}
}

func (f *fakeResolver) setTLSA(name string, answer TLSALookup, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tlsa[strings.ToLower(name)] = fakeAnswer[TLSALookup]{answer, err}
}

// queried returns the queries made so far, as "TYPE name".
func (f *fakeResolver) queried() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.queries...)
}

func fakeLookup[T any](f *fakeResolver, ctx context.Context, kind string, table map[string]fakeAnswer[T], name string, notFound T) (T, error) {
	f.mu.Lock()
	f.queries = append(f.queries, kind+" "+name)
	answer, ok := table[strings.ToLower(name)]
	f.mu.Unlock()
	if err := ctx.Err(); err != nil {
		var zero T
		return zero, err
	}
	if !ok {
		return notFound, nil
	}
	return answer.value, answer.err
}

// resolver returns all four lookups backed by f.
func (f *fakeResolver) resolver() Resolver {
	return Resolver{
		LookupMX: func(ctx context.Context, req *LookupMXRequest) (MXLookup, error) {
			return fakeLookup(f, ctx, "MX", f.mx, req.Name, MXLookup{State: LookupNotFound, Security: f.security})
		},
		LookupIP: func(ctx context.Context, req *LookupIPRequest) (IPLookup, error) {
			return fakeLookup(f, ctx, "IP", f.ip, req.Name, IPLookup{State: LookupNotFound, Security: f.security})
		},
		LookupTXT: func(ctx context.Context, req *LookupTXTRequest) (TXTLookup, error) {
			return fakeLookup(f, ctx, "TXT", f.txt, req.Name, TXTLookup{State: LookupNotFound, Security: f.security})
		},
		LookupTLSA: func(ctx context.Context, req *LookupTLSARequest) (TLSALookup, error) {
			return fakeLookup(f, ctx, "TLSA", f.tlsa, req.Name, TLSALookup{State: LookupNotFound, Security: f.security})
		},
	}
}

// TestFakeResolver pins the shared fake's contract before later tasks build on
// it: scripted answers are returned verbatim, names are case-insensitive,
// unscripted names are NXDOMAIN with the default security, and every query is
// recorded.
func TestFakeResolver(t *testing.T) {
	f := newFakeResolver(DNSSECInsecure)
	tempErr := errors.New("SERVFAIL")
	f.setMX("Example.COM", MXLookup{State: LookupFound, Records: []MX{{Host: "mx.example.com", Preference: 10}}, Security: DNSSECSecure}, nil)
	f.setIP("mx.example.com", IPLookup{State: LookupFound, Addresses: []netip.Addr{netip.MustParseAddr("192.0.2.1")}}, nil)
	f.setTXT("_mta-sts.example.com", TXTLookup{State: LookupFound, Records: []string{"v=STSv1; id=1"}}, nil)
	f.setTLSA("_25._tcp.mx.example.com", TLSALookup{}, tempErr)
	r := f.resolver()
	ctx := context.Background()

	mx, err := r.LookupMX(ctx, &LookupMXRequest{Name: "example.com"})
	if err != nil || mx.Security != DNSSECSecure || len(mx.Records) != 1 || mx.Records[0].Host != "mx.example.com" {
		t.Errorf("scripted MX = %+v, %v", mx, err)
	}
	if ip, err := r.LookupIP(ctx, &LookupIPRequest{Name: "mx.example.com"}); err != nil || len(ip.Addresses) != 1 {
		t.Errorf("scripted IP = %+v, %v", ip, err)
	}
	if txt, err := r.LookupTXT(ctx, &LookupTXTRequest{Name: "_mta-sts.example.com"}); err != nil || len(txt.Records) != 1 {
		t.Errorf("scripted TXT = %+v, %v", txt, err)
	}
	if _, err := r.LookupTLSA(ctx, &LookupTLSARequest{Name: "_25._tcp.mx.example.com"}); !errors.Is(err, tempErr) {
		t.Errorf("scripted TLSA error = %v, want %v", err, tempErr)
	}
	if missing, err := r.LookupMX(ctx, &LookupMXRequest{Name: "missing.example"}); err != nil || missing.State != LookupNotFound || missing.Security != DNSSECInsecure {
		t.Errorf("unscripted MX = %+v, %v; want NXDOMAIN with the default security", missing, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.LookupIP(cancelled, &LookupIPRequest{Name: "mx.example.com"}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled lookup = %v, want context.Canceled", err)
	}
	want := []string{"MX example.com", "IP mx.example.com", "TXT _mta-sts.example.com", "TLSA _25._tcp.mx.example.com", "MX missing.example", "IP mx.example.com"}
	if got := f.queried(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("queries = %v, want %v", got, want)
	}
}
