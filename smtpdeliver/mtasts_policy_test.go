package smtpdeliver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	enforcePolicy = "version: STSv1\nmode: enforce\nmx: mx.example.com\nmax_age: 604800\n"
	testingPolicy = "version: STSv1\nmode: testing\nmx: mx.example.com\nmax_age: 604800\n"
	nonePolicy    = "version: STSv1\nmode: none\nmax_age: 604800\n"
	week          = 7 * 24 * time.Hour
)

// policyServer is a local HTTPS server answering as mta-sts.example.com, with a
// client that trusts it and dials it whatever host the URL names.
type policyServer struct {
	srv      *httptest.Server
	client   *http.Client
	requests atomic.Int32
	mu       sync.Mutex
	handler  http.HandlerFunc
	headers  http.Header
	paths    []string
	// sent are the request headers as the client sent them. The server's
	// view is not enough: net/http adds Cache-Control for Pragma: no-cache.
	sent []http.Header
}

type recordingTransport struct {
	inner http.RoundTripper
	ps    *policyServer
}

func (rt *recordingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	rt.ps.mu.Lock()
	rt.ps.sent = append(rt.ps.sent, r.Header.Clone())
	rt.ps.mu.Unlock()
	return rt.inner.RoundTrip(r)
}

func newPolicyServer(t *testing.T) *policyServer {
	t.Helper()
	return newPolicyServerFor(t, "mta-sts.example.com")
}

// newPolicyServerFor serves a certificate for names, trusted by the client.
func newPolicyServerFor(t *testing.T, names ...string) *policyServer {
	t.Helper()
	ps := &policyServer{}
	ps.serve(http.StatusOK, enforcePolicy)
	ps.srv = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ps.requests.Add(1)
		ps.mu.Lock()
		h := ps.handler
		ps.headers = r.Header.Clone()
		ps.paths = append(ps.paths, r.Host+r.URL.Path)
		ps.mu.Unlock()
		h(w, r)
	}))
	// The certificate-mismatch test makes handshakes fail on purpose.
	ps.srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	cert, pool := policyCert(t, names...)
	ps.srv.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	ps.srv.StartTLS()
	t.Cleanup(ps.srv.Close)
	addr := ps.srv.Listener.Addr().String()
	ps.client = &http.Client{Transport: &recordingTransport{ps: ps, inner: &http.Transport{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}}}
	return ps
}

func (ps *policyServer) serve(status int, body string) {
	ps.setHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
}

func (ps *policyServer) setHandler(h http.HandlerFunc) {
	ps.mu.Lock()
	ps.handler = h
	ps.mu.Unlock()
}

func policyCert(t *testing.T, names ...string) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: names[0]},
		DNSNames:     names,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

// scriptedCache wraps a memory cache with injectable failures and a store
// counter.
type scriptedCache struct {
	inner    PolicyCache
	loadErr  error
	storeErr error
	stores   atomic.Int32
}

func newScriptedCache() *scriptedCache { return &scriptedCache{inner: NewMemoryPolicyCache(nil)} }

func (c *scriptedCache) cache() PolicyCache {
	return PolicyCache{
		Load: func(ctx context.Context, req *PolicyCacheLoadRequest) (PolicyCacheEntry, bool, error) {
			if c.loadErr != nil {
				return PolicyCacheEntry{}, false, c.loadErr
			}
			return c.inner.Load(ctx, req)
		},
		Store: func(ctx context.Context, req *PolicyCacheStoreRequest) error {
			c.stores.Add(1)
			if c.storeErr != nil {
				return c.storeErr
			}
			return c.inner.Store(ctx, req)
		},
	}
}

func (c *scriptedCache) put(t *testing.T, id, body string, fetched time.Time) {
	t.Helper()
	p, _ := parsePolicy([]byte(body))
	if err := c.inner.Store(context.Background(), &PolicyCacheStoreRequest{Entry: PolicyCacheEntry{Domain: "example.com", ID: id, Body: []byte(body), Policy: p, FetchedAt: fetched}}); err != nil {
		t.Fatal(err)
	}
}

type mtastsHarness struct {
	d     *Deliverer
	dns   *fakeResolver
	srv   *policyServer
	cache *scriptedCache
	now   time.Time
}

