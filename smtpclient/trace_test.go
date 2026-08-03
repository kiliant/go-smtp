package smtpclient

import (
	"context"
	"encoding/base64"
	"strings"
	"sync"
	"testing"
)

// traceRecorder collects trace events. The hook is documented as being called
// on the goroutine driving the connection, so the recorder locks rather than
// assuming the test goroutine is the only writer.
type traceRecorder struct {
	mu     sync.Mutex
	events []TraceEvent
}

func (r *traceRecorder) hook() func(TraceEvent) {
	return func(ev TraceEvent) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.events = append(r.events, ev)
	}
}

func (r *traceRecorder) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.events))
	for _, ev := range r.events {
		out = append(out, string(ev.Direction)+" "+ev.Line)
	}
	return out
}

func (r *traceRecorder) joined() string {
	return strings.Join(r.lines(), "\n")
}

// TestTraceRecordsBothDirections proves the hook sees the conversation at all,
// in order, and labels each line's direction. Without this the redaction tests
// below could pass simply because nothing was ever traced.
func TestTraceRecordsBothDirections(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "NOOP", replies: fakeReplies("250 ok\r\n")},
	}, nil)
	defer done()
	var rec traceRecorder
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test", Trace: rec.hook()})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Noop(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	got := rec.lines()
	want := []string{
		"received 220 fake.test ready",
		"sent EHLO client.test",
		"received 250 fake.test",
		"sent NOOP",
		"received 250 ok",
	}
	if len(got) != len(want) {
		t.Fatalf("trace = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("trace[%d] = %q, want %q (full: %#v)", i, got[i], want[i], got)
		}
	}
}

// TestTraceRedactsAUTHInitialResponse is the T11 assertion for the payload
// carried in the AUTH command itself, which is where PLAIN puts the password.
// It asserts on the credential bytes, not merely on the presence of the
// redaction marker: a hook that emitted both would still be a leak.
func TestTraceRedactsAUTHInitialResponse(t *testing.T) {
	const secret = "hunter2"
	initial := base64.StdEncoding.EncodeToString([]byte("\x00user\x00" + secret))
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 AUTH PLAIN\r\n")},
		{command: "AUTH PLAIN " + initial, replies: fakeReplies("235 accepted\r\n")},
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
	}, nil)
	defer done()
	var rec traceRecorder
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test", Trace: rec.hook()})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Auth(context.Background(), &AuthOptions{Username: "user", Password: secret, AllowInsecureAuth: true}); err != nil {
		t.Fatal(err)
	}
	trace := rec.joined()
	if strings.Contains(trace, initial) {
		t.Fatalf("trace leaked the AUTH initial response:\n%s", trace)
	}
	if strings.Contains(trace, secret) {
		t.Fatalf("trace leaked the password:\n%s", trace)
	}
	if !strings.Contains(trace, "sent AUTH PLAIN "+redactedPayload) {
		t.Fatalf("trace did not record a redacted AUTH command:\n%s", trace)
	}
}

// TestTraceRedactsSASLExchange covers the multi-step case: the bare
// continuation response the client writes outside the command encoder, and
// the server's 334 challenge. CRAM-MD5 is used because its response is
// derived from the password, so a leak here is a credential leak.
func TestTraceRedactsSASLExchange(t *testing.T) {
	const secret = "tanstaaftanstaaf"
	challenge := base64.StdEncoding.EncodeToString([]byte("<1896.697170952@postoffice.reston.mci.net>"))
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 AUTH CRAM-MD5\r\n")},
		{command: "AUTH CRAM-MD5", replies: fakeReplies("334 " + challenge + "\r\n")},
		// The RFC 2195 worked example: HMAC-MD5 of that challenge under this
		// password, for user "tim". That the client produces it verbatim is
		// itself the known-answer check for CRAM-MD5.
		{command: base64.StdEncoding.EncodeToString([]byte("tim b913a602c7eda7a495b4e6e7334d3890")), replies: fakeReplies("235 accepted\r\n")},
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
	}, nil)
	defer done()
	var rec traceRecorder
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test", Trace: rec.hook()})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Auth(context.Background(), &AuthOptions{Username: "tim", Password: secret, AllowInsecureAuth: true}); err != nil {
		t.Fatal(err)
	}
	trace := rec.joined()
	if strings.Contains(trace, challenge) {
		t.Fatalf("trace leaked the 334 challenge:\n%s", trace)
	}
	if strings.Contains(trace, secret) {
		t.Fatalf("trace leaked the password:\n%s", trace)
	}
	// The client's computed response is the credential-derived value; it is
	// written as a bare line, so the whole line must be the marker.
	if !strings.Contains(trace, "sent "+redactedPayload) {
		t.Fatalf("trace did not record a redacted SASL response:\n%s", trace)
	}
	if !strings.Contains(trace, "received 334 "+redactedPayload) {
		t.Fatalf("trace did not record a redacted challenge:\n%s", trace)
	}
}

