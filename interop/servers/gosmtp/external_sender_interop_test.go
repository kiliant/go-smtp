//go:build interop

package gosmtp

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kiliant/go-smtp/interop/harness"
	"github.com/kiliant/go-smtp/smtpclient"
	"github.com/kiliant/go-smtp/smtpserver"
)

const postfixSenderImage = "docker.io/boky/postfix@sha256:aafc772384232497bed875e1eb66b4d3e54ba1ebc86e2e185a6dc1dbc48182ef"

func TestPostfixRelaysToGoSMTP(t *testing.T) {
	requirePodman(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	target := startExternalTarget(t, ctx, smtpserver.ModeSMTP)
	sender := startPostfixSender(t, ctx, "smtp", targetContainerAddress(target))

	body := []byte("postfix-smtp-body-01\r\n.\r\npostfix-smtp-tail-7f42\r\n")
	message := append([]byte("From: sender@example.test\r\nTo: interop@example.test\r\nSubject: Postfix SMTP relay\r\n\r\n"), body...)
	submitToSender(t, ctx, sender, []string{"interop@example.test"}, message)

	delivered, err := harness.WaitForMessage(ctx, target.Sink(), "interop@example.test")
	if err != nil {
		logSender(t, sender)
		t.Fatal(err)
	}
	if got := messageBody(delivered.Raw); !bytes.Equal(got, body) {
		t.Fatalf("Postfix SMTP body changed in relay\nwant: %q\n got: %q\nraw: %q", body, got, delivered.Raw)
	}
	waitPostfixQueueEmpty(t, ctx, sender)
}

func TestPostfixLMTPTransportDrivesPerRecipientReplies(t *testing.T) {
	requirePodman(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	target := startExternalTarget(t, ctx, smtpserver.ModeLMTP)
	sender := startPostfixSender(t, ctx, "lmtp", targetContainerAddress(target))

	recipients := []string{"first@example.test", "second@example.test"}
	body := []byte("postfix-lmtp-body-02\r\n.\r\npostfix-lmtp-tail-a913\r\n")
	message := append([]byte("From: sender@example.test\r\nTo: first@example.test, second@example.test\r\nSubject: Postfix LMTP relay\r\n\r\n"), body...)
	submitToSender(t, ctx, sender, recipients, message)

	if _, err := harness.WaitForMessage(ctx, target.Sink(), recipients[0]); err != nil {
		logSender(t, sender)
		t.Fatal(err)
	}
	waitPostfixQueueEmpty(t, ctx, sender)
	stored := target.sink.source.Messages()
	if len(stored) != 1 {
		t.Fatalf("LMTP deliveries = %d, want one transaction; messages = %+v", len(stored), stored)
	}
	if got := stored[0].Recipients; len(got) != len(recipients) || got[0] != recipients[0] || got[1] != recipients[1] {
		t.Fatalf("LMTP recipients = %v, want issue order %v", got, recipients)
	}
	if got := messageBody(stored[0].Data); !bytes.Equal(got, body) {
		t.Fatalf("Postfix LMTP body changed in relay\nwant: %q\n got: %q\nraw: %q", body, got, stored[0].Data)
	}
}

func TestEximRelaysToGoSMTP(t *testing.T) {
	requirePodman(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	target := startExternalTarget(t, ctx, smtpserver.ModeSMTP)
	sender := startEximSender(t, ctx, targetPort(target))

	body := []byte("exim-smtp-body-03\r\n.\r\nexim-smtp-tail-c284\r\n")
	message := append([]byte("From: sender@example.test\r\nTo: interop@example.test\r\nSubject: Exim SMTP relay\r\n\r\n"), body...)
	submitToSender(t, ctx, sender, []string{"interop@example.test"}, message)
	delivered, err := harness.WaitForMessage(ctx, target.Sink(), "interop@example.test")
	if err != nil {
		logRuntime(t, "Exim sender", sender)
		t.Fatal(err)
	}
	if got := messageBody(delivered.Raw); !bytes.Equal(got, body) {
		t.Fatalf("Exim SMTP body changed in relay\nwant: %q\n got: %q\nraw: %q", body, got, delivered.Raw)
	}
	waitEximQueueEmpty(t, ctx, sender)
}

func requirePodman(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skip("podman is not installed")
	}
}

func startExternalTarget(t *testing.T, ctx context.Context, mode smtpserver.Mode) *Target {
	t.Helper()
	target, err := startTarget(ctx, mode, "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := target.Stop(stopCtx); err != nil {
			t.Errorf("stopping gosmtp %s target: %v", mode, err)
		}
	})
	hostAddress := net.JoinHostPort("127.0.0.1", targetPort(target))
	healthCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client, err := harness.WaitForEHLO(healthCtx, hostAddress, &smtpclient.ClientOptions{
		Identity:        "external-sender-health.example.test",
		LMTP:            mode == smtpserver.ModeLMTP,
		GreetingTimeout: 3 * time.Second,
		MailTimeout:     3 * time.Second,
	})
	if err != nil {
		t.Fatalf("health-gating gosmtp %s target: %v", mode, err)
	}
	_ = client.Close()
	return target
}