func newMTASTSHarness(t *testing.T) *mtastsHarness {
	t.Helper()
	h := &mtastsHarness{dns: newFakeResolver(DNSSECInsecure), srv: newPolicyServer(t), cache: newScriptedCache(), now: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}
	h.dns.setTXT("_mta-sts.example.com", TXTLookup{State: LookupFound, Records: []string{"v=STSv1; id=live1"}, Security: DNSSECInsecure}, nil)
	d, err := New(&Options{
		Resolver:               h.dns.resolver(),
		MTASTS:                 &MTASTSOptions{Cache: h.cache.cache()},
		HTTPClient:             h.srv.client,
		DisableLoopElimination: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	d.mtasts.now = func() time.Time { return h.now }
	h.d = d
	return h
}

func (h *mtastsHarness) evaluate(t *testing.T, force bool) mtastsDecision {
	t.Helper()
	dec, err := h.d.mtasts.evaluate(context.Background(), "example.com", force)
	if err != nil {
		t.Fatal(err)
	}
	return dec
}

func (h *mtastsHarness) txtQueries() int {
	n := 0
	for _, q := range h.dns.queried() {
		if strings.HasPrefix(q, "TXT ") {
			n++
		}
	}
	return n
}

// TestMTASTSStateTable implements DELIVERY-DESIGN.md §5's table row by row.
func TestMTASTSStateTable(t *testing.T) {
	t.Run("valid unexpired, refresh not due: apply cached without discovery", func(t *testing.T) {
		h := newMTASTSHarness(t)
		h.cache.put(t, "old1", enforcePolicy, h.now.Add(-time.Hour))
		dec := h.evaluate(t, false)
		if dec.policy == nil || dec.result.Source != PolicySourceCache || !dec.result.Applied || dec.result.Cause != nil {
			t.Fatalf("decision = %+v", dec.result)
		}
		if h.txtQueries() != 0 || h.srv.requests.Load() != 0 {
			t.Errorf("discovery ran for a policy not due for refresh")
		}
	})
	t.Run("valid unexpired, due refresh succeeds: store and apply refreshed", func(t *testing.T) {
		h := newMTASTSHarness(t)
		h.cache.put(t, "old1", testingPolicy, h.now.Add(-2*24*time.Hour))
		dec := h.evaluate(t, false)
		if dec.result.Source != PolicySourceFetched || dec.result.Mode != "enforce" || !dec.result.ValidUntil.Equal(h.now.Add(week)) {
			t.Fatalf("decision = %+v", dec.result)
		}
		e, ok, _ := h.cache.inner.Load(context.Background(), &PolicyCacheLoadRequest{Domain: "example.com"})
		if !ok || e.ID != "live1" || string(e.Body) != enforcePolicy || !e.FetchedAt.Equal(h.now) {
			t.Errorf("stored entry = %+v", e)
		}
	})
	for _, fail := range []struct {
		name  string
		setup func(h *mtastsHarness)
	}{
		{"TXT lookup fails", func(h *mtastsHarness) { h.dns.setTXT("_mta-sts.example.com", TXTLookup{}, errors.New("SERVFAIL")) }},
		{"policy fetch fails", func(h *mtastsHarness) { h.srv.serve(http.StatusInternalServerError, "") }},
		{"fetched policy is invalid", func(h *mtastsHarness) { h.srv.serve(http.StatusOK, "version: STSv1\nmode: bogus\n") }},
		{"TXT record is invalid", func(h *mtastsHarness) {
			h.dns.setTXT("_mta-sts.example.com", TXTLookup{State: LookupFound, Records: []string{"v=STSv1; ext=1"}, Security: DNSSECInsecure}, nil)
		}},
	} {
		t.Run("valid unexpired, due refresh: "+fail.name+": apply cached, record, do not extend", func(t *testing.T) {
			h := newMTASTSHarness(t)
			fetched := h.now.Add(-6 * 24 * time.Hour)
			h.cache.put(t, "old1", enforcePolicy, fetched)
			fail.setup(h)
			dec := h.evaluate(t, false)
			if dec.policy == nil || dec.result.Source != PolicySourceCache || dec.result.Cause == nil {
				t.Fatalf("decision = %+v, want the cached policy with the refresh failure recorded", dec.result)
			}
			if want := fetched.Add(week); !dec.result.ValidUntil.Equal(want) {
				t.Errorf("ValidUntil = %v, want unchanged %v: a failed refresh must never extend expiry", dec.result.ValidUntil, want)
			}
			if h.cache.stores.Load() != 0 {
				t.Error("a failed refresh stored something")
			}
		})
	}
	t.Run("removed TXT record does not remove a cached policy", func(t *testing.T) {
		h := newMTASTSHarness(t)
		h.cache.put(t, "old1", enforcePolicy, h.now.Add(-2*24*time.Hour))
		h.dns.setTXT("_mta-sts.example.com", TXTLookup{State: LookupNotFound, Security: DNSSECInsecure}, nil)
		if dec := h.evaluate(t, false); dec.policy == nil || dec.result.Source != PolicySourceCache {
			t.Fatalf("decision = %+v; RFC 8461 §3.1 keeps the cached policy", dec.result)
		}
	})
	t.Run("expired, new policy succeeds: store and apply new", func(t *testing.T) {
		h := newMTASTSHarness(t)
		h.cache.put(t, "old1", enforcePolicy, h.now.Add(-8*24*time.Hour))
		if dec := h.evaluate(t, false); dec.result.Source != PolicySourceFetched || dec.policy == nil {
			t.Fatalf("decision = %+v", dec.result)
		}
	})
	t.Run("expired, fetch fails: no policy, never the expired one", func(t *testing.T) {
		h := newMTASTSHarness(t)
		h.cache.put(t, "old1", enforcePolicy, h.now.Add(-8*24*time.Hour))
		h.srv.serve(http.StatusServiceUnavailable, "")
		dec := h.evaluate(t, false)
		if dec.policy != nil || dec.result.Applied || dec.deferred {
			t.Fatalf("decision = %+v, want no policy: an expired policy must never apply", dec.result)
		}
		if !errors.Is(dec.result.Cause, errPolicyFetch) {
			t.Errorf("Cause = %v, want the fetch failure", dec.result.Cause)
		}
	})
	t.Run("absent and no TXT: no policy and no failure", func(t *testing.T) {
		h := newMTASTSHarness(t)
		h.dns.setTXT("_mta-sts.example.com", TXTLookup{State: LookupFound, Records: []string{"v=spf1 -all"}, Security: DNSSECInsecure}, nil)
		dec := h.evaluate(t, false)
		if dec.policy != nil || dec.result.Cause != nil || h.srv.requests.Load() != 0 {
			t.Fatalf("decision = %+v, requests = %d", dec.result, h.srv.requests.Load())
		}
	})
	t.Run("live mode none: stored, blocks nothing", func(t *testing.T) {
		h := newMTASTSHarness(t)
		h.srv.serve(http.StatusOK, nonePolicy)
		dec := h.evaluate(t, false)
		if dec.policy != nil || dec.result.Mode != "none" || dec.result.Applied || h.cache.stores.Load() != 1 {
			t.Fatalf("decision = %+v, stores = %d", dec.result, h.cache.stores.Load())
		}
	})
	t.Run("testing mode applies for reporting but does not constrain", func(t *testing.T) {
		h := newMTASTSHarness(t)
		h.srv.serve(http.StatusOK, testingPolicy)
		dec := h.evaluate(t, false)
		if dec.policy == nil || dec.result.Applied || dec.result.Mode != "testing" {
			t.Fatalf("decision = %+v", dec.result)
		}
	})
	t.Run("cache load error defers; it is not a miss", func(t *testing.T) {
		h := newMTASTSHarness(t)
		h.cache.loadErr = errors.New("database down")
		dec := h.evaluate(t, false)
		if !dec.deferred || !errors.Is(dec.result.Cause, errPolicyCacheLoad) {
			t.Fatalf("decision = %+v, want deferred", dec)
		}
		if h.txtQueries() != 0 {
			t.Error("discovery ran after a cache load error")
		}
	})
	t.Run("store error: live policy still applies", func(t *testing.T) {
		h := newMTASTSHarness(t)
		h.cache.storeErr = errors.New("disk full")
		dec := h.evaluate(t, false)
		if dec.policy == nil || dec.result.Source != PolicySourceFetched || !errors.Is(dec.result.Cause, errPolicyCacheStore) {
			t.Fatalf("decision = %+v", dec.result)
		}
	})
}

func TestMTASTSCachedEntriesAreRevalidated(t *testing.T) {
	t.Run("expiry comes from Body, never from Policy.MaxAge", func(t *testing.T) {
		h := newMTASTSHarness(t)
		short := "version: STSv1\nmode: enforce\nmx: mx.example.com\nmax_age: 3600\n"
		lying := MTASTSPolicy{Version: "STSv1", Mode: MTASTSEnforce, MX: []string{"mx.example.com"}, MaxAge: 365 * 24 * time.Hour}
		_ = h.cache.inner.Store(context.Background(), &PolicyCacheStoreRequest{Entry: PolicyCacheEntry{Domain: "example.com", ID: "old1", Body: []byte(short), Policy: lying, FetchedAt: h.now.Add(-2 * time.Hour)}})
		h.srv.serve(http.StatusServiceUnavailable, "")
		if dec := h.evaluate(t, false); dec.policy != nil {
			t.Fatalf("decision = %+v: the Body's max_age expired the entry an hour ago", dec.result)
		}
	})
	for _, bad := range []struct {
		name  string
		entry PolicyCacheEntry
	}{
		{"empty Body", PolicyCacheEntry{Domain: "example.com", ID: "old1"}},
		{"unparsable Body", PolicyCacheEntry{Domain: "example.com", ID: "old1", Body: []byte("garbage")}},
		{"wrong domain", PolicyCacheEntry{Domain: "other.example", ID: "old1", Body: []byte(enforcePolicy)}},
		{"invalid id", PolicyCacheEntry{Domain: "example.com", ID: "bad id", Body: []byte(enforcePolicy)}},
		{"FetchedAt in the future", PolicyCacheEntry{Domain: "example.com", ID: "old1", Body: []byte(enforcePolicy)}},
	} {
		t.Run(bad.name+" is treated as absent", func(t *testing.T) {
			h := newMTASTSHarness(t)
			e := bad.entry
			e.FetchedAt = h.now.Add(-time.Hour)
			if bad.name == "FetchedAt in the future" {
				e.FetchedAt = h.now.Add(time.Hour)
			}
			// Load returns the entry for whatever domain is asked, so a
			// cache that mixes up domains is exercised too.
			stored := e
			inner := h.cache.inner
			h.cache.inner = PolicyCache{
				Load: func(context.Context, *PolicyCacheLoadRequest) (PolicyCacheEntry, bool, error) {
					return stored, true, nil
				},
				Store: func(ctx context.Context, req *PolicyCacheStoreRequest) error {
					stored = req.Entry
					return inner.Store(ctx, req)
				},
			}
			h.srv.serve(http.StatusServiceUnavailable, "")
			dec := h.evaluate(t, false)
			if dec.policy != nil || !errors.Is(dec.result.Cause, errInvalidCachedEntry) {
				t.Fatalf("decision = %+v, want no policy with the invalid entry recorded", dec.result)
			}
			h.srv.serve(http.StatusOK, enforcePolicy)
			h.now = h.now.Add(policyFetchBackoff)
			if dec := h.evaluate(t, false); dec.policy == nil || dec.result.Source != PolicySourceFetched {
				t.Errorf("after the policy host recovers: %+v, want a live policy replacing the invalid entry", dec.result)
			}
		})
	}
}

func TestMTASTSFetchBackoff(t *testing.T) {
	h := newMTASTSHarness(t)
	h.srv.serve(http.StatusInternalServerError, "")
	h.evaluate(t, false)
	if got := h.srv.requests.Load(); got != 1 {
		t.Fatalf("requests = %d", got)
	}
	h.now = h.now.Add(policyFetchBackoff - time.Second)
	if dec := h.evaluate(t, false); !errors.Is(dec.result.Cause, errFetchBackoff) || h.srv.requests.Load() != 1 {
		t.Fatalf("second fetch within five minutes: cause %v, requests %d", dec.result.Cause, h.srv.requests.Load())
	}
	h.dns.setTXT("_mta-sts.example.com", TXTLookup{State: LookupFound, Records: []string{"v=STSv1; id=live2"}, Security: DNSSECInsecure}, nil)
	h.evaluate(t, false)
	if h.srv.requests.Load() != 2 {
		t.Errorf("a new policy id must not inherit the old id's backoff (RFC 8461 §3.3 is per id)")
	}
	h.now = h.now.Add(2 * policyFetchBackoff)
	h.srv.serve(http.StatusOK, enforcePolicy)
	if dec := h.evaluate(t, false); dec.policy == nil {
		t.Errorf("after the backoff: %+v", dec.result)
	}
}

func TestRefreshPolicy(t *testing.T) {
	h := newMTASTSHarness(t)
	h.cache.put(t, "old1", enforcePolicy, h.now.Add(-time.Hour))
	got, err := h.d.RefreshPolicy(context.Background(), "Example.COM.", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != PolicySourceFetched || h.srv.requests.Load() != 1 {
		t.Errorf("RefreshPolicy = %+v, requests %d; want a forced live fetch", got, h.srv.requests.Load())
	}
	h.srv.serve(http.StatusInternalServerError, "")
	h.dns.setTXT("_mta-sts.example.com", TXTLookup{State: LookupFound, Records: []string{"v=STSv1; id=live9"}, Security: DNSSECInsecure}, nil)
	got, err = h.d.RefreshPolicy(context.Background(), "example.com", nil)
	if err != nil || got.Source != PolicySourceCache || got.Cause == nil {
		t.Errorf("failed refresh = %+v, %v; want the cached policy and the failure in Cause, no call error", got, err)
	}
	if _, err := h.d.RefreshPolicy(context.Background(), "not a domain", nil); err == nil {
		t.Error("RefreshPolicy accepted an invalid domain")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.d.RefreshPolicy(ctx, "example.com", nil); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled RefreshPolicy = %v", err)
	}
	plain, err := New(&Options{DisableLoopElimination: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plain.RefreshPolicy(context.Background(), "example.com", nil); err == nil {
		t.Error("RefreshPolicy without MTA-STS succeeded")
	}
}

func TestPolicyFetchHardening(t *testing.T) {
	fetch := func(t *testing.T, ps *policyServer, timeout time.Duration) ([]byte, error) {
		t.Helper()
		d, err := New(&Options{HTTPClient: ps.client, DisableLoopElimination: true, Timeouts: Timeouts{PolicyFetch: timeout}})
		if err != nil {
			t.Fatal(err)
		}
		return d.fetchPolicy(context.Background(), "example.com")
	}
	t.Run("fetches exactly the well-known URL without HTTP caching", func(t *testing.T) {
		ps := newPolicyServer(t)
		body, err := fetch(t, ps, 0)
		if err != nil || string(body) != enforcePolicy {
			t.Fatalf("fetch = %q, %v", body, err)
		}
		if ps.paths[0] != "mta-sts.example.com/.well-known/mta-sts.txt" {
			t.Errorf("fetched %q", ps.paths[0])
		}
		if got := ps.sent[0].Get("Cache-Control"); got != "no-cache" {
			t.Errorf("client sent Cache-Control %q, want no-cache (RFC 8461 §3.3)", got)
		}
	})
	t.Run("redirects are not followed", func(t *testing.T) {
		ps := newPolicyServer(t)
		ps.setHandler(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/moved" {
				w.Header().Set("Content-Type", "text/plain")
				_, _ = w.Write([]byte(enforcePolicy))
				return
			}
			http.Redirect(w, r, "https://mta-sts.example.com/moved", http.StatusFound)
		})
		client := *ps.client
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return nil } // a caller allowing redirects
		ps.client = &client
		if _, err := fetch(t, ps, 0); !errors.Is(err, errPolicyFetch) || ps.requests.Load() != 1 {
			t.Errorf("fetch = %v after %d requests; want the redirect refused", err, ps.requests.Load())
		}
	})
	for _, tc := range []struct {
		name, contentType string
		ok                bool
	}{
		{"text/plain with charset", "text/plain; charset=utf-8", true},
		{"html", "text/html", false},
		{"missing", "", false},
	} {
		t.Run("content type "+tc.name, func(t *testing.T) {
			ps := newPolicyServer(t)
			ps.setHandler(func(w http.ResponseWriter, r *http.Request) {
				w.Header()["Content-Type"] = []string{tc.contentType}
				_, _ = w.Write([]byte(enforcePolicy))
			})
			if _, err := fetch(t, ps, 0); (err == nil) != tc.ok {
				t.Errorf("fetch error = %v, want ok=%v", err, tc.ok)
			}
		})
	}
	t.Run("non-200 status", func(t *testing.T) {
		ps := newPolicyServer(t)
		ps.serve(http.StatusNoContent, "")
		if _, err := fetch(t, ps, 0); !errors.Is(err, errPolicyFetch) {
			t.Errorf("fetch = %v", err)
		}
	})
	t.Run("body at the limit is accepted, one byte over is refused", func(t *testing.T) {
		ps := newPolicyServer(t)
		pad := maxPolicyBody - len(enforcePolicy)
		ps.serve(http.StatusOK, enforcePolicy+strings.Repeat("x", pad))
		if body, err := fetch(t, ps, 0); err != nil || len(body) != maxPolicyBody {
			t.Errorf("64 KiB body: %d bytes, %v", len(body), err)
		}
		ps.serve(http.StatusOK, enforcePolicy+strings.Repeat("x", pad+1))
		if _, err := fetch(t, ps, 0); !errors.Is(err, errPolicyFetch) {
			t.Errorf("64 KiB + 1 body: %v, want refused", err)
		}
	})
	t.Run("slow body is bounded by Timeouts.PolicyFetch", func(t *testing.T) {
		ps := newPolicyServer(t)
		release := make(chan struct{})
		defer close(release)
		ps.setHandler(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("version: STSv1\n"))
			w.(http.Flusher).Flush()
			select {
			case <-release:
			case <-r.Context().Done():
			}
		})
		start := time.Now()
		if _, err := fetch(t, ps, 200*time.Millisecond); err == nil {
			t.Fatal("slowloris fetch succeeded")
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("fetch took %v, want about Timeouts.PolicyFetch", elapsed)
		}
	})
	t.Run("certificate must be valid for the policy host", func(t *testing.T) {
		ps := newPolicyServerFor(t, "www.example.com")
		if _, err := fetch(t, ps, 0); !errors.Is(err, errPolicyFetch) {
			t.Errorf("fetch with a certificate for another host = %v, want refused (RFC 8461 §3.3)", err)
		}
	})
}

