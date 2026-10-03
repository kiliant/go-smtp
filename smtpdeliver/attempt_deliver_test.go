package smtpdeliver

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/smtpclient"
)

const testBody = "Subject: hi\r\n\r\nhello\r\n"

// engine is a Deliverer wired to a fake resolver and a fake network.
type engine struct {
	d     *Deliverer
	dns   *fakeResolver
	net   *network
	opens atomic.Int32
}

func newEngine(t *testing.T, mutate func(*Options)) *engine {
	t.Helper()
	e := &engine{dns: newFakeResolver(DNSSECInsecure), net: newNetwork()}
	opts := &Options{Resolver: e.dns.resolver(), Dial: e.net.dial, DisableLoopElimination: true, Identity: "sender.test"}
	if mutate != nil {
		mutate(opts)
	}
	d, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	e.d = d
	return e
}

// mx publishes domain's MX hosts, each with one address served by a peer.
func (e *engine) mx(domain string, hosts ...any) {
	answer := MXLookup{State: LookupFound, Security: DNSSECInsecure}
	for i := 0; i < len(hosts); i += 3 {
		name, addr := hosts[i+1].(string), hosts[i+2].(string)
		answer.Records = append(answer.Records, MX{Preference: uint16(hosts[i].(int)), Host: name})
		e.dns.setIP(name, found(addr), nil)
	}
	e.dns.setMX(domain, answer, nil)
}

func (e *engine) source(body string) MessageSource {
	return MessageSource{Open: func(context.Context, *OpenMessageOptions) (io.ReadCloser, error) {
		e.opens.Add(1)
		return io.NopCloser(strings.NewReader(body)), nil
	}}
}

func (e *engine) deliver(t *testing.T, ctx context.Context, rcpts ...string) (Result, error) {
	t.Helper()
	return e.d.Deliver(ctx, &Request{
		EnvelopeFrom: "sender@sender.test",
		Message:      e.source(testBody),
		Destinations: oneDestination("example.com", rcpts...),
	}, nil)
}

func dispositions(dr DestinationResult) string {
	var out []string
	for _, r := range dr.Recipients {
		out = append(out, string(r.Disposition))
	}
	return strings.Join(out, ",")
}

func TestDeliverDelivered(t *testing.T) {
	e := newEngine(t, nil)
	p := newFakePeer(t, "PIPELINING", "ENHANCEDSTATUSCODES")
	e.net.add("192.0.2.1", p)
	e.mx("example.com", 10, "mx.example.com", "192.0.2.1")
	var events []Event
	e.d.trace = func(ev Event) { events = append(events, ev) }

	res, err := e.deliver(t, context.Background(), "a@example.com", "b@example.com")
	if err != nil {
		t.Fatal(err)
	}
	dr := res.Destinations[0]
	if dispositions(dr) != "delivered,delivered" || len(dr.Attempts) != 1 || dr.Attempts[0].Stage != StageComplete || dr.Attempts[0].Cause != nil {
		t.Fatalf("result = %+v", dr)
	}
	out := dr.Recipients[0]
	if out.Reply == nil || out.Reply.Code != 250 || !strings.Contains(out.Reply.Text, "queued as Q1") || out.Status.Raw != "2.0.0" || out.Attempt != 0 {
		t.Errorf("outcome = %+v; the positive reply carries the queue id and must be kept", out)
	}
	got := p.delivered()
	if len(got) != 1 || len(got[0].rcpts) != 2 || got[0].body != testBody {
		t.Errorf("server received %+v; want one copy for both recipients (RFC 5321 §4.5.4.1)", got)
	}
	if e.opens.Load() != 1 {
		t.Errorf("Open called %d times", e.opens.Load())
	}
	var sawAttempt bool
	for _, ev := range events {
		sawAttempt = sawAttempt || ev.Kind == EventAttempt
	}
	if !sawAttempt {
		t.Error("no EventAttempt traced")
	}
}