func targetPort(target *Target) string {
	return fmt.Sprint(target.listener.Addr().(*net.TCPAddr).Port)
}

func targetContainerAddress(target *Target) string {
	return net.JoinHostPort("host.containers.internal", targetPort(target))
}

func startPostfixSender(t *testing.T, ctx context.Context, transport, destination string) *harness.Handle {
	t.Helper()
	host, port, err := net.SplitHostPort(destination)
	if err != nil {
		t.Fatalf("invalid Postfix relay destination %q: %v", destination, err)
	}
	env := map[string]string{
		"ALLOWED_SENDER_DOMAINS":               "example.test",
		"POSTFIX_myhostname":                   "postfix-sender.example.test",
		"POSTFIX_relayhost":                    "[" + host + "]:" + port,
		"POSTFIX_smtpd_recipient_restrictions": "permit_mynetworks,reject_unauth_destination",
		"POSTFIX_smtpd_relay_restrictions":     "permit_mynetworks,reject_unauth_destination",
		"POSTFIX_smtp_tls_security_level": "none",
		// Nothing here uses IPv6.
		"POSTFIX_inet_protocols": "ipv4",
		// Postfix's own SMTP client resolver is DNS-only by default
		// (smtp_host_lookup = dns) and, unlike a normal getaddrinfo/NSS
		// lookup, never consults /etc/hosts. Podman registers
		// host.containers.internal only as an /etc/hosts entry in the
		// sender container, not as an actual DNS record, so the relayhost
		// lookup fails outright on at least one CI runner's Podman network
		// ('Host or domain name not found... type=A: Host not found') even
		// though the alias resolves fine there for every ordinary consumer,
		// Exim's byname lookup among them — that's why only the Postfix
		// senders were affected. dns,native tries real DNS first and falls
		// back to the native resolver, which does read /etc/hosts.
		"POSTFIX_smtp_host_lookup": "dns, native",
	}
	if transport == "lmtp" {
		env["POSTFIX_default_transport"] = "lmtp"
		env["POSTFIX_relay_transport"] = "lmtp"
		// The lmtp delivery agent is a separate Postfix service from smtp
		// and has its own independent host-lookup setting; smtp_host_lookup
		// above does not apply to it. Confirmed live in CI: with only
		// smtp_host_lookup set, TestPostfixRelaysToGoSMTP (smtp agent)
		// passed while this LMTP case still bounced on the identical
		// unresolved-host.containers.internal symptom.
		env["POSTFIX_lmtp_host_lookup"] = "dns, native"
	}
	handle, err := harness.Run(ctx, harness.RunConfig{
		Name:  harness.ContainerName("gosmtp-postfix-" + transport),
		Image: postfixSenderImage,
		Env:   env,
		Ports: []int{25},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if t.Failed() {
			logSender(t, handle)
		}
		if err := handle.Stop(stopCtx); err != nil {
			t.Errorf("stopping Postfix sender: %v", err)
		}
	})
	addr, ok := handle.HostAddr(25)
	if !ok {
		t.Fatal("Postfix sender has no mapped SMTP address")
	}
	healthCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client, err := harness.WaitForEHLO(healthCtx, addr, &smtpclient.ClientOptions{
		Identity:        "external-sender-health.example.test",
		GreetingTimeout: 3 * time.Second,
		MailTimeout:     3 * time.Second,
	})
	if err != nil {
		logSender(t, handle)
		t.Fatalf("health-gating Postfix sender: %v", err)
	}
	_ = client.Close()
	return handle
}

