//go:build interop

package smtpclient_test

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kiliant/go-smtp/interop/harness"
	"github.com/kiliant/go-smtp/smtpclient"
)

// This file deliberately does not blank-import interop/servers/dovecot.
// That package's init() unconditionally calls harness.Register, and this
// test binary already shares a process-wide harness registry with
// TestDataTransparencyInterop in data_interop_test.go (a file owned by a
// different task), which iterates every harness.Selected profile expecting
// an SMTP port. Registering the LMTP-only Dovecot profile here would make
// that generic loop pick it up and fail on "no SMTP port" — a regression in
// a file this task does not own. Instead this test starts the same
// container image directly from interop/servers/dovecot's own Containerfile
// (owned by this task) and duplicates the small doveadm-based sink from
// interop/servers/dovecot/sink.go, so it never touches the shared registry.
const (
	lmtpRecipientOne     = "interop@example.test"
	lmtpRecipientTwo     = "interop-second@example.test"
	lmtpRecipientUnknown = "nobody@example.invalid"
)

// TestLMTPPerRecipientDataRepliesInterop closes the T07 interop gap: it
// drives this module's public client (smtpclient) through a real LMTP
// transaction against Dovecot — the only Tier 1 server in the matrix that
// speaks LMTP — with three recipients, and asserts genuinely per-recipient
// behavior rather than merely a single reply copied out N times.
//
// The three recipients are chosen so the transaction has a real mixed
// outcome: two accepted mailboxes (provisioned in
// interop/servers/dovecot/users) and one address Dovecot's userdb does not
// recognise. That gives:
//
//   - A real per-recipient RCPT split (two 2yz, one 5yz) proving a single
//     LMTP session can and does treat recipients independently.
//   - A DataResult whose length equals the number of RCPT-accepted
//     recipients (2), not the number of RCPT commands sent (3) and not 1 —
//     the cardinality RFC 2033 §4.2 requires.
//   - Two final DATA replies that are genuinely distinct: Dovecot's LMTP
//     final reply for each recipient embeds that recipient's own address and
//     a per-recipient transaction-id suffix ("...:R2" for the second
//     recipient in the same transaction), so identical Text on both entries
//     would mean the client attributed one wire reply to both recipients
//     instead of reading two.
//
// A mixed accept/fail case scoped to the DATA final-reply stage itself (e.g.
// one recipient accepted at RCPT but failing at delivery, the scripted case
// named in docs/tasks/T07-lmtp.md) was investigated against this Dovecot
// image and not achieved without excessive config surgery: a per-user quota
// override (userdb extra fields "quota_rule=*:messages=0" and
// "quota_message_count=0", both tried with the quota mail_plugin explicitly
// enabled for the lmtp protocol) did not cause Dovecot 2.4.3 to reject
// delivery for that mailbox — quota enforcement did not trigger for either
// field name in this image, and further reverse-engineering of Dovecot
// 2.4's new settings-based quota configuration was judged out of scope here.
// The RCPT-level rejection above, combined with the DATA final reply
// distinctness assertion, is the strongest mixed-outcome case achieved and
// is recorded in interop/servers/dovecot/profile.go's package doc comment.
func TestLMTPPerRecipientDataRepliesInterop(t *testing.T) {
	cfg := harness.LoadConfig()
	if !cfg.Selects("dovecot") {
		t.Skip("dovecot not selected (see GO_SMTP_INTEROP_SERVERS)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.StartTimeout+cfg.HealthTimeout+4*cfg.SinkTimeout)
	defer cancel()

	run := harness.RunConfig{
		Name:             harness.ContainerName("smtpclient-dovecot-lmtp"),
		ContainerfileDir: dovecotContainerfileDir(),
		Ports:            []int{lmtpContainerPort},
		CapAdd:           []string{"NET_BIND_SERVICE"},
	}
	h, err := harness.Run(ctx, run)
	if err != nil {
		t.Fatalf("starting dovecot: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), cfg.StopTimeout)
		defer stopCancel()
		if t.Failed() {
			if logs, logErr := h.Logs(stopCtx); logErr == nil {
				t.Logf("dovecot container logs:\n%s", logs)
			}
		}
		if err := h.Stop(stopCtx); err != nil {
			t.Errorf("stopping dovecot: %v", err)
		}
	})

	addr, ok := h.HostAddr(lmtpContainerPort)
	if !ok {
		t.Fatalf("no mapped LMTP port for dovecot")
	}

	// Health-gate on a real LHLO negotiation, never a sleep: this is the
	// same check interop/servers/dovecot's profile claims (an LMTP service
	// that completes LHLO), performed directly here since this test does
	// not go through harness.AssertProfile/the shared Profile registry.
	healthCtx, healthCancel := context.WithTimeout(ctx, cfg.HealthTimeout)
	healthClient, err := harness.WaitForEHLO(healthCtx, addr, &smtpclient.ClientOptions{
		LMTP:            true,
		GreetingTimeout: cfg.CommandTimeout,
		MailTimeout:     cfg.CommandTimeout,
	})
	healthCancel()
	if err != nil {
		if logs, logErr := h.Logs(context.Background()); logErr == nil {
			t.Logf("dovecot container logs:\n%s", logs)
		}
		t.Fatalf("dovecot never completed LHLO within the health timeout: %v", err)
	}
	_ = healthClient.Close()

	sink := dovecotDoveadmSink{exec: h}
	resetCtx, resetCancel := context.WithTimeout(ctx, cfg.SinkTimeout)
	defer resetCancel()
	if err := sink.Reset(resetCtx, lmtpRecipientOne); err != nil {
		t.Fatalf("resetting %s: %v", lmtpRecipientOne, err)
	}
	if err := sink.Reset(resetCtx, lmtpRecipientTwo); err != nil {
		t.Fatalf("resetting %s: %v", lmtpRecipientTwo, err)
	}

	client, err := smtpclient.Dial(ctx, &smtpclient.ClientOptions{
		Address:         addr,
		Identity:        "interop-client.example.test",
		LMTP:            true,
		GreetingTimeout: 10 * time.Second,
		MailTimeout:     10 * time.Second,
	})
	if err != nil {
		t.Fatalf("dialing dovecot: %v", err)
	}
	defer func() { _ = client.Close() }()

	commandCtx, commandCancel := context.WithTimeout(ctx, maxDuration(cfg.CommandTimeout, 30*time.Second))
	defer commandCancel()

	if err := client.Mail(commandCtx, "sender@example.test", nil, nil); err != nil {
		t.Fatalf("MAIL FROM: %v", err)
	}

	recipients, err := client.RcptBatch(commandCtx, []smtpclient.Recipient{
		{Address: lmtpRecipientOne},
		{Address: lmtpRecipientTwo},
		{Address: lmtpRecipientUnknown},
	}, nil)
	if err != nil {
		t.Fatalf("RcptBatch: %v", err)
	}
	if len(recipients) != 3 {
		t.Fatalf("RcptBatch returned %d results, want 3: %#v", len(recipients), recipients)
	}
	if !recipients[0].Accepted() {
		t.Fatalf("dovecot rejected the provisioned recipient %s: %#v", lmtpRecipientOne, recipients[0])
	}
	if !recipients[1].Accepted() {
		t.Fatalf("dovecot rejected the provisioned recipient %s: %#v", lmtpRecipientTwo, recipients[1])
	}
	if recipients[2].Accepted() {
		t.Fatalf("dovecot unexpectedly accepted the unregistered recipient %s: %#v", lmtpRecipientUnknown, recipients[2])
	}
	if rejectErr := recipients[2].Err(); rejectErr == nil || rejectErr.Code < 500 || rejectErr.Code >= 600 {
		t.Fatalf("unregistered recipient %s rejection = %#v, want a 5yz RCPT reply", lmtpRecipientUnknown, recipients[2])
	}

	body := []byte("From: sender@example.test\r\nTo: " + lmtpRecipientOne + "\r\nSubject: lmtp-per-recipient\r\n\r\nhello from the LMTP interop test\r\n")

	result, err := client.Data(commandCtx, bytes.NewReader(body), nil)
	if err != nil {
		t.Fatalf("DATA: %v", err)
	}

	// Cardinality: RFC 2033 §4.2 — one final DATA reply per RCPT-accepted
	// recipient, in RCPT order, never one per RCPT command sent (which was
	// 3, including the rejected recipient) and never a single SMTP-style
	// reply for the whole message.
	if len(result) != 2 {
		t.Fatalf("DataResult has %d entries, want exactly 2 (one per RCPT-accepted recipient, excluding the rejected %s): %#v",
			len(result), lmtpRecipientUnknown, result)
	}
	if result[0].Recipient != lmtpRecipientOne || result[1].Recipient != lmtpRecipientTwo {
		t.Fatalf("DataResult recipient order = [%q %q], want [%q %q]",
			result[0].Recipient, result[1].Recipient, lmtpRecipientOne, lmtpRecipientTwo)
	}
	if !result[0].Accepted() {
		t.Fatalf("DATA final reply for %s = %#v, want accepted", lmtpRecipientOne, result[0])
	}
	if !result[1].Accepted() {
		t.Fatalf("DATA final reply for %s = %#v, want accepted", lmtpRecipientTwo, result[1])
	}

	// The load-bearing assertion: the two final replies must be genuinely
	// distinct wire replies, not the same reply attributed to both
	// recipients. Dovecot's LMTP final reply text embeds the recipient
	// address and a per-recipient transaction-id suffix for every recipient
	// after the first, so identical text here can only mean the client
	// collapsed two replies into one.
	if result[0].Text == result[1].Text {
		t.Fatalf("both DATA final replies carry identical text %q; want two distinct per-recipient replies (RFC 2033 §4.2)", result[0].Text)
	}
	if !strings.Contains(result[0].Text, lmtpRecipientOne) {
		t.Fatalf("DATA final reply for %s = %q, want it to reference its own recipient address", lmtpRecipientOne, result[0].Text)
	}
	if !strings.Contains(result[1].Text, lmtpRecipientTwo) {
		t.Fatalf("DATA final reply for %s = %q, want it to reference its own recipient address", lmtpRecipientTwo, result[1].Text)
	}
	if strings.Contains(result[0].Text, lmtpRecipientTwo) || strings.Contains(result[1].Text, lmtpRecipientOne) {
		t.Fatalf("DATA final replies reference the wrong recipient: %q / %q", result[0].Text, result[1].Text)
	}

	// Round-trip both mailboxes independently: two distinct deliveries
	// occurred, not one write read back twice.
	sinkCtx, sinkCancel := context.WithTimeout(ctx, cfg.SinkTimeout)
	defer sinkCancel()
	msgOne, err := harness.WaitForMessage(sinkCtx, sink, lmtpRecipientOne)
	if err != nil {
		t.Fatalf("retrieving delivery to %s: %v", lmtpRecipientOne, err)
	}
	msgTwo, err := harness.WaitForMessage(sinkCtx, sink, lmtpRecipientTwo)
	if err != nil {
		t.Fatalf("retrieving delivery to %s: %v", lmtpRecipientTwo, err)
	}
	want := bytes.TrimRight(normalizeLines(body), "\n")
	if have := bytes.TrimRight(normalizeLines(msgOne.Raw), "\n"); !bytes.Contains(have, want) {
		t.Fatalf("delivery to %s does not contain the submitted body\nwant contained in: %q\ngot: %q", lmtpRecipientOne, want, have)
	}
	if have := bytes.TrimRight(normalizeLines(msgTwo.Raw), "\n"); !bytes.Contains(have, want) {
		t.Fatalf("delivery to %s does not contain the submitted body\nwant contained in: %q\ngot: %q", lmtpRecipientTwo, want, have)
	}

	// NOOP after a complete LMTP DATA exchange proves the connection is
	// still synchronised: a reply-count mismatch anywhere above would have
	// poisoned it (smtpclient/lmtp.go), and this is the interop-level check
	// for that invariant.
	if err := client.Noop(commandCtx, nil); err != nil {
		t.Fatalf("NOOP after LMTP DATA: %v", err)
	}
}

const lmtpContainerPort = 24

// dovecotContainerfileDir locates interop/servers/dovecot relative to this
// file, so the container this test starts is built from exactly the
// Containerfile that package owns, without importing it (see the package
// doc comment above for why).
func dovecotContainerfileDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "interop", "servers", "dovecot")
}