func TestDeliverClassification(t *testing.T) {
	cases := []struct {
		name  string
		setup func(e *engine, p1, p2 *fakePeer)
		rcpts []string
		want  string
		check func(t *testing.T, dr DestinationResult, p1, p2 *fakePeer)
	}{
		{"permanent MAIL reply is final for everyone", func(e *engine, p1, p2 *fakePeer) {
			p1.mail = func(string) string { return "550 5.7.1 sender rejected" }
		}, []string{"a@example.com", "b@example.com"}, "permanent-failure,permanent-failure", func(t *testing.T, dr DestinationResult, p1, p2 *fakePeer) {
			if r := dr.Recipients[0].Reply; r == nil || r.Command != "MAIL" || r.Code != 550 || r.Recipient != "a@example.com" {
				t.Errorf("reply = %+v", r)
			}
			if p2.sessionCount() != 0 {
				t.Error("a permanent MAIL reply moved on to another MX")
			}
		}},
		{"permanent RCPT is final for that recipient only", func(e *engine, p1, p2 *fakePeer) {
			p1.rcpt = func(a string) string {
				if a == "b@example.com" {
					return "550 5.1.1 no such user"
				}
				return "250 ok"
			}
		}, []string{"a@example.com", "b@example.com"}, "delivered,permanent-failure", nil},
		{"permanent DATA reply is final for every accepted recipient", func(e *engine, p1, p2 *fakePeer) {
			p1.data = func(string) string { return "554 5.6.0 content rejected" }
		}, []string{"a@example.com", "b@example.com"}, "permanent-failure,permanent-failure", func(t *testing.T, dr DestinationResult, p1, p2 *fakePeer) {
			if p2.sessionCount() != 0 {
				t.Error("a permanent DATA reply moved on to another MX")
			}
		}},
		{"4yz MAIL fails over to the next MX", func(e *engine, p1, p2 *fakePeer) {
			p1.mail = func(string) string { return "451 4.3.0 try later" }
		}, []string{"a@example.com"}, "delivered", func(t *testing.T, dr DestinationResult, p1, p2 *fakePeer) {
			if len(dr.Attempts) != 2 || dr.Recipients[0].Attempt != 1 {
				t.Errorf("attempts = %+v", dr.Attempts)
			}
		}},
		{"temporary everywhere: temporary with the last reply", func(e *engine, p1, p2 *fakePeer) {
			p1.rcpt = func(string) string { return "450 4.2.1 mailbox busy" }
			p2.rcpt = func(string) string { return "452 4.2.2 over quota" }
		}, []string{"a@example.com"}, "temporary-failure", func(t *testing.T, dr DestinationResult, p1, p2 *fakePeer) {
			if r := dr.Recipients[0].Reply; r == nil || r.Code != 452 || dr.Recipients[0].Attempt != 1 {
				t.Errorf("outcome = %+v, want the last temporary reply", dr.Recipients[0])
			}
		}},
		{"mixed RCPT across MX hosts: no copy is sent twice", func(e *engine, p1, p2 *fakePeer) {
			p1.rcpt = func(a string) string {
				if a == "b@example.com" {
					return "451 4.3.0 later"
				}
				return "250 ok"
			}
		}, []string{"a@example.com", "b@example.com"}, "delivered,delivered", func(t *testing.T, dr DestinationResult, p1, p2 *fakePeer) {
			d1, d2 := p1.delivered(), p2.delivered()
			if len(d1) != 1 || strings.Join(d1[0].rcpts, ",") != "a@example.com" {
				t.Errorf("MX1 received %+v", d1)
			}
			if len(d2) != 1 || strings.Join(d2[0].rcpts, ",") != "b@example.com" {
				t.Errorf("MX2 received %+v; a must not be re-sent", d2)
			}
		}},
		{"lost final reply is indeterminate and never retried", func(e *engine, p1, p2 *fakePeer) {
			p1.dropAfterDot = true
		}, []string{"a@example.com", "b@example.com"}, "indeterminate,indeterminate", func(t *testing.T, dr DestinationResult, p1, p2 *fakePeer) {
			if p2.sessionCount() != 0 {
				t.Error("an indeterminate recipient was offered to another MX: possible duplicate delivery")
			}
			if dr.Recipients[0].Cause == nil {
				t.Error("indeterminate outcome without its cause")
			}
		}},
		{"duplicate recipients keep separate outcomes", nil, []string{"a@example.com", "a@example.com"}, "delivered,delivered", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEngine(t, nil)
			p1, p2 := newFakePeer(t, "PIPELINING", "ENHANCEDSTATUSCODES"), newFakePeer(t, "PIPELINING", "ENHANCEDSTATUSCODES")
			e.net.add("192.0.2.1", p1)
			e.net.add("192.0.2.2", p2)
			e.mx("example.com", 10, "mx1.example.com", "192.0.2.1", 20, "mx2.example.com", "192.0.2.2")
			if tc.setup != nil {
				tc.setup(e, p1, p2)
			}
			res, err := e.deliver(t, context.Background(), tc.rcpts...)
			if err != nil {
				t.Fatal(err)
			}
			dr := res.Destinations[0]
			if got := dispositions(dr); got != tc.want {
				t.Fatalf("dispositions = %s, want %s (%+v)", got, tc.want, dr)
			}
			if tc.check != nil {
				tc.check(t, dr, p1, p2)
			}
		})
	}
}

