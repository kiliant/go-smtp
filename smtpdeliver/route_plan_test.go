package smtpdeliver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"
)

func addrs(list ...string) []netip.Addr {
	out := make([]netip.Addr, len(list))
	for i, s := range list {
		out[i] = netip.MustParseAddr(s)
	}
	return out
}

func found(list ...string) IPLookup {
	return IPLookup{State: LookupFound, Addresses: addrs(list...), Security: DNSSECInsecure}
}

func mxs(pairs ...any) MXLookup {
	out := MXLookup{State: LookupFound, Security: DNSSECInsecure}
	for i := 0; i < len(pairs); i += 2 {
		out.Records = append(out.Records, MX{Preference: uint16(pairs[i].(int)), Host: pairs[i+1].(string)})
	}
	return out
}

// newTestPlanner builds a Deliverer over f with a deterministic shuffle that
// keeps each equal-preference group in name order.
func newTestPlanner(t *testing.T, f *fakeResolver, mutate func(*Options)) *routePlanner {
	t.Helper()
	opts := &Options{Resolver: f.resolver(), LocalNames: []string{"mx.sender.test"}, LocalAddresses: addrs("198.51.100.25")}
	if mutate != nil {
		mutate(opts)
	}
	d, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	p := d.newRoutePlanner()
	p.shuffle = func([]routeHost) {}
	return p
}

// describe renders a plan as "pref:host:addr" or "pref:host:skip" entries.
func describe(plan routePlan) []string {
	var out []string
	for _, s := range plan.steps {
		if s.skipped() {
			out = append(out, fmt.Sprintf("%d:%s:skip", s.host.pref, s.host.name))
		} else {
			out = append(out, fmt.Sprintf("%d:%s:%s", s.host.pref, s.host.name, s.addr))
		}
	}
	return out
}