// dovecotDoveadmSink is a deliberate duplicate of the small sink in
// interop/servers/dovecot/sink.go (doveadm rather than a raw maildir read,
// since Dovecot 2.4's mailbox_list_layout can change the on-disk layout
// without changing LMTP behavior). See the package doc comment above for
// why this test does not import that package directly.
type dovecotDoveadmSink struct {
	exec harness.Execer
}

func (s dovecotDoveadmSink) Fetch(ctx context.Context, recipient string) ([]harness.Message, error) {
	out, err := s.exec.Exec(ctx, "doveadm", "fetch", "-u", recipient, "text", "mailbox", "INBOX")
	if err != nil {
		return nil, fmt.Errorf("dovecot sink: fetch %s: %w", recipient, err)
	}
	out = bytes.TrimSpace(out)
	if len(out) == 0 {
		return nil, nil
	}
	const prefix = "text:"
	if !bytes.HasPrefix(out, []byte(prefix)) {
		return nil, fmt.Errorf("dovecot sink: unexpected doveadm output %q", out)
	}
	raw := bytes.TrimPrefix(out, []byte(prefix))
	raw = bytes.TrimPrefix(raw, []byte("\r\n"))
	raw = bytes.TrimPrefix(raw, []byte("\n"))
	return []harness.Message{{Recipient: recipient, Raw: raw}}, nil
}

func (s dovecotDoveadmSink) Reset(ctx context.Context, recipient string) error {
	_, err := s.exec.Exec(ctx, "doveadm", "expunge", "-u", recipient, "mailbox", "INBOX", "all")
	if err != nil {
		return fmt.Errorf("dovecot sink: reset %s: %w", recipient, err)
	}
	return nil
}
