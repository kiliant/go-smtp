package smtpdeliver

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	smtp "github.com/kiliant/go-smtp"
)

func validCache() PolicyCache {
	return PolicyCache{
		Load: func(context.Context, *PolicyCacheLoadRequest) (PolicyCacheEntry, bool, error) {
			return PolicyCacheEntry{}, false, nil
		},
		Store: func(context.Context, *PolicyCacheStoreRequest) error { return nil },
	}
}

// baseOptions is the smallest configuration New accepts.
func baseOptions() *Options {
	return &Options{LocalNames: []string{"mx.sender.test"}}
}

func TestNewRejectsUnsafeConfiguration(t *testing.T) {
	full := newFakeResolver(DNSSECSecure).resolver()
	cases := []struct {
		name   string
		mutate func(*Options)
		want   string
	}{
		{"nil options lack a local identity", nil, "loop elimination"},
		{"no local identity", func(o *Options) { o.LocalNames = nil }, "loop elimination"},
		{"DANE without TLSA lookup", func(o *Options) {
			o.DANE = &DANEOptions{}
			o.Resolver = full
			o.Resolver.LookupTLSA = nil
		}, "DANE requires"},
		{"DANE without MX lookup", func(o *Options) {
			o.DANE = &DANEOptions{}
			o.Resolver = full
			o.Resolver.LookupMX = nil
		}, "DANE requires"},
		{"DANE without IP lookup", func(o *Options) {
			o.DANE = &DANEOptions{}
			o.Resolver = full
			o.Resolver.LookupIP = nil
		}, "DANE requires"},
		{"unknown DANE mode", func(o *Options) {
			o.DANE = &DANEOptions{Mode: "strict-ish"}
			o.Resolver = full
		}, "unsupported DANE mode"},
		{"MTA-STS without cache", func(o *Options) { o.MTASTS = &MTASTSOptions{} }, "PolicyCache"},
		{"MTA-STS without Store", func(o *Options) {
			c := validCache()
			c.Store = nil
			o.MTASTS = &MTASTSOptions{Cache: c}
		}, "PolicyCache"},
		{"single address without opt-in", func(o *Options) { o.MaxAddresses = 1 }, "AllowSingleAddress"},
		{"negative address cap", func(o *Options) { o.MaxAddresses = -1 }, "MaxAddresses"},
		{"negative destination cap", func(o *Options) { o.MaxDestinations = -1 }, "MaxDestinations"},
		{"policy fetch above one minute", func(o *Options) { o.Timeouts.PolicyFetch = 2 * time.Minute }, "PolicyFetch"},
		{"negative timeout", func(o *Options) { o.Timeouts.DNS = -time.Second }, "negative timeout"},
		{"invalid local name", func(o *Options) { o.LocalNames = []string{"bad_name"} }, "LocalNames[0]"},
		{"invalid local address", func(o *Options) {
			o.LocalNames = nil
			o.LocalAddresses = []netip.Addr{{}}
		}, "LocalAddresses[0]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var opts *Options
			if tc.mutate != nil {
				opts = baseOptions()
				tc.mutate(opts)
			}
			d, err := New(opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("New = %v, %v; want an error mentioning %q", d, err, tc.want)
			}
		})
	}
}

func TestNewAcceptsAndDefaults(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Options)
	}{
		{"local name", nil},
		{"local address only", func(o *Options) {
			o.LocalNames = nil
			o.LocalAddresses = []netip.Addr{netip.MustParseAddr("192.0.2.1")}
		}},
		{"loop elimination disabled", func(o *Options) { o.LocalNames = nil; o.DisableLoopElimination = true }},
		{"single address opted in", func(o *Options) { o.MaxAddresses = 1; o.AllowSingleAddress = true }},
		{"DANE opportunistic by default", func(o *Options) {
			o.DANE = &DANEOptions{}
			o.Resolver = newFakeResolver(DNSSECSecure).resolver()
		}},
		{"DANE mandatory", func(o *Options) {
			o.DANE = &DANEOptions{Mode: DANEMandatory}
			o.Resolver = newFakeResolver(DNSSECSecure).resolver()
		}},
		{"MTA-STS", func(o *Options) { o.MTASTS = &MTASTSOptions{Cache: validCache()} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := baseOptions()
			if tc.mutate != nil {
				tc.mutate(opts)
			}
			d, err := New(opts)
			if err != nil {
				t.Fatal(err)
			}
			if d.resolver.LookupMX == nil || d.resolver.LookupIP == nil || d.resolver.LookupTXT == nil || d.dial == nil || d.httpClient == nil {
				t.Error("New left a default unset")
			}
			if d.maxDestinations != defaultMaxDestinations || d.timeouts.DNS != defaultDNSTimeout || d.timeouts.Connect != defaultConnectTimeout || d.timeouts.PolicyFetch != maxPolicyFetchTimeout {
				t.Errorf("defaults = %d %+v", d.maxDestinations, d.timeouts)
			}
			if opts.DANE != nil && opts.DANE.Mode == "" && d.daneMode != DANEOpportunistic {
				t.Errorf("empty DANE mode resolved to %q, want opportunistic", d.daneMode)
			}
		})
	}
}