func TestDeliverMixedRCPTOpensTwice(t *testing.T) {
	e := newEngine(t, nil)
	p1, p2 := newFakePeer(t), newFakePeer(t)
	p1.rcpt = func(a string) string {
		if a == "b@example.com" {
			return "451 later"
		}
		return "250 ok"
	}
	e.net.add("192.0.2.1", p1)
	e.net.add("192.0.2.2", p2)
	e.mx("example.com", 10, "mx1.example.com", "192.0.2.1", 20, "mx2.example.com", "192.0.2.2")
	if _, err := e.deliver(t, context.Background(), "a@example.com", "b@example.com"); err != nil {
		t.Fatal(err)
	}
	if e.opens.Load() != 2 {
		t.Errorf("Open called %d times, want once per content transfer", e.opens.Load())
	}
}

func TestDeliverConnectionFailover(t *testing.T) {
	e := newEngine(t, nil)
	p2 := newFakePeer(t)
	e.net.add("192.0.2.2", p2) // 192.0.2.1 refuses
	e.mx("example.com", 10, "mx1.example.com", "192.0.2.1", 20, "mx2.example.com", "192.0.2.2")
	res, err := e.deliver(t, context.Background(), "a@example.com")
	if err != nil {
		t.Fatal(err)
	}
	dr := res.Destinations[0]
	if dispositions(dr) != "delivered" || len(dr.Attempts) != 2 || dr.Attempts[0].Stage != StageConnect || dr.Attempts[0].Cause == nil {
		t.Fatalf("result = %+v", dr)
	}
}

func TestDeliverRoutingOutcomes(t *testing.T) {
	e := newEngine(t, nil)
	e.dns.setMX("example.com", mxs(0, "."), nil)
	res, err := e.deliver(t, context.Background(), "a@example.com")
	if err != nil {
		t.Fatal(err)
	}
	out := res.Destinations[0].Recipients[0]
	if out.Disposition != DispositionPermanent || out.Status.Raw != "5.1.10" || out.Attempt != -1 || !errors.Is(out.Cause, errNullMX) {
		t.Errorf("null MX outcome = %+v", out)
	}
	if len(e.net.dialed()) != 0 {
		t.Error("dialled for a null-MX domain")
	}
}