func TestRoutePlanDestinationOutcomes(t *testing.T) {
	tempErr := errors.New("SERVFAIL")
	cases := []struct {
		name        string
		setup       func(f *fakeResolver)
		mutate      func(*Options)
		disposition Disposition
		status      string
		cause       error
		noIPQueries bool
	}{
		{"NXDOMAIN is permanent", func(f *fakeResolver) {}, nil, DispositionPermanent, "5.1.2", errDomainNotFound, true},
		{"null MX is permanent and never falls through to A/AAAA", func(f *fakeResolver) {
			f.setMX("example.com", mxs(0, "."), nil)
			f.setIP("example.com", found("192.0.2.1"), nil)
		}, nil, DispositionPermanent, "5.1.10", errNullMX, true},
		{"null MX spelled empty", func(f *fakeResolver) { f.setMX("example.com", mxs(0, ""), nil) }, nil, DispositionPermanent, "5.1.10", errNullMX, true},
		{"null MX mixed with real MX is temporary", func(f *fakeResolver) {
			f.setMX("example.com", mxs(0, ".", 10, "mx.example.com"), nil)
			f.setIP("mx.example.com", found("192.0.2.1"), nil)
		}, nil, DispositionTemporary, "4.4.4", errNullMXMixed, true},
		{"null MX with nonzero preference is temporary", func(f *fakeResolver) { f.setMX("example.com", mxs(10, "."), nil) }, nil, DispositionTemporary, "4.4.4", errNullMXMixed, true},
		{"MX lookup failure is temporary", func(f *fakeResolver) { f.setMX("example.com", MXLookup{}, tempErr) }, nil, DispositionTemporary, "4.4.3", tempErr, true},
		{"bogus MX is temporary", func(f *fakeResolver) {
			a := mxs(10, "mx.example.com")
			a.Security = DNSSECBogus
			f.setMX("example.com", a, nil)
		}, nil, DispositionTemporary, "4.4.3", errDNSSECFailure, true},
		{"indeterminate MX is temporary", func(f *fakeResolver) {
			a := mxs(10, "mx.example.com")
			a.Security = DNSSECIndeterminate
			f.setMX("example.com", a, nil)
		}, nil, DispositionTemporary, "4.4.3", errDNSSECFailure, true},
		{"unvalidated MX under DANE is temporary", func(f *fakeResolver) {
			a := mxs(10, "mx.example.com")
			a.Security = DNSSECUnvalidated
			f.setMX("example.com", a, nil)
		}, func(o *Options) { o.DANE = &DANEOptions{} }, DispositionTemporary, "4.4.3", errDNSSECFailure, true},
		{"unknown DNSSEC state under DANE is temporary", func(f *fakeResolver) {
			a := mxs(10, "mx.example.com")
			a.Security = "half-secure"
			f.setMX("example.com", a, nil)
		}, func(o *Options) { o.DANE = &DANEOptions{} }, DispositionTemporary, "4.4.3", errDNSSECFailure, true},
		{"unknown lookup state is temporary", func(f *fakeResolver) {
			f.setMX("example.com", MXLookup{State: "refused", Security: DNSSECInsecure}, nil)
		}, nil, DispositionTemporary, "4.4.3", nil, true},
		{"implicit MX without addresses is permanent", func(f *fakeResolver) {
			f.setMX("example.com", MXLookup{State: LookupFound, Security: DNSSECInsecure}, nil)
		}, nil, DispositionPermanent, "5.1.2", errNoUsableMX, false},
		{"every MX without addresses is permanent", func(f *fakeResolver) {
			f.setMX("example.com", mxs(10, "a.example.com", 20, "b.example.com"), nil)
		}, nil, DispositionPermanent, "5.4.4", errNoUsableMX, false},
		{"every MX target invalid is permanent", func(f *fakeResolver) {
			f.setMX("example.com", mxs(10, "192.0.2.1", 20, "bad_host.example"), nil)
		}, nil, DispositionPermanent, "5.4.4", errNoUsableMX, true},
		{"address lookup failures make the destination temporary", func(f *fakeResolver) {
			f.setMX("example.com", mxs(10, "a.example.com", 20, "b.example.com"), nil)
			f.setIP("a.example.com", IPLookup{}, tempErr)
		}, nil, DispositionTemporary, "4.4.3", tempErr, false},
		{"best MX is this sender by name: routing loop", func(f *fakeResolver) {
			f.setMX("example.com", mxs(10, "MX.Sender.Test.", 20, "backup.example.com"), nil)
			f.setIP("backup.example.com", found("192.0.2.2"), nil)
		}, nil, DispositionPermanent, "5.4.6", errRoutingLoop, true},
		{"best MX is this sender by address: routing loop", func(f *fakeResolver) {
			f.setMX("example.com", mxs(10, "alias.example.com", 20, "backup.example.com"), nil)
			f.setIP("alias.example.com", found("198.51.100.25"), nil)
			f.setIP("backup.example.com", found("192.0.2.2"), nil)
		}, nil, DispositionPermanent, "5.4.6", errRoutingLoop, false},
		{"loopback MX counts as this sender", func(f *fakeResolver) {
			f.setMX("example.com", mxs(10, "localhost.example.com"), nil)
			f.setIP("localhost.example.com", found("127.0.0.1"), nil)
		}, nil, DispositionPermanent, "5.4.6", errRoutingLoop, false},
		{"implicit MX naming this sender is a loop", func(f *fakeResolver) {
			f.setMX("mx.sender.test", MXLookup{State: LookupFound, Security: DNSSECInsecure}, nil)
		}, nil, DispositionPermanent, "5.4.6", errRoutingLoop, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeResolver(DNSSECInsecure)
			tc.setup(f)
			p := newTestPlanner(t, f, func(o *Options) {
				if tc.mutate != nil {
					tc.mutate(o)
					o.Resolver = f.resolver()
				}
			})
			domain := "example.com"
			if strings.Contains(tc.name, "implicit MX naming") {
				domain = "mx.sender.test"
			}
			plan, failure, err := p.plan(context.Background(), domain)
			if err != nil {
				t.Fatal(err)
			}
			if failure == nil {
				t.Fatalf("plan = %v, want a %s failure", describe(plan), tc.disposition)
			}
			if failure.disposition != tc.disposition || failure.status.Raw != tc.status {
				t.Errorf("failure = %s %s (%v), want %s %s", failure.disposition, failure.status.Raw, failure.cause, tc.disposition, tc.status)
			}
			if tc.cause != nil && !errors.Is(failure, tc.cause) {
				t.Errorf("cause = %v, want %v in its chain", failure.cause, tc.cause)
			}
			if tc.noIPQueries {
				for _, q := range f.queried() {
					if strings.HasPrefix(q, "IP ") {
						t.Errorf("queried %q; this outcome must be decided without address lookups", q)
					}
				}
			}
		})
	}
}

