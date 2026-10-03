package smtpdeliver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// testPKI is a small certificate hierarchy: root → intermediate → leaves.
type testPKI struct {
	root, inter       *x509.Certificate
	rootKey, interKey *ecdsa.PrivateKey
	roots             *x509.CertPool
	serial            int64
}

func newTestPKI(t *testing.T) *testPKI {
	t.Helper()
	p := &testPKI{}
	p.rootKey = mustKey(t)
	p.root = p.issue(t, &x509.Certificate{Subject: pkix.Name{CommonName: "Test Root"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}, nil, nil, &p.rootKey.PublicKey, p.rootKey)
	p.interKey = mustKey(t)
	p.inter = p.issue(t, &x509.Certificate{Subject: pkix.Name{CommonName: "Test Intermediate"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}, p.root, p.rootKey, &p.interKey.PublicKey, nil)
	p.roots = x509.NewCertPool()
	p.roots.AddCert(p.root)
	return p
}

func mustKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func (p *testPKI) issue(t *testing.T, tmpl, parent *x509.Certificate, parentKey *ecdsa.PrivateKey, pub *ecdsa.PublicKey, selfKey *ecdsa.PrivateKey) *x509.Certificate {
	t.Helper()
	p.serial++
	tmpl.SerialNumber = big.NewInt(p.serial)
	if tmpl.NotBefore.IsZero() {
		tmpl.NotBefore = time.Now().Add(-time.Hour)
		tmpl.NotAfter = time.Now().Add(time.Hour)
	}
	signer, signerKey := parent, parentKey
	if parent == nil {
		signer, signerKey = tmpl, selfKey
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signer, pub, signerKey)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// leaf issues a server certificate under the intermediate. Its chain, as a
// server presents it, is leaf, intermediate, root.
type leafCert struct {
	cert  *x509.Certificate
	key   *ecdsa.PrivateKey
	chain []*x509.Certificate
}

func (p *testPKI) leaf(t *testing.T, cn string, dnsNames []string, expired bool) leafCert {
	t.Helper()
	key := mustKey(t)
	tmpl := &x509.Certificate{Subject: pkix.Name{CommonName: cn}, DNSNames: dnsNames, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	if expired {
		tmpl.NotBefore = time.Now().Add(-48 * time.Hour)
		tmpl.NotAfter = time.Now().Add(-24 * time.Hour)
	}
	c := p.issue(t, tmpl, p.inter, p.interKey, &key.PublicKey, nil)
	return leafCert{cert: c, key: key, chain: []*x509.Certificate{c, p.inter, p.root}}
}

// selfSigned is a certificate no Web PKI root vouches for.
func selfSigned(t *testing.T, name string) leafCert {
	t.Helper()
	p := &testPKI{}
	key := mustKey(t)
	c := p.issue(t, &x509.Certificate{Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}, nil, nil, &key.PublicKey, key)
	return leafCert{cert: c, key: key, chain: []*x509.Certificate{c}}
}

func (l leafCert) tlsCert() tls.Certificate {
	var raw [][]byte
	for _, c := range l.chain {
		raw = append(raw, c.Raw)
	}
	return tls.Certificate{Certificate: raw, PrivateKey: l.key, Leaf: l.cert}
}

func tlsaFor(usage, selector, mtype uint8, c *x509.Certificate) TLSA {
	data := c.Raw
	if selector == 1 {
		data = c.RawSubjectPublicKeyInfo
	}
	switch mtype {
	case 1:
		s := sha256.Sum256(data)
		data = s[:]
	case 2:
		s := sha512.Sum512(data)
		data = s[:]
	}
	return TLSA{Usage: usage, Selector: selector, MatchingType: mtype, Association: append([]byte(nil), data...)}
}

func TestUsableTLSA(t *testing.T) {
	ok := func(u, s, m uint8, n int) bool {
		return usableTLSA(TLSA{Usage: u, Selector: s, MatchingType: m, Association: make([]byte, n)})
	}
	for _, u := range []uint8{2, 3} {
		for _, s := range []uint8{0, 1} {
			if !ok(u, s, 0, 10) || !ok(u, s, 1, 32) || !ok(u, s, 2, 64) {
				t.Errorf("usage %d selector %d: a supported combination was unusable", u, s)
			}
		}
	}
	for _, c := range []struct {
		u, s, m uint8
		n       int
	}{{0, 0, 1, 32}, {1, 1, 1, 32}, {4, 1, 1, 32}, {3, 2, 1, 32}, {3, 1, 3, 32}, {3, 1, 1, 31}, {3, 1, 2, 32}, {3, 0, 0, 0}} {
		if ok(c.u, c.s, c.m, c.n) {
			t.Errorf("TLSA %d %d %d with %d bytes is usable, want unusable", c.u, c.s, c.m, c.n)
		}
	}
}

func TestVerifyDANEVectors(t *testing.T) {
	p := newTestPKI(t)
	l := p.leaf(t, "mx.example.com", []string{"mx.example.com"}, false)
	refs := []string{"mx.example.com", "example.com"}
	now := time.Now()
	for _, sel := range []uint8{0, 1} {
		for _, mt := range []uint8{0, 1, 2} {
			ee := tlsaFor(3, sel, mt, l.cert)
			if err := verifyDANE(l.chain, []TLSA{ee}, refs, now); err != nil {
				t.Errorf("DANE-EE %d %d: %v", sel, mt, err)
			}
			for name, anchor := range map[string]*x509.Certificate{"root": p.root, "intermediate": p.inter} {
				ta := tlsaFor(2, sel, mt, anchor)
				if err := verifyDANE(l.chain, []TLSA{ta}, refs, now); err != nil {
					t.Errorf("DANE-TA %d %d at the %s: %v", sel, mt, name, err)
				}
			}
			if err := verifyDANE(l.chain, []TLSA{tlsaFor(3, sel, mt, selfSigned(t, "x").cert)}, refs, now); !errors.Is(err, errDANEVerify) {
				t.Errorf("DANE-EE %d %d with a foreign certificate: %v", sel, mt, err)
			}
		}
	}
}

func TestVerifyDANERules(t *testing.T) {
	p := newTestPKI(t)
	now := time.Now()
	refs := []string{"mx.example.com", "example.com"}
	taRoot := tlsaFor(2, 0, 1, p.root)

	t.Run("DANE-EE ignores names and expiry", func(t *testing.T) {
		l := p.leaf(t, "unrelated.test", []string{"unrelated.test"}, true)
		if err := verifyDANE(l.chain, []TLSA{tlsaFor(3, 1, 1, l.cert)}, refs, now); err != nil {
			t.Error(err)
		}
	})
	t.Run("DANE-TA checks names", func(t *testing.T) {
		l := p.leaf(t, "other.test", []string{"other.test"}, false)
		if err := verifyDANE(l.chain, []TLSA{taRoot}, refs, now); !errors.Is(err, errDANEVerify) {
			t.Errorf("TA with a non-matching name = %v", err)
		}
	})
	t.Run("DANE-TA accepts the next-hop domain as second identifier", func(t *testing.T) {
		l := p.leaf(t, "example.com", []string{"example.com"}, false)
		if err := verifyDANE(l.chain, []TLSA{taRoot}, refs, now); err != nil {
			t.Error(err)
		}
	})
	t.Run("DANE-TA checks expiry", func(t *testing.T) {
		l := p.leaf(t, "mx.example.com", []string{"mx.example.com"}, true)
		if err := verifyDANE(l.chain, []TLSA{taRoot}, refs, now); !errors.Is(err, errDANEVerify) {
			t.Errorf("TA with an expired leaf = %v", err)
		}
	})
	t.Run("DANE-TA requires the anchor in the presented chain", func(t *testing.T) {
		l := p.leaf(t, "mx.example.com", []string{"mx.example.com"}, false)
		if err := verifyDANE(l.chain[:2], []TLSA{taRoot}, refs, now); !errors.Is(err, errDANEVerify) {
			t.Errorf("TA for a root the server did not send = %v", err)
		}
	})
	t.Run("DANE-TA never matches the leaf itself", func(t *testing.T) {
		l := p.leaf(t, "mx.example.com", []string{"mx.example.com"}, false)
		if err := verifyDANE(l.chain, []TLSA{tlsaFor(2, 1, 1, l.cert)}, refs, now); !errors.Is(err, errDANEVerify) {
			t.Errorf("TA record matching the leaf = %v", err)
		}
	})
	t.Run("one matching record among several is enough", func(t *testing.T) {
		l := p.leaf(t, "mx.example.com", []string{"mx.example.com"}, false)
		bogus := TLSA{Usage: 3, Selector: 1, MatchingType: 1, Association: make([]byte, 32)}
		if err := verifyDANE(l.chain, []TLSA{bogus, taRoot}, refs, now); err != nil {
			t.Error(err)
		}
	})
	t.Run("no certificate", func(t *testing.T) {
		if err := verifyDANE(nil, []TLSA{taRoot}, refs, now); !errors.Is(err, errDANEVerify) {
			t.Error(err)
		}
	})
}

func TestCertNameMatching(t *testing.T) {
	cases := []struct {
		presented, ref string
		want           bool
	}{
		{"mx.example.com", "MX.example.com.", true},
		{"*.example.com", "mx1.example.com", true},
		{"*.example.com", "example.com", false},
		{"*.example.com", "a.b.example.com", false},
		{"*.com", "example.com", false},
		{"smtp*.example.com", "smtp1.example.com", false},
		{"", "", false},
	}
	for _, c := range cases {
		if got := certNameMatches(c.presented, c.ref); got != c.want {
			t.Errorf("certNameMatches(%q, %q) = %v, want %v", c.presented, c.ref, got, c.want)
		}
	}
	p := newTestPKI(t)
	cnOnly := p.leaf(t, "mx.example.com", nil, false)
	if err := matchReferenceIDs(cnOnly.cert, []string{"mx.example.com"}); err != nil {
		t.Errorf("CN-ID without DNS-IDs: %v (RFC 7672 §3.2.3)", err)
	}
	both := p.leaf(t, "mx.example.com", []string{"other.example.com"}, false)
	if err := matchReferenceIDs(both.cert, []string{"mx.example.com"}); err == nil {
		t.Error("CN-ID was considered although DNS-IDs are present (RFC 7672 §3.2.3)")
	}
}

// daneDeliverer builds a Deliverer with DANE enabled over f.
func daneDeliverer(t *testing.T, f *fakeResolver, mode DANEMode, mutate func(*Options)) *Deliverer {
	t.Helper()
	opts := &Options{Resolver: f.resolver(), DANE: &DANEOptions{Mode: mode}, DisableLoopElimination: true}
	if mutate != nil {
		mutate(opts)
	}
	d, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func secureStep(mx string) routeStep {
	return routeStep{host: routeHost{name: mx, pref: 10, security: DNSSECSecure}, addr: netip.MustParseAddr("192.0.2.1"), addrSecurity: DNSSECSecure}
}

func TestLookupDANE(t *testing.T) {
	usable := TLSA{Usage: 3, Selector: 1, MatchingType: 1, Association: make([]byte, 32)}
	pkixOnly := TLSA{Usage: 1, Selector: 1, MatchingType: 1, Association: make([]byte, 32)}
	secure := func(rs ...TLSA) TLSALookup {
		return TLSALookup{State: LookupFound, Records: rs, Security: DNSSECSecure}
	}
	tempErr := errors.New("SERVFAIL")
	cases := []struct {
		name      string
		step      func(routeStep) routeStep
		setup     func(f *fakeResolver)
		want      daneState
		base      string
		err       bool
		noQueries bool
	}{
		{"secure usable records", nil, func(f *fakeResolver) { f.setTLSA("_25._tcp.mx.example.com", secure(usable), nil) }, daneUsable, "mx.example.com", false, false},
		{"secure but all unusable still requires TLS", nil, func(f *fakeResolver) { f.setTLSA("_25._tcp.mx.example.com", secure(pkixOnly), nil) }, daneUnusable, "mx.example.com", false, false},
		{"authenticated denial", nil, func(f *fakeResolver) {
			f.setTLSA("_25._tcp.mx.example.com", TLSALookup{State: LookupNotFound, Security: DNSSECSecure}, nil)
		}, daneNone, "", false, false},
		{"insecure TLSA", nil, func(f *fakeResolver) {
			f.setTLSA("_25._tcp.mx.example.com", TLSALookup{State: LookupFound, Records: []TLSA{usable}, Security: DNSSECInsecure}, nil)
		}, daneNone, "", false, false},
		{"lookup error makes the MX unreachable", nil, func(f *fakeResolver) { f.setTLSA("_25._tcp.mx.example.com", TLSALookup{}, tempErr) }, daneNone, "", true, false},
		{"bogus TLSA makes the MX unreachable", nil, func(f *fakeResolver) {
			f.setTLSA("_25._tcp.mx.example.com", TLSALookup{State: LookupFound, Records: []TLSA{usable}, Security: DNSSECBogus}, nil)
		}, daneNone, "", true, false},
		{"indeterminate TLSA makes the MX unreachable", nil, func(f *fakeResolver) {
			f.setTLSA("_25._tcp.mx.example.com", TLSALookup{State: LookupFound, Security: DNSSECIndeterminate}, nil)
		}, daneNone, "", true, false},
		{"unvalidated TLSA makes the MX unreachable", nil, func(f *fakeResolver) {
			f.setTLSA("_25._tcp.mx.example.com", TLSALookup{State: LookupFound, Security: DNSSECUnvalidated}, nil)
		}, daneNone, "", true, false},
		{"insecure MX skips TLSA", func(s routeStep) routeStep { s.host.security = DNSSECInsecure; return s }, func(f *fakeResolver) {
			f.setTLSA("_25._tcp.mx.example.com", secure(usable), nil)
		}, daneNone, "", false, true},
		{"insecure address skips TLSA", func(s routeStep) routeStep { s.addrSecurity = DNSSECInsecure; return s }, func(f *fakeResolver) {
			f.setTLSA("_25._tcp.mx.example.com", secure(usable), nil)
		}, daneNone, "", false, true},
		{"secure alias: expanded name first", func(s routeStep) routeStep { s.addrCanonical = "real.example.net"; return s }, func(f *fakeResolver) {
			f.setTLSA("_25._tcp.real.example.net", secure(usable), nil)
			f.setTLSA("_25._tcp.mx.example.com", secure(pkixOnly), nil)
		}, daneUsable, "real.example.net", false, false},
		{"secure alias: falls back to the original name", func(s routeStep) routeStep { s.addrCanonical = "real.example.net"; return s }, func(f *fakeResolver) {
			f.setTLSA("_25._tcp.real.example.net", TLSALookup{State: LookupNotFound, Security: DNSSECSecure}, nil)
			f.setTLSA("_25._tcp.mx.example.com", secure(usable), nil)
		}, daneUsable, "mx.example.com", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeResolver(DNSSECSecure)
			tc.setup(f)
			d := daneDeliverer(t, f, "", nil)
			step := secureStep("mx.example.com")
			if tc.step != nil {
				step = tc.step(step)
			}
			res, err := d.lookupDANE(context.Background(), "example.com", step)
			if err != nil {
				t.Fatal(err)
			}
			if (res.err != nil) != tc.err {
				t.Fatalf("err = %v, want error %v", res.err, tc.err)
			}
			if tc.err {
				if !errors.Is(res.err, errTLSALookup) {
					t.Errorf("err = %v, want errTLSALookup", res.err)
				}
				return
			}
			if res.state != tc.want || res.baseDomain != tc.base {
				t.Errorf("state %d base %q, want %d %q", res.state, res.baseDomain, tc.want, tc.base)
			}
			if tc.noQueries {
				for _, q := range f.queried() {
					if strings.HasPrefix(q, "TLSA") {
						t.Errorf("queried %q; RFC 7672 §2.2.2 skips TLSA here", q)
					}
				}
			}
		})
	}

	t.Run("reference identifiers", func(t *testing.T) {
		f := newFakeResolver(DNSSECSecure)
		f.setTLSA("_25._tcp.mx.example.com", secure(usable), nil)
		d := daneDeliverer(t, f, "", nil)
		step := secureStep("mx.example.com")
		step.host.nextHopCanonical = "example.net"
		res, _ := d.lookupDANE(context.Background(), "example.com", step)
		if strings.Join(res.refIDs, ",") != "mx.example.com,example.com,example.net" {
			t.Errorf("refIDs = %v, want base domain, next-hop domain, expanded next-hop (RFC 7672 §3.2.2)", res.refIDs)
		}
	})
	t.Run("DANE disabled does nothing", func(t *testing.T) {
		f := newFakeResolver(DNSSECSecure)
		d, _ := New(&Options{Resolver: f.resolver(), DisableLoopElimination: true})
		if res, _ := d.lookupDANE(context.Background(), "example.com", secureStep("mx.example.com")); res.state != daneNone || len(f.queried()) != 0 {
			t.Errorf("DANE disabled: %+v, queries %v", res, f.queried())
		}
	})
	t.Run("context", func(t *testing.T) {
		f := newFakeResolver(DNSSECSecure)
		d := daneDeliverer(t, f, "", nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := d.lookupDANE(ctx, "example.com", secureStep("mx.example.com")); !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	})
}

// handshake runs one TLS handshake with the attempt's client configuration
// against a server presenting chain. It returns the SNI the server saw and
// the client's error.
func handshake(t *testing.T, a *tlsAttempt, server leafCert) (string, error) {
	t.Helper()
	if a.skip != nil {
		t.Fatalf("attempt was skipped: %v", a.skip)
	}
	// A kernel-buffered loopback connection, not net.Pipe: after a failed
	// verification both sides may write (an alert and the server's flight)
	// at once, which deadlocks an unbuffered pipe.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	sni := make(chan string, 1)
	go func() {
		s, err := ln.Accept()
		if err != nil {
			sni <- ""
			return
		}
		srv := tls.Server(s, &tls.Config{
			Certificates: []tls.Certificate{server.tlsCert()},
			GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
				sni <- hello.ServerName
				return nil, nil
			},
		})
		_ = srv.SetDeadline(time.Now().Add(10 * time.Second))
		_ = srv.Handshake()
		_ = srv.Close()
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	client := tls.Client(c, a.config)
	err = client.Handshake()
	_ = client.Close()
	return <-sni, err
}

type tlsCase struct {
	name       string
	mode       DANEMode
	dane       daneResult
	sts        string // "", "enforce", "testing", "none", "enforce-other-mx"
	requireTLS bool
	insecureMX bool
	caller     func(*tls.Config)
	server     func(p *testPKI) leafCert
	skip       error
	required   bool
	handshake  bool // expected handshake success
	stsCause   bool
	daneCause  bool
}

func stsDecision(mode string) *mtastsDecision {
	if mode == "" {
		return nil
	}
	mx := "mx.example.com"
	if mode == "enforce-other-mx" {
		mode, mx = "enforce", "other.example.com"
	}
	p := MTASTSPolicy{Version: "STSv1", Mode: MTASTSMode(mode), MX: []string{mx}, MaxAge: week}
	dec := applied(PolicyCacheEntry{FetchedAt: time.Now()}, p, PolicySourceFetched, nil)
	return &dec
}

func TestTLSDecisionTable(t *testing.T) {
	p := newTestPKI(t)
	good := func(p *testPKI) leafCert { return p.leaf(t, "mx.example.com", []string{"mx.example.com"}, false) }
	wrongName := func(p *testPKI) leafCert { return p.leaf(t, "evil.example", []string{"evil.example"}, false) }
	untrusted := func(*testPKI) leafCert { return selfSigned(t, "mx.example.com") }
	daneFor := func(l leafCert) daneResult {
		return daneResult{state: daneUsable, baseDomain: "mx.example.com", records: []TLSA{tlsaFor(3, 1, 1, l.cert)}, refIDs: []string{"mx.example.com", "example.com"}}
	}
	selfSignedMX := selfSigned(t, "mx.example.com")
	otherKey := selfSigned(t, "mx.example.com")

	cases := []tlsCase{
		{name: "no policy: unauthenticated opportunistic TLS", server: untrusted, handshake: true},
		{name: "MTA-STS none: no constraint", sts: "none", server: untrusted, handshake: true},
		{name: "MTA-STS testing: failure recorded, delivery proceeds", sts: "testing", server: untrusted, handshake: true, stsCause: true},
		{name: "MTA-STS enforce: valid Web PKI certificate", sts: "enforce", server: good, required: true, handshake: true},
		{name: "MTA-STS enforce: untrusted certificate fails", sts: "enforce", server: untrusted, required: true, stsCause: true},
		{name: "MTA-STS enforce: name mismatch fails", sts: "enforce", server: wrongName, required: true, stsCause: true},
		{name: "MTA-STS enforce: MX not in policy is unreachable", sts: "enforce-other-mx", skip: errMTASTSMXMismatch},
		{name: "caller InsecureSkipVerify cannot disable enforce", sts: "enforce", server: untrusted, required: true, stsCause: true, caller: func(c *tls.Config) { c.InsecureSkipVerify = true }},
		{name: "DANE-EE match authenticates a self-signed certificate", dane: daneFor(selfSignedMX), server: func(*testPKI) leafCert { return selfSignedMX }, required: true, handshake: true},
		{name: "DANE-EE mismatch fails", dane: daneFor(otherKey), server: func(*testPKI) leafCert { return selfSignedMX }, required: true, daneCause: true},
		{name: "MTA-STS cannot rescue failed DANE", sts: "enforce", dane: daneFor(otherKey), server: good, required: true, daneCause: true},
		{name: "DANE and MTA-STS enforce intersect", sts: "enforce", dane: daneFor(selfSignedMX), server: func(*testPKI) leafCert { return selfSignedMX }, required: true, stsCause: true},
		{name: "secure unusable TLSA: TLS required, no authentication", dane: daneResult{state: daneUnusable, baseDomain: "mx.example.com"}, server: untrusted, required: true, handshake: true},
		{name: "TLSA lookup failure: unreachable", dane: daneResult{err: errTLSALookup}, skip: errTLSALookup},
		{name: "mandatory DANE without usable records: unreachable", mode: DANEMandatory, skip: errDANEMandatory},
		{name: "mandatory DANE with insecure MX: unreachable", mode: DANEMandatory, insecureMX: true, dane: daneFor(selfSignedMX), skip: errDANEInsecureMX},
		{name: "audit DANE: mismatch recorded, delivery proceeds", mode: DANEAudit, dane: daneFor(otherKey), server: func(*testPKI) leafCert { return selfSignedMX }, handshake: true, daneCause: true},
		{name: "audit DANE: TLSA lookup failure recorded, delivery proceeds", mode: DANEAudit, dane: daneResult{err: errTLSALookup}, server: untrusted, handshake: true, daneCause: true},
		{name: "REQUIRETLS with insecure MX and no MTA-STS: unreachable", requireTLS: true, insecureMX: true, skip: errRequireTLSMXNotValid},
		{name: "REQUIRETLS with secure MX: Web PKI required", requireTLS: true, server: good, required: true, handshake: true},
		{name: "REQUIRETLS with secure MX: untrusted fails", requireTLS: true, server: untrusted, required: true},
		{name: "REQUIRETLS with insecure MX validated by MTA-STS testing", requireTLS: true, insecureMX: true, sts: "testing", server: good, required: true, handshake: true},
		{name: "REQUIRETLS satisfied by DANE", requireTLS: true, dane: daneFor(selfSignedMX), server: func(*testPKI) leafCert { return selfSignedMX }, required: true, handshake: true},
		{name: "caller VerifyConnection may reject", server: untrusted, caller: func(c *tls.Config) {
			c.VerifyConnection = func(tls.ConnectionState) error { return errors.New("caller says no") }
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeResolver(DNSSECSecure)
			mutate := func(o *Options) {
				o.TLSConfig = &tls.Config{RootCAs: p.roots}
				if tc.caller != nil {
					tc.caller(o.TLSConfig)
				}
			}
			var d *Deliverer
			if tc.mode != "" || tc.dane.state != daneNone || tc.dane.err != nil {
				d = daneDeliverer(t, f, tc.mode, mutate)
			} else {
				opts := &Options{Resolver: f.resolver(), DisableLoopElimination: true}
				mutate(opts)
				var err error
				if d, err = New(opts); err != nil {
					t.Fatal(err)
				}
			}
			step := secureStep("mx.example.com")
			if tc.insecureMX {
				step.host.security = DNSSECInsecure
			}
			a := d.planTLS(tlsInput{domain: "example.com", step: step, sts: stsDecision(tc.sts), dane: tc.dane, requireTLS: tc.requireTLS})
			if tc.skip != nil {
				if !errors.Is(a.skip, tc.skip) {
					t.Fatalf("skip = %v, want %v", a.skip, tc.skip)
				}
				return
			}
			if a.required != tc.required {
				t.Errorf("required = %v, want %v", a.required, tc.required)
			}
			sni, err := handshake(t, a, tc.server(p))
			if (err == nil) != tc.handshake {
				t.Errorf("handshake error = %v, want success %v", err, tc.handshake)
			}
			if sni != a.serverName || sni != "mx.example.com" {
				t.Errorf("SNI = %q, want the MX/TLSA base name", sni)
			}
			var stsCause, daneCause error
			for _, pr := range a.report.policies() {
				switch pr.Kind {
				case PolicyMTASTS:
					stsCause = pr.Cause
				case PolicyDANE:
					daneCause = pr.Cause
				}
			}
			if (stsCause != nil) != tc.stsCause {
				t.Errorf("MTA-STS cause = %v, want recorded %v", stsCause, tc.stsCause)
			}
			if (daneCause != nil) != tc.daneCause {
				t.Errorf("DANE cause = %v, want recorded %v", daneCause, tc.daneCause)
			}
		})
	}
}

func TestTLSAttemptDetails(t *testing.T) {
	f := newFakeResolver(DNSSECSecure)
	d := daneDeliverer(t, f, "", nil)
	step := secureStep("mx.example.com")
	selfSignedMX := selfSigned(t, "mx.example.com")
	t.Run("SNI is the TLSA base domain", func(t *testing.T) {
		dane := daneResult{state: daneUsable, baseDomain: "real.example.net", records: []TLSA{tlsaFor(3, 1, 1, selfSignedMX.cert)}, refIDs: []string{"real.example.net"}}
		a := d.planTLS(tlsInput{domain: "example.com", step: step, dane: dane})
		if a.serverName != "real.example.net" || a.config.ServerName != "real.example.net" {
			t.Errorf("serverName = %q / %q, want the TLSA base domain (RFC 7672 §8.1)", a.serverName, a.config.ServerName)
		}
	})
	t.Run("no STARTTLS", func(t *testing.T) {
		opp := d.planTLS(tlsInput{domain: "example.com", step: step, sts: stsDecision("testing")})
		if err := opp.noSTARTTLS(); err != nil {
			t.Errorf("opportunistic attempt without STARTTLS = %v, want cleartext allowed", err)
		}
		if pr := opp.report.policies(); len(pr) == 0 || pr[0].Cause == nil {
			t.Error("MTA-STS testing did not record the missing STARTTLS")
		}
		req := d.planTLS(tlsInput{domain: "example.com", step: step, sts: stsDecision("enforce")})
		if err := req.noSTARTTLS(); !errors.Is(err, errTLSRequired) {
			t.Errorf("required attempt without STARTTLS = %v", err)
		}
	})
	t.Run("caller configuration is not mutated", func(t *testing.T) {
		caller := &tls.Config{ServerName: "caller"}
		d2 := daneDeliverer(t, f, "", func(o *Options) { o.TLSConfig = caller })
		a := d2.planTLS(tlsInput{domain: "example.com", step: step, sts: stsDecision("enforce")})
		if caller.ServerName != "caller" || caller.InsecureSkipVerify || caller.VerifyConnection != nil || a.config == caller {
			t.Error("planTLS changed the caller's tls.Config")
		}
	})
	t.Run("destination cause does not leak into the attempt", func(t *testing.T) {
		dec := stsDecision("enforce")
		dec.result.Cause = fmt.Errorf("refresh failed")
		a := d.planTLS(tlsInput{domain: "example.com", step: step, sts: dec})
		if pr := a.report.policies(); pr[0].Cause != nil {
			t.Errorf("attempt MTA-STS cause = %v, want nil before the handshake", pr[0].Cause)
		}
	})
}

func TestDANEReportSemantics(t *testing.T) {
	f := newFakeResolver(DNSSECSecure)
	step := secureStep("mx.example.com")
	usable := daneResult{state: daneUsable, baseDomain: "mx.example.com", records: []TLSA{{Usage: 3, Selector: 1, MatchingType: 1, Association: make([]byte, 32)}}}
	daneOf := func(a *tlsAttempt) PolicyResult {
		for _, pr := range a.report.policies() {
			if pr.Kind == PolicyDANE {
				return pr
			}
		}
		t.Fatal("no DANE result")
		return PolicyResult{}
	}
	t.Run("a skip caused by DANE is Applied", func(t *testing.T) {
		d := daneDeliverer(t, f, "", nil)
		if pr := daneOf(d.planTLS(tlsInput{domain: "example.com", step: step, dane: daneResult{err: errTLSALookup}})); !pr.Applied || pr.Cause == nil {
			t.Errorf("lookup failure: %+v", pr)
		}
		m := daneDeliverer(t, f, DANEMandatory, nil)
		if pr := daneOf(m.planTLS(tlsInput{domain: "example.com", step: step})); !pr.Applied || pr.Cause == nil {
			t.Errorf("mandatory without records: %+v", pr)
		}
	})
	t.Run("missing STARTTLS is a DANE failure when DANE asked for TLS", func(t *testing.T) {
		for _, mode := range []DANEMode{"", DANEAudit} {
			d := daneDeliverer(t, f, mode, nil)
			a := d.planTLS(tlsInput{domain: "example.com", step: step, dane: usable})
			err := a.noSTARTTLS()
			if pr := daneOf(a); !errors.Is(pr.Cause, errTLSRequired) {
				t.Errorf("mode %q: DANE result %+v, want the missing STARTTLS recorded", mode, pr)
			}
			if (err != nil) != (mode != DANEAudit) {
				t.Errorf("mode %q: noSTARTTLS = %v", mode, err)
			}
		}
	})
	t.Run("audit mode is never Applied", func(t *testing.T) {
		d := daneDeliverer(t, f, DANEAudit, nil)
		if pr := daneOf(d.planTLS(tlsInput{domain: "example.com", step: step, dane: usable})); pr.Applied {
			t.Errorf("audit: %+v", pr)
		}
	})
}