// TestTraceRedactsEveryMechanismInitialResponse covers T11's wording
// literally: the redaction must hold for every mechanism, not only the two
// driven end to end above. Redaction is mechanism-agnostic by construction —
// everything after the mechanism name goes — and this pins that property
// against the full set the client can select, so adding a mechanism cannot
// quietly introduce a leak.
func TestTraceRedactsEveryMechanismInitialResponse(t *testing.T) {
	const payload = "c2VjcmV0LWNyZWRlbnRpYWw="
	for _, mechanism := range []string{
		"PLAIN", "LOGIN", "CRAM-MD5", "EXTERNAL",
		"SCRAM-SHA-1", "SCRAM-SHA-1-PLUS",
		"SCRAM-SHA-256", "SCRAM-SHA-256-PLUS",
		"OAUTHBEARER", "XOAUTH2",
	} {
		got := traceCommandLine(queuedCommand{verb: "AUTH", args: []string{mechanism, payload}})
		if strings.Contains(got, payload) {
			t.Errorf("AUTH %s trace leaked the payload: %q", mechanism, got)
		}
		if want := "AUTH " + mechanism + " " + redactedPayload; got != want {
			t.Errorf("AUTH %s trace = %q, want %q", mechanism, got, want)
		}
	}
	// A mechanism that carries several arguments must not leak a later one.
	got := traceCommandLine(queuedCommand{verb: "AUTH", args: []string{"PLAIN", payload, payload}})
	if strings.Contains(got, payload) {
		t.Errorf("multi-argument AUTH trace leaked a payload: %q", got)
	}
	// Non-AUTH commands must still be traced in full, or the trace is useless.
	if got := traceCommandLine(queuedCommand{verb: "MAIL", args: []string{"FROM:<a@b.test>"}}); got != "MAIL FROM:<a@b.test>" {
		t.Errorf("non-AUTH command was altered: %q", got)
	}
}

// TestTraceOmitsMessageContent pins the documented promise that DATA payloads
// never reach the hook. A trace that streamed message bodies would be both a
// privacy problem and unbounded.
func TestTraceOmitsMessageContent(t *testing.T) {
	const body = "SUBJECT-LINE-THAT-MUST-NOT-BE-TRACED"
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 ok\r\n")},
		{command: "RCPT TO:<rcpt@example.test>", replies: fakeReplies("250 ok\r\n")},
		{command: "DATA", replies: fakeReplies("354 go ahead\r\n")},
		{command: body, replies: nil},
		{command: ".", replies: fakeReplies("250 queued\r\n")},
	}, nil)
	defer done()
	var rec traceRecorder
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test", Trace: rec.hook()})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt(context.Background(), "rcpt@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Data(context.Background(), strings.NewReader(body+"\r\n"), nil); err != nil {
		t.Fatal(err)
	}
	if trace := rec.joined(); strings.Contains(trace, body) {
		t.Fatalf("trace leaked message content:\n%s", trace)
	}
}

// TestTraceNilHookIsInert guards the zero value: the overwhelmingly common
// configuration is no tracing at all, and it must not panic or allocate a
// conversation nobody asked for.
func TestTraceNilHookIsInert(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "NOOP", replies: fakeReplies("250 ok\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Noop(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

// TestTraceReplyLineJoinsMultilineReplies pins the documented shape: a
// multiline reply arrives as one event whose Line matches smtp.Error's Text,
// so a trace and an error read alike.
func TestTraceReplyLineJoinsMultilineReplies(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250-PIPELINING\r\n", "250 DSN\r\n")},
	}, nil)
	defer done()
	var rec traceRecorder
	if _, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test", Trace: rec.hook()}); err != nil {
		t.Fatal(err)
	}
	want := "received 250 fake.test\nPIPELINING\nDSN"
	if trace := rec.joined(); !strings.Contains(trace, want) {
		t.Fatalf("trace = %q, want a joined multiline reply %q", trace, want)
	}
}