func TestRoutePlanSteps(t *testing.T) {
	tempErr := errors.New("SERVFAIL")
	cases := []struct {
		name   string
		setup  func(f *fakeResolver)
		mutate func(*Options)
		want   []string
	}{
		{"implicit MX uses the domain's own addresses", func(f *fakeResolver) {
			f.setMX("example.com", MXLookup{State: LookupFound, Security: DNSSECInsecure}, nil)
			f.setIP("example.com", found("192.0.2.1", "2001:db8::1"), nil)
		}, nil, []string{"0:example.com:192.0.2.1", "0:example.com:2001:db8::1"}},
		{"ascending preference, every address of one MX before the next", func(f *fakeResolver) {
			f.setMX("example.com", mxs(20, "b.example.com", 10, "a.example.com", 30, "c.example.com"), nil)
			f.setIP("a.example.com", found("192.0.2.1", "192.0.2.2"), nil)
			f.setIP("b.example.com", found("192.0.2.3"), nil)
			f.setIP("c.example.com", found("192.0.2.4"), nil)
		}, nil, []string{"10:a.example.com:192.0.2.1", "10:a.example.com:192.0.2.2", "20:b.example.com:192.0.2.3", "30:c.example.com:192.0.2.4"}},
		{"resolver address order is kept", func(f *fakeResolver) {
			f.setMX("example.com", mxs(10, "a.example.com"), nil)
			f.setIP("a.example.com", found("2001:db8::9", "192.0.2.9", "192.0.2.1"), nil)
		}, nil, []string{"10:a.example.com:2001:db8::9", "10:a.example.com:192.0.2.9", "10:a.example.com:192.0.2.1"}},
		{"duplicate MX keeps its best preference", func(f *fakeResolver) {
			f.setMX("example.com", mxs(30, "a.example.com", 10, "A.example.com.", 20, "b.example.com"), nil)
			f.setIP("a.example.com", found("192.0.2.1"), nil)
			f.setIP("b.example.com", found("192.0.2.2"), nil)
		}, nil, []string{"10:a.example.com:192.0.2.1", "20:b.example.com:192.0.2.2"}},
		{"unusable and duplicate addresses are dropped, mapped ones unmapped", func(f *fakeResolver) {
			f.setMX("example.com", mxs(10, "a.example.com"), nil)
			f.setIP("a.example.com", found("0.0.0.0", "::", "224.0.0.1", "::ffff:192.0.2.1", "192.0.2.1"), nil)
		}, nil, []string{"10:a.example.com:192.0.2.1"}},
		{"failed address lookup skips only that MX", func(f *fakeResolver) {
			f.setMX("example.com", mxs(10, "a.example.com", 20, "b.example.com"), nil)
			f.setIP("a.example.com", IPLookup{}, tempErr)
			f.setIP("b.example.com", found("192.0.2.2"), nil)
		}, nil, []string{"10:a.example.com:skip", "20:b.example.com:192.0.2.2"}},
		{"bogus address answer skips only that MX", func(f *fakeResolver) {
			f.setMX("example.com", mxs(10, "a.example.com", 20, "b.example.com"), nil)
			bad := found("192.0.2.1")
			bad.Security = DNSSECBogus
			f.setIP("a.example.com", bad, nil)
			f.setIP("b.example.com", found("192.0.2.2"), nil)
		}, nil, []string{"10:a.example.com:skip", "20:b.example.com:192.0.2.2"}},
		{"invalid MX target is skipped, not fatal", func(f *fakeResolver) {
			f.setMX("example.com", mxs(10, "192.0.2.7", 20, "b.example.com"), nil)
			f.setIP("b.example.com", found("192.0.2.2"), nil)
		}, nil, []string{"20:b.example.com:192.0.2.2"}},
		{"unvalidated answers without DANE are ordinary DNS", func(f *fakeResolver) {
			a := mxs(10, "a.example.com")
			a.Security = DNSSECUnvalidated
			f.setMX("example.com", a, nil)
			ip := found("192.0.2.1")
			ip.Security = DNSSECUnvalidated
			f.setIP("a.example.com", ip, nil)
		}, nil, []string{"10:a.example.com:192.0.2.1"}},
		{"insecure MX under DANE proceeds", func(f *fakeResolver) {
			f.setMX("example.com", mxs(10, "a.example.com"), nil)
			f.setIP("a.example.com", found("192.0.2.1"), nil)
		}, func(o *Options) { o.DANE = &DANEOptions{} }, []string{"10:a.example.com:192.0.2.1"}},
		{"loop by name discards its preference and worse", func(f *fakeResolver) {
			f.setMX("example.com", mxs(5, "first.example.com", 10, "mx.sender.test", 10, "peer.example.com", 20, "backup.example.com"), nil)
			f.setIP("first.example.com", found("192.0.2.1"), nil)
			f.setIP("peer.example.com", found("192.0.2.2"), nil)
			f.setIP("backup.example.com", found("192.0.2.3"), nil)
		}, nil, []string{"5:first.example.com:192.0.2.1"}},
		{"loop by address discards its preference and worse", func(f *fakeResolver) {
			f.setMX("example.com", mxs(5, "first.example.com", 10, "alias.example.com", 10, "peer.example.com", 20, "backup.example.com"), nil)
			f.setIP("first.example.com", found("192.0.2.1"), nil)
			f.setIP("alias.example.com", found("192.0.2.9", "198.51.100.25"), nil)
			f.setIP("peer.example.com", found("192.0.2.2"), nil)
			f.setIP("backup.example.com", found("192.0.2.3"), nil)
		}, nil, []string{"5:first.example.com:192.0.2.1"}},
		{"loop elimination disabled keeps loopback", func(f *fakeResolver) {
			f.setMX("example.com", mxs(10, "local.example.com"), nil)
			f.setIP("local.example.com", found("127.0.0.1"), nil)
		}, func(o *Options) { o.LocalNames = nil; o.LocalAddresses = nil; o.DisableLoopElimination = true }, []string{"10:local.example.com:127.0.0.1"}},
		{"address cap spans hosts; skipped hosts do not count", func(f *fakeResolver) {
			f.setMX("example.com", mxs(10, "a.example.com", 20, "b.example.com", 30, "c.example.com"), nil)
			f.setIP("a.example.com", found("192.0.2.1", "192.0.2.2"), nil)
			f.setIP("b.example.com", IPLookup{}, tempErr)
			f.setIP("c.example.com", found("192.0.2.3", "192.0.2.4"), nil)
		}, func(o *Options) { o.MaxAddresses = 3 }, []string{"10:a.example.com:192.0.2.1", "10:a.example.com:192.0.2.2", "20:b.example.com:skip", "30:c.example.com:192.0.2.3"}},
		{"single address by explicit opt-in", func(f *fakeResolver) {
			f.setMX("example.com", mxs(10, "a.example.com", 20, "b.example.com"), nil)
			f.setIP("a.example.com", found("192.0.2.1", "192.0.2.2"), nil)
			f.setIP("b.example.com", found("192.0.2.3"), nil)
		}, func(o *Options) { o.MaxAddresses = 1; o.AllowSingleAddress = true }, []string{"10:a.example.com:192.0.2.1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeResolver(DNSSECInsecure)
			tc.setup(f)
			p := newTestPlanner(t, f, func(o *Options) {
				if tc.mutate != nil {
					tc.mutate(o)
					o.Resolver = f.resolver()
				}
			})
			plan, failure, err := p.plan(context.Background(), "example.com")
			if err != nil || failure != nil {
				t.Fatalf("plan failed: %v %v", failure, err)
			}
			if got := describe(plan); !slices.Equal(got, tc.want) {
				t.Errorf("plan = %v\nwant   %v", got, tc.want)
			}
		})
	}
}

