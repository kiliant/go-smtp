package smtpdeliver

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

// DNS wire constants (RFC 1035 §3.2.2, §4.1.1; RFC 3596).
const (
	dnsTypeA     = 1
	dnsTypeMX    = 15
	dnsTypeTXT   = 16
	dnsTypeAAAA  = 28
	rcodeOK      = 0
	rcodeSrvFail = 2
	rcodeNXName  = 3
)

type stubRR struct {
	rrType uint16
	rdata  []byte
}

type stubAnswer struct {
	rcode int
	rrs   []stubRR
}

// dnsStub answers queries from a table, over the TCP framing the Go resolver
// uses for a Dial-returned net.Conn that is not a PacketConn (RFC 1035
// §4.2.2). No network is involved.
type dnsStub struct {
	t       *testing.T
	mu      sync.Mutex
	answers map[string]stubAnswer // "TYPE name." → answer; missing is NXDOMAIN
	queries []string
}

func newStubResolver(t *testing.T, answers map[string]stubAnswer) (*net.Resolver, *dnsStub) {
	stub := &dnsStub{t: t, answers: answers}
	r := &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			client, server := net.Pipe()
			go stub.serve(server)
			return client, nil
		},
	}
	return r, stub
}

func (s *dnsStub) serve(conn net.Conn) {
	defer conn.Close()
	for {
		var length [2]byte
		if _, err := io.ReadFull(conn, length[:]); err != nil {
			return
		}
		msg := make([]byte, binary.BigEndian.Uint16(length[:]))
		if _, err := io.ReadFull(conn, msg); err != nil {
			return
		}
		resp := s.respond(msg)
		out := binary.BigEndian.AppendUint16(nil, uint16(len(resp)))
		if _, err := conn.Write(append(out, resp...)); err != nil {
			return
		}
	}
}

func (s *dnsStub) respond(query []byte) []byte {
	if len(query) < 12 {
		s.t.Errorf("short DNS query")
		return nil
	}
	name, end := readName(query, 12)
	qtype := binary.BigEndian.Uint16(query[end:])
	question := query[12 : end+4]
	key := typeName(qtype) + " " + name
	s.mu.Lock()
	s.queries = append(s.queries, key)
	answer, ok := s.answers[key]
	s.mu.Unlock()
	if !ok {
		answer = stubAnswer{rcode: rcodeNXName}
	}
	// QR, RD and RA set (RFC 1035 §4.1.1), so the resolver does not treat an
	// empty answer as a lame referral.
	flags := uint16(0x8180) | uint16(answer.rcode)
	msg := binary.BigEndian.AppendUint16(nil, binary.BigEndian.Uint16(query))
	msg = binary.BigEndian.AppendUint16(msg, flags)
	msg = binary.BigEndian.AppendUint16(msg, 1)
	msg = binary.BigEndian.AppendUint16(msg, uint16(len(answer.rrs)))
	msg = binary.BigEndian.AppendUint16(msg, 0)
	msg = binary.BigEndian.AppendUint16(msg, 0)
	msg = append(msg, question...)
	for _, rr := range answer.rrs {
		msg = append(msg, 0xc0, 0x0c) // name: pointer to the question
		msg = binary.BigEndian.AppendUint16(msg, rr.rrType)
		msg = binary.BigEndian.AppendUint16(msg, 1) // class IN
		msg = binary.BigEndian.AppendUint32(msg, 300)
		msg = binary.BigEndian.AppendUint16(msg, uint16(len(rr.rdata)))
		msg = append(msg, rr.rdata...)
	}
	return msg
}

func readName(msg []byte, off int) (string, int) {
	var labels []string
	for msg[off] != 0 {
		n := int(msg[off])
		labels = append(labels, string(msg[off+1:off+1+n]))
		off += 1 + n
	}
	return strings.ToLower(strings.Join(labels, ".")) + ".", off + 1
}

func typeName(t uint16) string {
	switch t {
	case dnsTypeA:
		return "A"
	case dnsTypeMX:
		return "MX"
	case dnsTypeTXT:
		return "TXT"
	case dnsTypeAAAA:
		return "AAAA"
	}
	return "TYPE?"
}

func encodeName(name string) []byte {
	var out []byte
	for _, label := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}
	return append(out, 0)
}

func mxRR(pref uint16, host string) stubRR {
	return stubRR{dnsTypeMX, append(binary.BigEndian.AppendUint16(nil, pref), encodeName(host)...)}
}

func addrRR(s string) stubRR {
	a := netip.MustParseAddr(s)
	if a.Is4() {
		b := a.As4()
		return stubRR{dnsTypeA, b[:]}
	}
	b := a.As16()
	return stubRR{dnsTypeAAAA, b[:]}
}

func txtRR(parts ...string) stubRR {
	var rdata []byte
	for _, p := range parts {
		rdata = append(rdata, byte(len(p)))
		rdata = append(rdata, p...)
	}
	return stubRR{dnsTypeTXT, rdata}
}

