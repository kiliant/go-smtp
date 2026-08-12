package smtpclient

import (
	"context"
	"strings"
	"testing"

	smtp "github.com/kiliant/go-smtp"
)

func TestDSNParametersAreXtextEncoded(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 DSN\r\n")},
		{command: "MAIL FROM:<sender@example.test> RET=FULL ENVID=queue+2Bid+3D1", replies: fakeReplies("250 ok\r\n")},
		{command: "RCPT TO:<recipient@example.test> NOTIFY=FAILURE,DELAY ORCPT=utf-8;someone+2Btag@example.test", replies: fakeReplies("250 ok\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{Delivery: &smtp.DeliveryOptions{DSN: &smtp.DSNMailOptions{Return: smtp.DSNReturnFull, EnvelopeID: "queue+id=1"}}}, nil); err != nil {
		t.Fatal(err)
	}
	err = c.Rcpt(context.Background(), "recipient@example.test", &smtp.RcptOptions{Delivery: &smtp.RecipientDeliveryOptions{DSN: &smtp.DSNRcptOptions{Notify: []smtp.DSNNotify{smtp.DSNNotifyFailure, smtp.DSNNotifyDelay}, OriginalType: "utf-8", Original: "someone+tag@example.test"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
}

func TestDSNNotifyNeverRejectedLocally(t *testing.T) {
	_, err := dsnNotifyValue([]smtp.DSNNotify{smtp.DSNNotifyNever, smtp.DSNNotifyFailure})
	if err == nil || !strings.Contains(err.Error(), "alone") {
		t.Fatalf("dsnNotifyValue = %v, want NEVER-alone error", err)
	}
}

func TestDeliveryRejectsMissingExtensionBeforeWire(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")}}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{Delivery: &smtp.DeliveryOptions{DSN: &smtp.DSNMailOptions{Return: smtp.DSNReturnFull}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "DSN") {
		t.Fatalf("Mail = %v, want missing DSN error", err)
	}
}

func TestRRVSIsRecipientParameter(t *testing.T) {
	p, err := rrvsParam(&smtp.RRVSOptions{Timestamp: "2014-04-03T23:01:00Z", Disposition: "c"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Keyword != "RRVS" || p.Value != "2014-04-03T23:01:00Z;C" {
		t.Fatalf("RRVS = %#v", p)
	}
}

// TestRRVSOnMailRejectedWithActionableMessage is gone deliberately: T16 removed
// smtp.DeliveryOptions.RRVS, so RFC 7293's RCPT-only scope is now enforced by
// the type system instead of by a runtime error, and there is no longer a way to
// express the mistake this test made. A field a caller can only ever set to
// receive an error was also a field a server's receive-side parser could never
// fill — see docs/API-STABILITY.md §10. The replacement gate is
// TestAPISurfaceNoSenderLevelRRVS in the root package, which fails if the field
// is ever reintroduced; the RCPT-scoped positive path is covered above.

// TestDeliverByRequiresModeBeforeWrite covers the audit finding that BY=
// was emitted without its mandatory by-mode (RFC 2852 §4: by-value =
// by-time ";" by-mode [by-trace]). An unset Mode must fail locally, before
// MAIL reaches the wire — the fake script has no MAIL step, so writing it
// anyway fails the fake server's command match.
func TestDeliverByRequiresModeBeforeWrite(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 DELIVERBY 0\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{Delivery: &smtp.DeliveryOptions{DeliverBy: &smtp.DeliverByOptions{Seconds: 30}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "Mode") {
		t.Fatalf("Mail error = %v, want DELIVERBY Mode-required error", err)
	}
}

// TestDeliverByModeNAllowsZeroAndNegativeSeconds covers RFC 2852 §4: "In the
// case of a by-mode of 'N', it is possible that by-time may be zero or
// negative. This is not an error and should not be rejected as such."
func TestDeliverByModeNAllowsZeroAndNegativeSeconds(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 DELIVERBY 0\r\n")},
		{command: "MAIL FROM:<sender@example.test> BY=0;N", replies: fakeReplies("250 ok\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{Delivery: &smtp.DeliveryOptions{DeliverBy: &smtp.DeliverByOptions{Seconds: 0, Mode: "N"}}}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestDeliverByModeNAllowsNegativeSeconds(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 DELIVERBY 0\r\n")},
		{command: "MAIL FROM:<sender@example.test> BY=-30;N", replies: fakeReplies("250 ok\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{Delivery: &smtp.DeliveryOptions{DeliverBy: &smtp.DeliverByOptions{Seconds: -30, Mode: "N"}}}, nil); err != nil {
		t.Fatal(err)
	}
}

// TestDeliverByModeRRejectsNonPositiveSeconds is the regression guard that
// mode "R" (return if not delivered by by-time) keeps its original,
// stricter bound: only mode "N" gained the zero/negative allowance.
func TestDeliverByModeRRejectsNonPositiveSeconds(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 DELIVERBY 0\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{Delivery: &smtp.DeliveryOptions{DeliverBy: &smtp.DeliverByOptions{Seconds: 0, Mode: "R"}}}, nil)
	if err == nil || !strings.Contains(err.Error(), "1..999999999") {
		t.Fatalf("Mail error = %v, want DELIVERBY mode R bounds error", err)
	}
}

// TestDSNReturnAcceptsUnmodelledValue covers docs/API-STABILITY.md §1b: RET's
// vocabulary must stay open, gated only on the server having advertised DSN,
// not on a closed switch of known values.
func TestDSNReturnAcceptsUnmodelledValue(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 DSN\r\n")},
		{command: "MAIL FROM:<sender@example.test> RET=X-FUTURE-VALUE", replies: fakeReplies("250 ok\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{Delivery: &smtp.DeliveryOptions{DSN: &smtp.DSNMailOptions{Return: smtp.DSNReturn("x-future-value")}}}, nil); err != nil {
		t.Fatal(err)
	}
}

// TestDSNNotifyAcceptsUnmodelledValue is NOTIFY's counterpart to
// TestDSNReturnAcceptsUnmodelledValue.
func TestDSNNotifyAcceptsUnmodelledValue(t *testing.T) {
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 DSN\r\n")},
		{command: "MAIL FROM:<sender@example.test>", replies: fakeReplies("250 ok\r\n")},
		{command: "RCPT TO:<recipient@example.test> NOTIFY=X-FUTURE-VALUE", replies: fakeReplies("250 ok\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Mail(context.Background(), "sender@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	err = c.Rcpt(context.Background(), "recipient@example.test", &smtp.RcptOptions{Delivery: &smtp.RecipientDeliveryOptions{DSN: &smtp.DSNRcptOptions{Notify: []smtp.DSNNotify{smtp.DSNNotify("x-future-value")}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
}

// TestDSNNotifyDuplicateRejectedLocally is the regression guard that
// removing the closed-vocabulary check for NOTIFY did not also remove the
// genuinely semantic RFC 3461 §4.1 duplicate-token rule.
func TestDSNNotifyDuplicateRejectedLocally(t *testing.T) {
	_, err := dsnNotifyValue([]smtp.DSNNotify{smtp.DSNNotifyFailure, smtp.DSNNotifyFailure})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("dsnNotifyValue = %v, want duplicate error", err)
	}
}