func TestRoutePlanLoopByNameSkipsDiscardedLookups(t *testing.T) {
	f := newFakeResolver(DNSSECInsecure)
	f.setMX("example.com", mxs(5, "first.example.com", 10, "mx.sender.test", 20, "backup.example.com"), nil)
	f.setIP("first.example.com", found("192.0.2.1"), nil)
	p := newTestPlanner(t, f, nil)
	if _, failure, err := p.plan(context.Background(), "example.com"); err != nil || failure != nil {
		t.Fatal(failure, err)
	}
	for _, q := range f.queried() {
		if q == "IP backup.example.com" || q == "IP mx.sender.test" {
			t.Errorf("queried %q, which loop elimination had already discarded", q)
		}
	}
}

// TestRoutePlanShufflesEqualPreference checks the default shuffle: an
// equal-preference group must come out in more than one order (RFC 5321
// §5.1), and preference groups must never mix.
func TestRoutePlanShufflesEqualPreference(t *testing.T) {
	f := newFakeResolver(DNSSECInsecure)
	f.setMX("example.com", mxs(10, "a.example.com", 10, "b.example.com", 10, "c.example.com", 20, "z.example.com"), nil)
	for _, h := range []string{"a", "b", "c", "z"} {
		f.setIP(h+".example.com", found("192.0.2.1"), nil)
	}
	d, err := New(&Options{Resolver: f.resolver(), DisableLoopElimination: true})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for range 200 {
		plan, failure, err := d.newRoutePlanner().plan(context.Background(), "example.com")
		if err != nil || failure != nil {
			t.Fatal(failure, err)
		}
		var names []string
		for _, s := range plan.steps {
			names = append(names, s.host.name)
		}
		if names[3] != "z.example.com" {
			t.Fatalf("order %v mixes preference groups", names)
		}
		seen[strings.Join(names[:3], ",")] = true
	}
	if len(seen) < 2 {
		t.Errorf("equal-preference group came out in only %d order(s) over 200 plans: %v", len(seen), seen)
	}
}