func TestStandardResolverMX(t *testing.T) {
	r, stub := newStubResolver(t, map[string]stubAnswer{
		"MX mail.example.":   {rrs: []stubRR{mxRR(20, "mx2.mail.example."), mxRR(10, "mx1.mail.example.")}},
		"MX nodata.example.": {rcode: rcodeOK},
		"MX broken.example.": {rcode: rcodeSrvFail},
	})
	std := standardResolver(r)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := std.LookupMX(ctx, &LookupMXRequest{Name: "mail.example"})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != LookupFound || got.Security != DNSSECUnvalidated || len(got.Records) != 2 {
		t.Fatalf("MX answer = %+v", got)
	}
	hosts := map[string]uint16{}
	for _, mx := range got.Records {
		hosts[mx.Host] = mx.Preference
	}
	if hosts["mx1.mail.example"] != 10 || hosts["mx2.mail.example"] != 20 {
		t.Errorf("MX records = %+v, want trailing dots stripped and preferences kept", got.Records)
	}

	// NODATA and NXDOMAIN are indistinguishable through the standard library;
	// both become an empty answer so the implicit MX applies (RFC 5321 §5.1).
	for _, name := range []string{"nodata.example", "missing.example"} {
		got, err := std.LookupMX(ctx, &LookupMXRequest{Name: name})
		if err != nil || got.State != LookupFound || len(got.Records) != 0 {
			t.Errorf("MX %s = %+v, %v; want an empty found answer", name, got, err)
		}
	}

	if _, err := std.LookupMX(ctx, &LookupMXRequest{Name: "broken.example"}); err == nil {
		t.Error("SERVFAIL MX lookup succeeded, want a temporary error")
	} else if isNotFound(err) {
		t.Errorf("SERVFAIL reported as not found: %v", err)
	}

	for _, q := range stub.queries {
		if !strings.HasSuffix(q, ".example.") {
			t.Errorf("query %q: a search suffix was applied to an absolute name", q)
		}
	}
}

func TestStandardResolverIP(t *testing.T) {
	r, _ := newStubResolver(t, map[string]stubAnswer{
		"A mx.example.":        {rrs: []stubRR{addrRR("192.0.2.1")}},
		"AAAA mx.example.":     {rrs: []stubRR{addrRR("2001:db8::1")}},
		"A broken.example.":    {rcode: rcodeSrvFail},
		"AAAA broken.example.": {rcode: rcodeSrvFail},
	})
	std := standardResolver(r)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := std.LookupIP(ctx, &LookupIPRequest{Name: "mx.example"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[netip.Addr]bool{netip.MustParseAddr("192.0.2.1"): true, netip.MustParseAddr("2001:db8::1"): true}
	if got.State != LookupFound || got.Security != DNSSECUnvalidated || len(got.Addresses) != 2 || !want[got.Addresses[0]] || !want[got.Addresses[1]] {
		t.Fatalf("IP answer = %+v", got)
	}
	if got, err := std.LookupIP(ctx, &LookupIPRequest{Name: "missing.example"}); err != nil || got.State != LookupNotFound {
		t.Errorf("IP missing = %+v, %v; want not found", got, err)
	}
	if _, err := std.LookupIP(ctx, &LookupIPRequest{Name: "broken.example"}); err == nil {
		t.Error("SERVFAIL address lookup succeeded, want a temporary error")
	}
}

func TestStandardResolverTXT(t *testing.T) {
	r, _ := newStubResolver(t, map[string]stubAnswer{
		"TXT _mta-sts.example.": {rrs: []stubRR{txtRR("v=STSv1; ", "id=20261003")}},
	})
	std := standardResolver(r)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	got, err := std.LookupTXT(ctx, &LookupTXTRequest{Name: "_mta-sts.example"})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != LookupFound || len(got.Records) != 1 || got.Records[0] != "v=STSv1; id=20261003" {
		t.Fatalf("TXT answer = %+v, want one record with its character-strings concatenated (RFC 8461 §3.1)", got)
	}
	if got, err := std.LookupTXT(ctx, &LookupTXTRequest{Name: "_mta-sts.missing.example"}); err != nil || got.State != LookupNotFound {
		t.Errorf("TXT missing = %+v, %v; want not found", got, err)
	}
}

// An interrupted lookup must surface as the context error even when the
// standard library hands back an answer (here, NXDOMAIN, which the MX adapter
// would otherwise turn into an empty RRset and the implicit MX).
func TestStandardResolverHonoursContext(t *testing.T) {
	r, _ := newStubResolver(t, nil)
	std := standardResolver(r)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := std.LookupMX(ctx, &LookupMXRequest{Name: "mail.example"}); !errors.Is(err, context.Canceled) {
		t.Errorf("MX with a cancelled context = %+v, %v; want context.Canceled", got, err)
	}
	if got, err := std.LookupIP(ctx, &LookupIPRequest{Name: "mx.example"}); !errors.Is(err, context.Canceled) {
		t.Errorf("IP with a cancelled context = %+v, %v; want context.Canceled", got, err)
	}
	if got, err := std.LookupTXT(ctx, &LookupTXTRequest{Name: "_mta-sts.example"}); !errors.Is(err, context.Canceled) {
		t.Errorf("TXT with a cancelled context = %+v, %v; want context.Canceled", got, err)
	}
}
