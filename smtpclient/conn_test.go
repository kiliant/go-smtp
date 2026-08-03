package smtpclient

import (
	"bufio"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

func TestDialEHLOPreservesRawExtensions(t *testing.T) {
	server, wait := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-server.test hello\r\n", "250-X-FUTURE alpha  beta\r\n", "250 PIPELINING\r\n")},
		{command: "QUIT", replies: fakeReplies("221 bye\r\n")},
	}, nil)
	client, err := Dial(context.Background(), &ClientOptions{
		Address: "unused:25", Identity: "client.test",
		DialContext: func(context.Context, string, string) (net.Conn, error) { return server, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	params, ok := client.Extension(smtp.Extension("x-future"))
	if !ok || params != "alpha  beta" {
		t.Fatalf("Extension(X-FUTURE) = (%q, %v), want raw parameters", params, ok)
	}
	if _, ok := client.Extension(smtp.Extension("server.test")); ok {
		t.Fatal("EHLO greeting domain was recorded as an extension")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	wait()
}

func TestHELOFallback(t *testing.T) {
	server, wait := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("502 no ehlo\r\n")},
		{command: "HELO client.test", replies: fakeReplies("250 hello\r\n")},
		{command: "QUIT", replies: fakeReplies("221 bye\r\n")},
	}, nil)
	client, err := NewClient(context.Background(), server, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := client.Extension(smtp.ExtPipelining); ok {
		t.Fatal("HELO fallback retained extensions")
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	wait()
}

func TestHELODoesNotFallbackOnOtherFailure(t *testing.T) {
	server, wait := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("501 5.5.1 rejected\r\n")},
	}, nil)
	_, err := NewClient(context.Background(), server, &ClientOptions{Identity: "client.test"})
	var smtpErr *smtp.Error
	if !errors.As(err, &smtpErr) || smtpErr.Code != 501 {
		t.Fatalf("NewClient error = %v, want *smtp.Error 501", err)
	}
	if smtpErr.Enhanced.Raw != "" || smtpErr.Text != "5.5.1 rejected" {
		t.Fatalf("pre-negotiation EHLO parsed enhanced code: %+v", smtpErr)
	}
	wait()
}

func TestGreeting554IsSMTPError(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = server.Write([]byte("554 5.7.1 unavailable\r\n"))
		_ = server.Close()
	}()
	_, err := NewClient(context.Background(), client, nil)
	var smtpErr *smtp.Error
	if !errors.As(err, &smtpErr) || smtpErr.Code != 554 {
		t.Fatalf("NewClient error = %v, want *smtp.Error 554", err)
	}
	if smtpErr.Enhanced.Raw != "" || smtpErr.Text != "5.7.1 unavailable" {
		t.Fatalf("pre-negotiation greeting parsed enhanced code: %+v", smtpErr)
	}
	<-done
}

func TestEnhancedCodeExtractionRequiresAdvertisement(t *testing.T) {
	tests := []struct {
		name      string
		ehloReply []string
		wantRaw   string
		wantText  string
	}{
		{
			name:      "not advertised",
			ehloReply: fakeReplies("250 fake.test\r\n"),
			wantText:  "5.7.1 denied",
		},
		{
			name:      "advertised",
			ehloReply: fakeReplies("250-fake.test\r\n", "250 ENHANCEDSTATUSCODES\r\n"),
			wantRaw:   "5.7.1",
			wantText:  "denied",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, wait := startFakeServer(t, []fakeStep{
				{command: "EHLO client.test", replies: test.ehloReply},
				{command: "QUIT", replies: fakeReplies("221 bye\r\n")},
			}, nil)
			client, err := NewClient(context.Background(), server, &ClientOptions{Identity: "client.test"})
			if err != nil {
				t.Fatal(err)
			}
			err = unexpectedReply("NOOP", smtpwire.Reply{Code: 550, Text: "5.7.1 denied"}, client.conn.enhancedStatusCodes(), 250)
			var smtpErr *smtp.Error
			if !errors.As(err, &smtpErr) {
				t.Fatalf("unexpectedReply error = %v, want *smtp.Error", err)
			}
			if smtpErr.Enhanced.Raw != test.wantRaw || smtpErr.Text != test.wantText {
				t.Fatalf("error = %+v, want Raw %q and Text %q", smtpErr, test.wantRaw, test.wantText)
			}
			if err := client.Close(); err != nil {
				t.Fatal(err)
			}
			wait()
		})
	}
}