func TestMemoryPolicyCache(t *testing.T) {
	c := NewMemoryPolicyCache(nil)
	ctx := context.Background()
	if _, ok, err := c.Load(ctx, &PolicyCacheLoadRequest{Domain: "example.com"}); ok || err != nil {
		t.Fatalf("empty cache Load = %v, %v", ok, err)
	}
	entry := PolicyCacheEntry{Domain: "example.com", ID: "1", Body: []byte(enforcePolicy), Policy: MTASTSPolicy{MX: []string{"mx.example.com"}}}
	if err := c.Store(ctx, &PolicyCacheStoreRequest{Entry: entry}); err != nil {
		t.Fatal(err)
	}
	entry.Body[0] = 'X'
	entry.Policy.MX[0] = "mutated"
	got, ok, err := c.Load(ctx, &PolicyCacheLoadRequest{Domain: "example.com"})
	if err != nil || !ok || string(got.Body) != enforcePolicy || got.Policy.MX[0] != "mx.example.com" {
		t.Fatalf("Load = %+v, %v, %v; want an isolated copy of what was stored", got, ok, err)
	}
	got.Body[0] = 'Y'
	again, _, _ := c.Load(ctx, &PolicyCacheLoadRequest{Domain: "example.com"})
	if string(again.Body) != enforcePolicy {
		t.Error("Load returned the cache's own slice")
	}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = c.Store(ctx, &PolicyCacheStoreRequest{Entry: PolicyCacheEntry{Domain: fmt.Sprintf("d%d.example", i)}})
			_, _, _ = c.Load(ctx, &PolicyCacheLoadRequest{Domain: "example.com"})
		}()
	}
	wg.Wait()
}