func TestNewDoesNotRetainCallerState(t *testing.T) {
	cfg := &tls.Config{ServerName: "caller"}
	client := &http.Client{Timeout: time.Second}
	names := []string{"MX.Sender.Test."}
	addrs := []netip.Addr{netip.MustParseAddr("::ffff:192.0.2.1")}
	cache := validCache()
	opts := &Options{TLSConfig: cfg, HTTPClient: client, LocalNames: names, LocalAddresses: addrs, MTASTS: &MTASTSOptions{Cache: cache}}
	d, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ServerName = "mutated"
	client.Timeout = time.Hour
	names[0] = "mutated.test"
	addrs[0] = netip.MustParseAddr("198.51.100.1")
	opts.MTASTS.Cache.Load = nil
	if d.tlsConfig == cfg || d.tlsConfig.ServerName != "caller" {
		t.Error("TLSConfig was not cloned")
	}
	if d.httpClient == client || d.httpClient.Timeout != time.Second {
		t.Error("HTTPClient was not copied")
	}
	if d.localNames[0] != "mx.sender.test" {
		t.Errorf("LocalNames = %v, want a normalised copy", d.localNames)
	}
	if d.localAddresses[0] != netip.MustParseAddr("192.0.2.1") {
		t.Errorf("LocalAddresses = %v, want an unmapped copy", d.localAddresses)
	}
	if d.cache.Load == nil {
		t.Error("PolicyCache was not copied")
	}
	if opts.Timeouts != (Timeouts{}) || opts.MaxDestinations != 0 {
		t.Error("New wrote defaults into the caller's Options")
	}
}

func stringSource(s string) MessageSource {
	return MessageSource{Open: func(context.Context, *OpenMessageOptions) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(s)), nil
	}}
}

func oneDestination(domain string, rcpts ...string) []Destination {
	var rs []Recipient
	for _, r := range rcpts {
		rs = append(rs, Recipient{Address: r})
	}
	return []Destination{{Domain: domain, Recipients: rs}}
}

