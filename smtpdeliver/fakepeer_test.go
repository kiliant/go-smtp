package smtpdeliver

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
)

// fakePeer is the shared scripted SMTP server for smtpdeliver tests. T29 owns
// its structure; T30 appends hostile behaviours and must not delete another
// task's (docs/tasks/BOARD.md). It listens on loopback TCP, which buffers in
// the kernel, so TLS failures cannot deadlock the way net.Pipe can.
type fakePeer struct {
	t  *testing.T
	ln net.Listener

	// Configuration; set before the first connection.
	ext      []string // EHLO keywords after the greeting line
	cert     *tls.Certificate
	greeting string
	mail     func(from string) string
	rcpt     func(addr string) string
	data     func(body string) string
	// dropAfterDot closes the connection after the DATA terminator without a
	// reply: the lost-final-reply case.
	dropAfterDot bool
	// dropOnDATA closes the connection on the DATA command, before 354.
	dropOnDATA bool

	mu         sync.Mutex
	sessions   int
	deliveries []fakeDelivery
	lines      []string
	wg         sync.WaitGroup
}

type fakeDelivery struct {
	from  string
	rcpts []string
	body  string
}

func newFakePeer(t *testing.T, ext ...string) *fakePeer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &fakePeer{t: t, ln: ln, ext: ext, greeting: "220 fake.test ESMTP"}
	go p.accept()
	t.Cleanup(func() {
		_ = ln.Close()
		p.wg.Wait()
	})
	return p
}

func (p *fakePeer) accept() {
	for {
		conn, err := p.ln.Accept()
		if err != nil {
			return
		}
		p.mu.Lock()
		p.sessions++
		p.mu.Unlock()
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			defer conn.Close()
			p.serve(conn)
		}()
	}
}

func (p *fakePeer) sessionCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sessions
}

func (p *fakePeer) delivered() []fakeDelivery {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]fakeDelivery(nil), p.deliveries...)
}

func (p *fakePeer) received() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.lines...)
}

func (p *fakePeer) serve(conn net.Conn) {
	r := bufio.NewReader(conn)
	w := conn
	write := func(lines ...string) bool {
		for _, l := range lines {
			if _, err := fmt.Fprintf(w, "%s\r\n", l); err != nil {
				return false
			}
		}
		return true
	}
	if !write(p.greeting) || !strings.HasPrefix(p.greeting, "220") {
		return
	}
	tlsActive := false
	var from string
	var rcpts []string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		p.mu.Lock()
		p.lines = append(p.lines, line)
		p.mu.Unlock()
		verb := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		switch verb {
		case "EHLO":
			out := []string{"fake.test"}
			for _, e := range p.ext {
				if tlsActive && e == "STARTTLS" {
					continue
				}
				out = append(out, e)
			}
			for i, l := range out {
				sep := "-"
				if i == len(out)-1 {
					sep = " "
				}
				write("250" + sep + l)
			}
		case "STARTTLS":
			if p.cert == nil {
				write("454 4.7.0 TLS not available")
				continue
			}
			write("220 2.0.0 go ahead")
			tc := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{*p.cert}})
			if err := tc.Handshake(); err != nil {
				return
			}
			r, w, tlsActive = bufio.NewReader(tc), tc, true
			from, rcpts = "", nil
		case "MAIL":
			from = between(line, "<", ">")
			reply := "250 2.1.0 ok"
			if p.mail != nil {
				reply = p.mail(from)
			}
			if !strings.HasPrefix(reply, "2") {
				from = ""
			}
			write(reply)
		case "RCPT":
			addr := between(line, "<", ">")
			reply := "250 2.1.5 ok"
			if p.rcpt != nil {
				reply = p.rcpt(addr)
			}
			if strings.HasPrefix(reply, "2") {
				rcpts = append(rcpts, addr)
			}
			write(reply)
		case "DATA":
			if p.dropOnDATA {
				return
			}
			write("354 go ahead")
			var body strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				body.WriteString(strings.TrimPrefix(l, "."))
			}
			if p.dropAfterDot {
				return
			}
			reply := "250 2.0.0 queued as Q1"
			if p.data != nil {
				reply = p.data(body.String())
			}
			if strings.HasPrefix(reply, "2") {
				p.mu.Lock()
				p.deliveries = append(p.deliveries, fakeDelivery{from: from, rcpts: append([]string(nil), rcpts...), body: body.String()})
				p.mu.Unlock()
			}
			from, rcpts = "", nil
			write(reply)
		case "RSET":
			from, rcpts = "", nil
			write("250 ok")
		case "NOOP":
			write("250 ok")
		case "QUIT":
			write("221 bye")
			return
		default:
			write("502 5.5.2 not implemented")
		}
	}
}

func between(s, open, close string) string {
	_, rest, ok := strings.Cut(s, open)
	if !ok {
		return ""
	}
	v, _, _ := strings.Cut(rest, close)
	return v
}

// network maps the addresses a test's resolver hands out to fake peers, and
// is the Deliverer's Dial hook. An unmapped address refuses the connection.
type network struct {
	mu    sync.Mutex
	peers map[netip.AddrPort]*fakePeer
	dials []netip.AddrPort
}

func newNetwork() *network { return &network{peers: map[netip.AddrPort]*fakePeer{}} }

func (n *network) add(addr string, p *fakePeer) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.peers[netip.AddrPortFrom(netip.MustParseAddr(addr), smtpPort)] = p
}

func (n *network) dial(ctx context.Context, req *DialRequest) (net.Conn, error) {
	n.mu.Lock()
	n.dials = append(n.dials, req.Address)
	p := n.peers[req.Address]
	n.mu.Unlock()
	if p == nil {
		return nil, fmt.Errorf("dial %s: connection refused", req.Address)
	}
	return (&net.Dialer{}).DialContext(ctx, "tcp", p.ln.Addr().String())
}

func (n *network) dialed() []netip.AddrPort {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]netip.AddrPort(nil), n.dials...)
}

// TestFakePeer pins the shared peer's basic contract.
func TestFakePeer(t *testing.T) {
	p := newFakePeer(t, "PIPELINING", "SIZE 1000")
	conn, err := net.Dial("tcp", p.ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	read := func() string { l, _ := r.ReadString('\n'); return strings.TrimRight(l, "\r\n") }
	send := func(s string) { fmt.Fprintf(conn, "%s\r\n", s) }
	if g := read(); !strings.HasPrefix(g, "220") {
		t.Fatalf("greeting %q", g)
	}
	send("EHLO c")
	for l := read(); strings.HasPrefix(l, "250-"); l = read() {
	}
	send("MAIL FROM:<a@x>")
	read()
	send("RCPT TO:<b@y>")
	read()
	send("DATA")
	read()
	send("..dot line")
	send(".")
	if l := read(); !strings.HasPrefix(l, "250") {
		t.Fatalf("DATA reply %q", l)
	}
	send("QUIT")
	read()
	got := p.delivered()
	if len(got) != 1 || got[0].from != "a@x" || got[0].rcpts[0] != "b@y" || got[0].body != ".dot line\r\n" {
		t.Errorf("deliveries = %+v", got)
	}
}