func TestDeliverMTASTS(t *testing.T) {
	t.Run("enforce: an MX outside the policy is skipped; exhaustion stays temporary", func(t *testing.T) {
		cache := newScriptedCache()
		e := newEngine(t, func(o *Options) { o.MTASTS = &MTASTSOptions{Cache: cache.cache()} })
		e.d.mtasts.now = time.Now
		cache.put(t, "id1", "version: STSv1\nmode: enforce\nmx: good.example.com\nmax_age: 604800\n", time.Now().Add(-time.Hour))
		p := newFakePeer(t)
		e.net.add("192.0.2.1", p)
		e.mx("example.com", 10, "evil.example.com", "192.0.2.1")
		res, err := e.deliver(t, context.Background(), "a@example.com")
		if err != nil {
			t.Fatal(err)
		}
		dr := res.Destinations[0]
		if dispositions(dr) != "temporary-failure" || p.sessionCount() != 0 {
			t.Fatalf("result = %+v, sessions %d; RFC 8461 §5 makes enforce failures temporary", dr, p.sessionCount())
		}
		if len(dr.Attempts) != 1 || dr.Attempts[0].Stage != StageResolve || !errors.Is(dr.Attempts[0].Cause, errMTASTSMXMismatch) {
			t.Errorf("attempts = %+v", dr.Attempts)
		}
		if len(dr.Policies) != 1 || dr.Policies[0].Kind != PolicyMTASTS || !dr.Policies[0].Applied {
			t.Errorf("destination policies = %+v", dr.Policies)
		}
	})
	t.Run("cache load failure defers the destination before DNS", func(t *testing.T) {
		cache := newScriptedCache()
		cache.loadErr = errors.New("db down")
		e := newEngine(t, func(o *Options) { o.MTASTS = &MTASTSOptions{Cache: cache.cache()} })
		res, err := e.deliver(t, context.Background(), "a@example.com")
		if err != nil {
			t.Fatal(err)
		}
		out := res.Destinations[0].Recipients[0]
		if out.Disposition != DispositionTemporary || out.Status.Raw != "4.3.0" {
			t.Errorf("outcome = %+v", out)
		}
		for _, q := range e.dns.queried() {
			if strings.HasPrefix(q, "MX ") {
				t.Error("MX lookup ran although the policy cache was unreadable")
			}
		}
	})
}

func TestDeliverRequireTLS(t *testing.T) {
	reqTLS := &smtp.MailOptions{Delivery: &smtp.DeliveryOptions{RequireTLS: true}}
	// A Web PKI-trusted certificate, so that only the property under test
	// can fail: REQUIRETLS demands an authenticated session.
	pki := newTestPKI(t)
	cert := pki.leaf(t, "mx.example.com", []string{"mx.example.com"}, false).tlsCert()
	trusted := func(o *Options) { o.TLSConfig = &tls.Config{RootCAs: pki.roots} }
	cases := []struct {
		name   string
		ext    []string
		cert   bool
		secure bool
		status string
	}{
		{"insecure MX without MTA-STS", []string{"STARTTLS", "REQUIRETLS"}, true, false, "5.7.10"},
		{"no STARTTLS", []string{"REQUIRETLS"}, false, true, "5.7.10"},
		{"TLS but no REQUIRETLS", []string{"STARTTLS"}, true, true, "5.7.30"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEngine(t, trusted)
			p := newFakePeer(t, tc.ext...)
			if tc.cert {
				p.cert = &cert
			}
			e.net.add("192.0.2.1", p)
			e.mx("example.com", 10, "mx.example.com", "192.0.2.1")
			if tc.secure {
				a := mxs(10, "mx.example.com")
				a.Security = DNSSECSecure
				e.dns.setMX("example.com", a, nil)
			}
			res, err := e.d.Deliver(context.Background(), &Request{EnvelopeFrom: "s@sender.test", MailOptions: reqTLS, Message: e.source(testBody), Destinations: oneDestination("example.com", "a@example.com")}, nil)
			if err != nil {
				t.Fatal(err)
			}
			out := res.Destinations[0].Recipients[0]
			if out.Disposition != DispositionPermanent || out.Status.Raw != tc.status {
				t.Errorf("outcome = %+v, want permanent %s (RFC 8689 §4.2.1)", out, tc.status)
			}
			if len(p.delivered()) != 0 {
				t.Error("a REQUIRETLS message was transmitted without meeting its requirements")
			}
		})
	}
	t.Run("requirements met: delivered with REQUIRETLS", func(t *testing.T) {
		e := newEngine(t, trusted)
		p := newFakePeer(t, "STARTTLS", "REQUIRETLS")
		p.cert = &cert
		e.net.add("192.0.2.1", p)
		e.mx("example.com", 10, "mx.example.com", "192.0.2.1")
		a := mxs(10, "mx.example.com")
		a.Security = DNSSECSecure
		e.dns.setMX("example.com", a, nil)
		res, err := e.d.Deliver(context.Background(), &Request{MailOptions: reqTLS, Message: e.source(testBody), Destinations: oneDestination("example.com", "a@example.com")}, nil)
		if err != nil || dispositions(res.Destinations[0]) != "delivered" {
			t.Fatalf("result = %+v, %v", res, err)
		}
		var mail string
		for _, l := range p.received() {
			if strings.HasPrefix(l, "MAIL") {
				mail = l
			}
		}
		if !strings.Contains(mail, "REQUIRETLS") {
			t.Errorf("MAIL line %q lacks REQUIRETLS", mail)
		}
	})
	t.Run("a refused connection leaves the ordinary temporary outcome", func(t *testing.T) {
		e := newEngine(t, nil)
		a := mxs(10, "mx.example.com")
		a.Security = DNSSECSecure
		e.dns.setMX("example.com", a, nil)
		e.dns.setIP("mx.example.com", found("192.0.2.9"), nil)
		res, _ := e.d.Deliver(context.Background(), &Request{MailOptions: reqTLS, Message: e.source(testBody), Destinations: oneDestination("example.com", "a@example.com")}, nil)
		if out := res.Destinations[0].Recipients[0]; out.Disposition != DispositionTemporary {
			t.Errorf("outcome = %+v", out)
		}
	})
}