func TestValidateRequest(t *testing.T) {
	d, err := New(&Options{LocalNames: []string{"mx.sender.test"}, MaxDestinations: 2})
	if err != nil {
		t.Fatal(err)
	}
	size := func(n int64) *int64 { return &n }
	valid := func() *Request {
		return &Request{Message: stringSource("x"), Destinations: oneDestination("example.com", "a@example.com")}
	}
	cases := []struct {
		name   string
		mutate func(*Request) *Request
		want   string
	}{
		{"nil request", func(*Request) *Request { return nil }, "nil Request"},
		{"no Open", func(r *Request) *Request { r.Message.Open = nil; return r }, "Open is nil"},
		{"negative size", func(r *Request) *Request { r.Message.Size = size(-1); return r }, "negative"},
		{"size disagreement", func(r *Request) *Request {
			r.Message.Size = size(10)
			r.MailOptions = &smtp.MailOptions{Transport: &smtp.TransportOptions{Size: size(11)}}
			return r
		}, "disagrees"},
		{"no destinations", func(r *Request) *Request { r.Destinations = nil; return r }, "no destinations"},
		{"too many destinations", func(r *Request) *Request {
			r.Destinations = append(oneDestination("a.test", "x@a.test"), append(oneDestination("b.test", "x@b.test"), oneDestination("c.test", "x@c.test")...)...)
			return r
		}, "limit of 2"},
		{"empty domain", func(r *Request) *Request { r.Destinations[0].Domain = ""; return r }, "empty domain"},
		{"root only", func(r *Request) *Request { r.Destinations[0].Domain = "."; return r }, "invalid length"},
		{"address literal", func(r *Request) *Request { r.Destinations[0].Domain = "[192.0.2.1]"; return r }, "address literal"},
		{"bare IP", func(r *Request) *Request { r.Destinations[0].Domain = "192.0.2.1"; return r }, "IP address"},
		{"unicode", func(r *Request) *Request { r.Destinations[0].Domain = "bücher.example"; return r }, "A-label"},
		{"underscore", func(r *Request) *Request { r.Destinations[0].Domain = "a_b.example"; return r }, "only ASCII"},
		{"empty label", func(r *Request) *Request { r.Destinations[0].Domain = "a..example"; return r }, "label"},
		{"long label", func(r *Request) *Request { r.Destinations[0].Domain = strings.Repeat("a", 64) + ".example"; return r }, "label"},
		{"leading hyphen", func(r *Request) *Request { r.Destinations[0].Domain = "-a.example"; return r }, "'-'"},
		{"duplicate domain", func(r *Request) *Request {
			r.Destinations = append(r.Destinations, oneDestination("EXAMPLE.com.", "b@example.com")...)
			return r
		}, "repeats domain"},
		{"no recipients", func(r *Request) *Request { r.Destinations[0].Recipients = nil; return r }, "no recipients"},
		{"empty recipient", func(r *Request) *Request { r.Destinations[0].Recipients[0].Address = ""; return r }, "empty address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := d.validateRequest(tc.mutate(valid()))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validateRequest error = %v, want one mentioning %q", err, tc.want)
			}
		})
	}

	t.Run("normalises and keeps duplicate recipients", func(t *testing.T) {
		r := valid()
		r.Message.Size = size(1)
		r.MailOptions = &smtp.MailOptions{Transport: &smtp.TransportOptions{Size: size(1)}}
		r.Destinations = oneDestination("Mail.Example.COM.", "a@x", "a@x")
		got, err := d.validateRequest(r)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0].domain != "mail.example.com" || len(got[0].recipients) != 2 {
			t.Fatalf("validateRequest = %+v", got)
		}
		r.Destinations[0].Recipients[0].Address = "mutated"
		if got[0].recipients[0].Address != "a@x" {
			t.Error("validated recipients alias the caller's slice")
		}
	})
}

func TestStubsValidateBeforeReportingUnimplemented(t *testing.T) {
	d, err := New(&Options{LocalNames: []string{"mx.sender.test"}, MTASTS: &MTASTSOptions{Cache: validCache()}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := d.Deliver(ctx, nil, nil); err == nil || errors.Is(err, errNotImplemented) {
		t.Errorf("Deliver(nil) = %v, want a validation error", err)
	}
	req := &Request{Message: stringSource("x"), Destinations: oneDestination("example.com", "a@example.com")}
	if _, err := d.Deliver(ctx, req, nil); !errors.Is(err, errNotImplemented) {
		t.Errorf("Deliver(valid) = %v, want errNotImplemented until T29", err)
	}
	if _, err := d.RefreshPolicy(ctx, "bad domain", nil); err == nil || errors.Is(err, errNotImplemented) {
		t.Errorf("RefreshPolicy(invalid) = %v, want a validation error", err)
	}
	if _, err := d.RefreshPolicy(ctx, "example.com", nil); !errors.Is(err, errNotImplemented) {
		t.Errorf("RefreshPolicy(valid) = %v, want errNotImplemented until T27", err)
	}
	plain, err := New(baseOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.RefreshPolicy(ctx, "example.com", nil); err == nil || !strings.Contains(err.Error(), "MTASTS") {
		t.Errorf("RefreshPolicy without MTA-STS = %v, want a configuration error", err)
	}
}

func TestMemoryPolicyCacheStubFailsClosed(t *testing.T) {
	cache := NewMemoryPolicyCache(nil)
	if _, _, err := cache.Load(context.Background(), &PolicyCacheLoadRequest{Domain: "example.com"}); !errors.Is(err, errNotImplemented) {
		t.Errorf("Load = %v, want errNotImplemented until T27 (a load error defers delivery; it is never a miss)", err)
	}
	if err := cache.Store(context.Background(), &PolicyCacheStoreRequest{}); !errors.Is(err, errNotImplemented) {
		t.Errorf("Store = %v, want errNotImplemented until T27", err)
	}
}