func TestRoutePlanHonoursContext(t *testing.T) {
	f := newFakeResolver(DNSSECInsecure)
	f.setMX("example.com", mxs(10, "a.example.com"), nil)
	p := newTestPlanner(t, f, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, failure, err := p.plan(ctx, "example.com"); !errors.Is(err, context.Canceled) || failure != nil {
		t.Errorf("plan with cancelled context = %v, %v; want context.Canceled and no destination outcome", failure, err)
	}
}

func TestRoutePlanAppliesDNSTimeout(t *testing.T) {
	f := newFakeResolver(DNSSECInsecure)
	var deadline time.Time
	r := f.resolver()
	r.LookupMX = func(ctx context.Context, req *LookupMXRequest) (MXLookup, error) {
		deadline, _ = ctx.Deadline()
		return MXLookup{State: LookupNotFound, Security: DNSSECInsecure}, nil
	}
	d, err := New(&Options{Resolver: r, DisableLoopElimination: true, Timeouts: Timeouts{DNS: 3 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, _, _ = d.newRoutePlanner().plan(context.Background(), "example.com")
	if deadline.IsZero() || deadline.Sub(start) > 3*time.Second+time.Second {
		t.Errorf("MX lookup deadline = %v after start, want about Timeouts.DNS", deadline.Sub(start))
	}
}

func TestDialSeam(t *testing.T) {
	step := routeStep{host: routeHost{name: "mx.example.com", pref: 10}, addr: netip.MustParseAddr("192.0.2.1")}
	req := dialRequest("example.com", step)
	if req.Network != "tcp" || req.Address != netip.MustParseAddrPort("192.0.2.1:25") || req.MX != "mx.example.com" || req.Domain != "example.com" {
		t.Errorf("dialRequest = %+v", req)
	}
	if step.tlsServerName() != "mx.example.com" {
		t.Errorf("TLS identity = %q, want the unexpanded MX name, never the address", step.tlsServerName())
	}

	var events []Event
	var gotDeadline time.Time
	client, server := net.Pipe()
	defer server.Close()
	d, err := New(&Options{
		DisableLoopElimination: true,
		Timeouts:               Timeouts{Connect: 2 * time.Second},
		Trace:                  func(e Event) { events = append(events, e) },
		Dial: func(ctx context.Context, r *DialRequest) (net.Conn, error) {
			gotDeadline, _ = ctx.Deadline()
			return client, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	conn, err := d.connect(context.Background(), req)
	if err != nil || conn != client {
		t.Fatalf("connect = %v, %v", conn, err)
	}
	if gotDeadline.IsZero() || gotDeadline.Sub(start) > 3*time.Second {
		t.Errorf("Dial ctx deadline = %v after start, want Timeouts.Connect", gotDeadline.Sub(start))
	}
	if len(events) != 1 || events[0].Kind != EventConnect || events[0].Address != req.Address || events[0].Cause != nil {
		t.Errorf("events = %+v", events)
	}

	dialErr := errors.New("refused")
	closed := false
	half, other := net.Pipe()
	defer other.Close()
	d.dial = func(context.Context, *DialRequest) (net.Conn, error) {
		return &closeSpy{Conn: half, closed: &closed}, dialErr
	}
	if _, err := d.connect(context.Background(), req); !errors.Is(err, dialErr) || !closed {
		t.Errorf("connect with a failing hook = %v (closed=%v), want the hook's error and the stray conn closed", err, closed)
	}
	d.dial = func(context.Context, *DialRequest) (net.Conn, error) { return nil, nil }
	if _, err := d.connect(context.Background(), req); err == nil {
		t.Error("connect accepted a hook returning neither a connection nor an error")
	}
}

type closeSpy struct {
	net.Conn
	closed *bool
}

func (c *closeSpy) Close() error {
	*c.closed = true
	return c.Conn.Close()
}

func TestRoutePlanCarriesCanonicalNames(t *testing.T) {
	f := newFakeResolver(DNSSECSecure)
	mx := mxs(10, "mx.example.com", 20, "plain.example.com")
	mx.CanonicalName = "Mail.Example.NET."
	f.setMX("example.com", mx, nil)
	alias := found("192.0.2.1")
	alias.CanonicalName = "real.example.org"
	f.setIP("mx.example.com", alias, nil)
	same := found("192.0.2.2")
	same.CanonicalName = "plain.example.com."
	f.setIP("plain.example.com", same, nil)
	p := newTestPlanner(t, f, nil)
	plan, failure, err := p.plan(context.Background(), "example.com")
	if err != nil || failure != nil {
		t.Fatal(failure, err)
	}
	if s := plan.steps[0]; s.host.nextHopCanonical != "mail.example.net" || s.addrCanonical != "real.example.org" {
		t.Errorf("step 0 canonical names = %q / %q", s.host.nextHopCanonical, s.addrCanonical)
	}
	if s := plan.steps[1]; s.addrCanonical != "" {
		t.Errorf("an unchanged canonical name must be empty, got %q", s.addrCanonical)
	}
}