func TestStartTLSReplacesExtensions(t *testing.T) {
	serverTLS, clientTLS := fakeTLSConfig(t)
	server, wait := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-server.test\r\n", "250-STARTTLS\r\n", "250 CLEARONLY old\r\n")},
		{command: "STARTTLS", replies: fakeReplies("220 ready\r\n"), startTLS: true},
		{command: "EHLO client.test", replies: fakeReplies("250-server.test\r\n", "250 NEWONLY new\r\n")},
		{command: "QUIT", replies: fakeReplies("221 bye\r\n")},
	}, serverTLS)
	client, err := NewClient(context.Background(), server, &ClientOptions{Identity: "client.test", TLSConfig: clientTLS})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.StartTLS(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := client.Extension(smtp.Extension("CLEARONLY")); ok {
		t.Fatal("cleartext extension survived STARTTLS")
	}
	if params, ok := client.Extension(smtp.Extension("NEWONLY")); !ok || params != "new" {
		t.Fatalf("post-TLS extension = (%q, %v)", params, ok)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	wait()
}

func TestSTARTTLSVerificationDefaultsOn(t *testing.T) {
	serverTLS, _ := fakeTLSConfig(t)
	server, wait := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-server.test\r\n", "250 STARTTLS\r\n")},
		{command: "STARTTLS", replies: fakeReplies("220 ready\r\n"), startTLS: true, allowTLSHandshakeErr: true},
	}, serverTLS)
	client, err := NewClient(context.Background(), server, &ClientOptions{Identity: "client.test", TLSServerName: "server.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.StartTLS(context.Background(), nil); err == nil {
		t.Fatal("STARTTLS succeeded with an untrusted certificate")
	}
	wait()
}