func startEximSender(t *testing.T, ctx context.Context, port string) *harness.Handle {
	t.Helper()
	handle, err := harness.Run(ctx, harness.RunConfig{
		Name:             harness.ContainerName("gosmtp-exim-smtp"),
		ContainerfileDir: eximSenderDir(),
		Env:              map[string]string{"GOSMTP_PORT": port},
		Ports:            []int{25},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if t.Failed() {
			logRuntime(t, "Exim sender", handle)
		}
		if err := handle.Stop(stopCtx); err != nil {
			t.Errorf("stopping Exim sender: %v", err)
		}
	})
	addr, ok := handle.HostAddr(25)
	if !ok {
		t.Fatal("Exim sender has no mapped SMTP address")
	}
	healthCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	client, err := harness.WaitForEHLO(healthCtx, addr, &smtpclient.ClientOptions{
		Identity:        "external-sender-health.example.test",
		GreetingTimeout: 3 * time.Second,
		MailTimeout:     3 * time.Second,
	})
	if err != nil {
		logRuntime(t, "Exim sender", handle)
		t.Fatalf("health-gating Exim sender: %v", err)
	}
	_ = client.Close()
	return handle
}

func eximSenderDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "exim-sender")
}

func submitToSender(t *testing.T, ctx context.Context, sender *harness.Handle, recipients []string, message []byte) {
	t.Helper()
	addr, ok := sender.HostAddr(25)
	if !ok {
		t.Fatal("Postfix sender has no mapped SMTP address")
	}
	client, err := smtpclient.Dial(ctx, &smtpclient.ClientOptions{
		Address:         addr,
		Identity:        "external-submit.example.test",
		GreetingTimeout: 5 * time.Second,
		MailTimeout:     10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Mail(ctx, "sender@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(ctx, recipient, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.Data(ctx, bytes.NewReader(message), nil); err != nil {
		t.Fatal(err)
	}
	if err := client.Quit(ctx, nil); err != nil {
		t.Fatal(err)
	}
}

func waitEximQueueEmpty(t *testing.T, ctx context.Context, sender *harness.Handle) {
	t.Helper()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var last string
	for {
		output, err := sender.Exec(ctx, "exim4", "-bp")
		if err == nil {
			last = string(output)
			if strings.TrimSpace(last) == "" {
				return
			}
		}
		select {
		case <-ctx.Done():
			logRuntime(t, "Exim sender", sender)
			t.Fatalf("Exim queue did not empty after relay completion: %v; last queue: %s", ctx.Err(), last)
		case <-ticker.C:
		}
	}
}

func waitPostfixQueueEmpty(t *testing.T, ctx context.Context, sender *harness.Handle) {
	t.Helper()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	var last string
	for {
		output, err := sender.Exec(ctx, "postqueue", "-p")
		if err == nil {
			last = string(output)
			if strings.Contains(last, "Mail queue is empty") {
				return
			}
		}
		select {
		case <-ctx.Done():
			logSender(t, sender)
			t.Fatalf("Postfix queue did not empty after relay completion: %v; last queue: %s", ctx.Err(), last)
		case <-ticker.C:
		}
	}
}

func messageBody(message []byte) []byte {
	if _, body, ok := bytes.Cut(message, []byte("\r\n\r\n")); ok {
		return body
	}
	return nil
}

func logSender(t *testing.T, sender *harness.Handle) {
	logRuntime(t, "Postfix sender", sender)
}

func logRuntime(t *testing.T, name string, sender *harness.Handle) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if logs, err := sender.Logs(ctx); err == nil && logs != "" {
		t.Logf("%s logs:\n%s", name, logs)
	}
}