func TestDeliverOpportunisticTLS(t *testing.T) {
	e := newEngine(t, nil)
	p := newFakePeer(t, "STARTTLS")
	cert := selfSigned(t, "mx.example.com").tlsCert()
	p.cert = &cert
	e.net.add("192.0.2.1", p)
	e.mx("example.com", 10, "mx.example.com", "192.0.2.1")
	res, err := e.deliver(t, context.Background(), "a@example.com")
	if err != nil || dispositions(res.Destinations[0]) != "delivered" {
		t.Fatalf("result = %+v, %v; unauthenticated opportunistic TLS (RFC 7435) must deliver", res, err)
	}
	var sawSTARTTLS bool
	for _, l := range p.received() {
		sawSTARTTLS = sawSTARTTLS || l == "STARTTLS"
	}
	if !sawSTARTTLS {
		t.Error("STARTTLS was advertised but not used")
	}
}

func TestDeliverSIZEOnlyWhenAdvertised(t *testing.T) {
	size := int64(len(testBody))
	for _, ext := range [][]string{nil, {"SIZE 100000"}} {
		e := newEngine(t, nil)
		p := newFakePeer(t, ext...)
		e.net.add("192.0.2.1", p)
		e.mx("example.com", 10, "mx.example.com", "192.0.2.1")
		src := e.source(testBody)
		src.Size = &size
		res, err := e.d.Deliver(context.Background(), &Request{Message: src, Destinations: oneDestination("example.com", "a@example.com")}, nil)
		if err != nil || dispositions(res.Destinations[0]) != "delivered" {
			t.Fatalf("ext %v: %+v, %v", ext, res, err)
		}
		var mail string
		for _, l := range p.received() {
			if strings.HasPrefix(l, "MAIL") {
				mail = l
			}
		}
		if want := len(ext) > 0; strings.Contains(mail, "SIZE=") != want {
			t.Errorf("ext %v: MAIL line %q, want SIZE sent %v (RFC 1870)", ext, mail, want)
		}
	}
}