func TestStartTLSInheritsConstructionInsecureSkipVerify(t *testing.T) {
	serverTLS, _ := fakeTLSConfig(t)
	server, wait := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-server.test\r\n", "250 STARTTLS\r\n")},
		{command: "STARTTLS", replies: fakeReplies("220 ready\r\n"), startTLS: true},
		{command: "EHLO client.test", replies: fakeReplies("250 server.test\r\n")},
		{command: "QUIT", replies: fakeReplies("221 bye\r\n")},
	}, serverTLS)
	client, err := NewClient(context.Background(), server, &ClientOptions{
		Identity: "client.test", TLSServerName: "server.test", InsecureSkipVerify: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.StartTLS(context.Background(), nil); err != nil {
		t.Fatalf("StartTLS did not inherit InsecureSkipVerify: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	wait()
}

func TestImplicitTLS(t *testing.T) {
	serverTLS, clientTLS := fakeTLSConfig(t)
	server, wait := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n"), implicitTLS: true},
		{command: "QUIT", replies: fakeReplies("221 bye\r\n")},
	}, serverTLS)
	client, err := NewClient(context.Background(), server, &ClientOptions{
		Identity: "client.test", ImplicitTLS: true, TLSConfig: clientTLS,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	wait()
}

func TestImplicitTLSHandshakeUsesGreetingTimeout(t *testing.T) {
	client, server := net.Pipe()
	defer server.Close()
	started := time.Now()
	_, err := NewClient(context.Background(), client, &ClientOptions{
		ImplicitTLS: true, GreetingTimeout: 20 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("implicit TLS unexpectedly completed without a peer handshake")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("implicit TLS handshake took %v, want configured timeout", elapsed)
	}
}

func TestStartTLSHandshakeUsesConfiguredTimeout(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer serverConn.Close()
		reader := bufio.NewReader(serverConn)
		_, _ = serverConn.Write([]byte("220 fake.test ready\r\n"))
		_, _ = reader.ReadString('\n') // EHLO
		_, _ = serverConn.Write([]byte("250-fake.test\r\n250 STARTTLS\r\n"))
		_, _ = reader.ReadString('\n') // STARTTLS
		_, _ = serverConn.Write([]byte("220 ready\r\n"))
		// Do not read a ClientHello. The client's TLS write must obey its
		// configured timeout rather than wait indefinitely.
		_, _ = reader.ReadByte()
	}()
	client, err := NewClient(context.Background(), clientConn, &ClientOptions{
		Identity: "client.test", MailTimeout: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err = client.StartTLS(context.Background(), nil)
	if err == nil {
		t.Fatal("STARTTLS unexpectedly completed without a peer handshake")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("STARTTLS handshake took %v, want configured timeout", elapsed)
	}
	<-done
}

func Test421PoisonsConnection(t *testing.T) {
	server, wait := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-server.test\r\n", "250 PIPELINING\r\n")},
		{command: "NOOP", replies: fakeReplies("421 closing\r\n")},
	}, nil)
	client, err := NewClient(context.Background(), server, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.conn.pipeline.execute(context.Background(), []queuedCommand{{verb: "NOOP", timeout: time.Second}})
	var smtpErr *smtp.Error
	if !errors.As(err, &smtpErr) || smtpErr.Code != 421 {
		t.Fatalf("NOOP error = %v, want *smtp.Error 421", err)
	}
	if _, err := client.conn.pipeline.execute(context.Background(), []queuedCommand{{verb: "MAIL", timeout: time.Second}}); err == nil {
		t.Fatal("poisoned connection accepted a second command")
	}
	wait()
}

func TestPipelineSyncPointsAreStructural(t *testing.T) {
	for _, verb := range []string{"EHLO", "DATA", "VRFY", "EXPN", "TURN", "QUIT", "NOOP"} {
		err := validateGroup([]queuedCommand{{verb: verb}, {verb: "MAIL"}})
		if err == nil {
			t.Errorf("%s before MAIL was accepted", verb)
		}
	}
	if err := validateGroup([]queuedCommand{{verb: "MAIL"}, {verb: "NOOP"}}); err != nil {
		t.Fatalf("sync point at end rejected: %v", err)
	}
}

// TestPipelineSyncPointsEnforcedOnProductionPath exists because the test
// above is not enough on its own. An audit found validateGroup was reachable
// only through execute, which no production code calls: every command entry
// point in the package calls executeLocked, having taken conn.opMu itself. A
// guard only its own unit test can reach is not enforcement, so this asserts
// the check on the path the package actually uses.
func TestPipelineSyncPointsEnforcedOnProductionPath(t *testing.T) {
	server, wait := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 PIPELINING\r\n")},
	}, nil)
	defer wait()
	client, err := NewClient(context.Background(), server, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	client.conn.opMu.Lock()
	defer client.conn.opMu.Unlock()
	_, err = client.conn.pipeline.executeLocked(context.Background(), []queuedCommand{
		{verb: "NOOP", timeout: time.Second},
		{verb: "MAIL", timeout: time.Second},
	})
	if err == nil || !strings.Contains(err.Error(), "sync point") {
		t.Fatalf("executeLocked accepted a mid-group sync point: %v", err)
	}
}

func TestPipelineCorrelatesMultilineRepliesByCommand(t *testing.T) {
	server, wait := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 PIPELINING\r\n")},
		{commands: []string{"MAIL", "RCPT"}, replies: fakeReplies("250-first line\r\n", "250 second line\r\n", "251 third reply\r\n")},
		{command: "QUIT", replies: fakeReplies("221 bye\r\n")},
	}, nil)
	client, err := NewClient(context.Background(), server, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	replies, err := client.conn.pipeline.execute(context.Background(), []queuedCommand{
		{verb: "MAIL", timeout: time.Second}, {verb: "RCPT", timeout: time.Second},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(replies) != 2 || replies[0].Code != 250 || replies[0].Text != "first line\nsecond line" || replies[1].Code != 251 {
		t.Fatalf("pipeline replies = %#v, want one multiline 250 then 251", replies)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	wait()
}

func TestPipelineUsesDepthOneWithoutAdvertisement(t *testing.T) {
	server, wait := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "MAIL", replies: fakeReplies("250 mail\r\n")},
		{command: "RCPT", replies: fakeReplies("250 rcpt\r\n")},
		{command: "QUIT", replies: fakeReplies("221 bye\r\n")},
	}, nil)
	client, err := NewClient(context.Background(), server, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.conn.pipeline.execute(context.Background(), []queuedCommand{{verb: "MAIL", timeout: time.Second}, {verb: "RCPT", timeout: time.Second}}); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	wait()
}

func TestPipelineRejectsOversizedCommandBeforeWriting(t *testing.T) {
	server, wait := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "QUIT", replies: fakeReplies("221 bye\r\n")},
	}, nil)
	client, err := NewClient(context.Background(), server, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.conn.pipeline.execute(context.Background(), []queuedCommand{{verb: "MAIL", args: []string{strings.Repeat("x", maxPipelineBytes)}, timeout: time.Second}})
	if err == nil || !strings.Contains(err.Error(), "pipeline byte bound") {
		t.Fatalf("oversized command error = %v, want byte-bound rejection", err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	wait()
}

func TestCloseIsIdempotent(t *testing.T) {
	server, wait := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
		{command: "QUIT", replies: fakeReplies("221 bye\r\n")},
	}, nil)
	client, err := NewClient(context.Background(), server, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close = %v, want nil", err)
	}
	wait()
}

func TestContextCancellationAfterWirePoisonsConnection(t *testing.T) {
	server, wait := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250 server.test\r\n")},
		{command: "NOOP", hold: true},
	}, nil)
	client, err := NewClient(context.Background(), server, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := client.conn.pipeline.execute(ctx, []queuedCommand{{verb: "NOOP", timeout: time.Minute}})
		result <- err
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled command = %v, want context.Canceled", err)
	}
	if !client.conn.closed() {
		t.Fatal("canceled in-flight command did not poison connection")
	}
	wait()
}