func TestDeliverCallErrors(t *testing.T) {
	t.Run("cancelled before any work", func(t *testing.T) {
		e := newEngine(t, nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := e.deliver(t, ctx, "a@example.com"); !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("cancelled mid-destination: partial result, untouched recipients not attempted", func(t *testing.T) {
		e := newEngine(t, nil)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r := e.dns.resolver()
		lookupIP := r.LookupIP
		r.LookupIP = func(c context.Context, req *LookupIPRequest) (IPLookup, error) {
			cancel()
			return lookupIP(c, req)
		}
		e.d.resolver = r
		e.mx("example.com", 10, "mx.example.com", "192.0.2.1")
		res, err := e.d.Deliver(ctx, &Request{Message: e.source(testBody), Destinations: append(oneDestination("example.com", "a@example.com"), oneDestination("example.org", "b@example.org")...)}, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
		if len(res.Destinations) != 2 || dispositions(res.Destinations[0]) != "not-attempted" || dispositions(res.Destinations[1]) != "not-attempted" {
			t.Errorf("result = %+v", res)
		}
	})
	t.Run("message source failure", func(t *testing.T) {
		e := newEngine(t, nil)
		p := newFakePeer(t)
		e.net.add("192.0.2.1", p)
		e.mx("example.com", 10, "mx.example.com", "192.0.2.1")
		srcErr := errors.New("spool unreadable")
		res, err := e.d.Deliver(context.Background(), &Request{Message: MessageSource{Open: func(context.Context, *OpenMessageOptions) (io.ReadCloser, error) {
			return nil, srcErr
		}}, Destinations: oneDestination("example.com", "a@example.com")}, nil)
		if !errors.Is(err, srcErr) || !errors.Is(err, errMessageSource) {
			t.Fatalf("err = %v", err)
		}
		if out := res.Destinations[0].Recipients[0]; out.Disposition != DispositionNotAttempted || len(p.delivered()) != 0 {
			t.Errorf("outcome = %+v", out)
		}
	})
	t.Run("message reader failure mid-content is a source failure, not a remote one", func(t *testing.T) {
		e := newEngine(t, nil)
		p := newFakePeer(t)
		e.net.add("192.0.2.1", p)
		e.mx("example.com", 10, "mx.example.com", "192.0.2.1")
		srcErr := errors.New("disk read error")
		res, err := e.d.Deliver(context.Background(), &Request{Message: MessageSource{Open: func(context.Context, *OpenMessageOptions) (io.ReadCloser, error) {
			return io.NopCloser(io.MultiReader(strings.NewReader("partial"), &errReader{srcErr})), nil
		}}, Destinations: oneDestination("example.com", "a@example.com")}, nil)
		if !errors.Is(err, srcErr) {
			t.Fatalf("err = %v", err)
		}
		if out := res.Destinations[0].Recipients[0]; out.Disposition != DispositionNotAttempted {
			t.Errorf("outcome = %+v", out)
		}
	})
}

type errReader struct{ err error }

func (r *errReader) Read([]byte) (int, error) { return 0, r.err }

func TestDeliverMultipleDestinations(t *testing.T) {
	e := newEngine(t, nil)
	p := newFakePeer(t)
	e.net.add("192.0.2.1", p)
	e.mx("example.com", 10, "mx.example.com", "192.0.2.1")
	e.dns.setMX("example.org", mxs(0, "."), nil)
	res, err := e.d.Deliver(context.Background(), &Request{Message: e.source(testBody), Destinations: append(oneDestination("example.org", "x@example.org"), oneDestination("example.com", "a@example.com")...)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Destinations) != 2 || res.Destinations[0].Domain != "example.org" || dispositions(res.Destinations[0]) != "permanent-failure" || dispositions(res.Destinations[1]) != "delivered" {
		t.Errorf("result = %+v; destinations are independent and in request order", res)
	}
}

func TestDeliverDoesNotLeakGoroutines(t *testing.T) {
	before := runtime.NumGoroutine()
	func() {
		e := newEngine(t, nil)
		p := newFakePeer(t, "PIPELINING")
		e.net.add("192.0.2.1", p)
		e.mx("example.com", 10, "mx.example.com", "192.0.2.1")
		for range 5 {
			if _, err := e.deliver(t, context.Background(), "a@example.com"); err != nil {
				t.Fatal(err)
			}
		}
		_ = p.ln.Close()
		p.wg.Wait()
	}()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before+2 {
		t.Errorf("goroutines: %d before, %d after", before, n)
	}
}

// TestApplyContentResultLMTPPrefix covers the LMTP row through the mapping
// function directly: no public route selects LMTP yet (T29 spec, S8), and a
// later route field would reach this same code.
func TestApplyContentResultLMTPPrefix(t *testing.T) {
	d, _ := New(&Options{DisableLoopElimination: true})
	in := attemptInput{recipients: []Recipient{{Address: "a@x"}, {Address: "b@x"}, {Address: "c@x"}}, indices: []int{0, 1, 2}}
	accepted := []int{0, 1, 2}
	unknown := &smtp.Error{Command: "DATA", Err: smtpclientUnknownForTest()}
	prefix := smtp.DataResult{{Recipient: "a@x", Command: "DATA", Code: 250}, {Recipient: "b@x", Command: "DATA", Code: 550}}
	out := d.applyContentResult(context.Background(), attemptOutcome{}, in, accepted, prefix, unknown)
	var got []string
	for _, dec := range out.decisions {
		got = append(got, string(dec.disposition))
	}
	if strings.Join(got, ",") != "delivered,permanent-failure,indeterminate" {
		t.Errorf("decisions = %v; the received prefix is authoritative and the rest is indeterminate", got)
	}

	coded := &smtp.Error{Command: "DATA", Code: 451, Text: "later"}
	out = d.applyContentResult(context.Background(), attemptOutcome{}, in, accepted, nil, coded)
	for _, dec := range out.decisions {
		if dec.disposition != DispositionTemporary || dec.reply == nil || dec.reply.Code != 451 || dec.reply.Recipient == "" {
			t.Errorf("coded 4yz decision = %+v", dec)
		}
	}
	transport := &smtp.Error{Command: "DATA", Err: io.ErrUnexpectedEOF}
	out = d.applyContentResult(context.Background(), attemptOutcome{}, in, accepted, nil, transport)
	for _, dec := range out.decisions {
		if dec.disposition != DispositionTemporary {
			t.Errorf("transport failure before completion = %+v, want temporary", dec)
		}
	}
}

func smtpclientUnknownForTest() error {
	return fmt.Errorf("lost: %w", smtpclient.ErrFinalStatusUnknown)
}

// A failed advertised STARTTLS is never followed by cleartext on the same
// address (DELIVERY-DESIGN.md §6). A 454 reply leaves the session usable, so
// this is the case where falling back would actually transmit.
func TestDeliverNoCleartextAfterFailedSTARTTLS(t *testing.T) {
	e := newEngine(t, nil)
	p := newFakePeer(t, "STARTTLS") // no certificate: STARTTLS answers 454
	e.net.add("192.0.2.1", p)
	e.mx("example.com", 10, "mx.example.com", "192.0.2.1")
	res, err := e.deliver(t, context.Background(), "a@example.com")
	if err != nil {
		t.Fatal(err)
	}
	dr := res.Destinations[0]
	if dispositions(dr) != "temporary-failure" || len(p.delivered()) != 0 {
		t.Fatalf("result = %+v, deliveries %d; the message went out in cleartext after STARTTLS failed", dr, len(p.delivered()))
	}
	if dr.Attempts[0].Stage != StageTLS {
		t.Errorf("stage = %s, want tls", dr.Attempts[0].Stage)
	}
}

func TestDeliverMandatoryDANEInsecureMX(t *testing.T) {
	e := newEngine(t, func(o *Options) { o.DANE = &DANEOptions{Mode: DANEMandatory} })
	p := newFakePeer(t)
	e.net.add("192.0.2.1", p)
	e.mx("example.com", 10, "mx.example.com", "192.0.2.1") // insecure answers
	res, err := e.deliver(t, context.Background(), "a@example.com")
	if err != nil {
		t.Fatal(err)
	}
	dr := res.Destinations[0]
	if dispositions(dr) != "temporary-failure" || p.sessionCount() != 0 {
		t.Fatalf("result = %+v", dr)
	}
	if len(dr.Policies) != 1 || dr.Policies[0].Kind != PolicyDANE || !errors.Is(dr.Policies[0].Cause, errDANEInsecureMX) {
		t.Errorf("destination policies = %+v, want the mandatory-DANE failure recorded once", dr.Policies)
	}
}
