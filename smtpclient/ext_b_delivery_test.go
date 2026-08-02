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
	if err := c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{Delivery: &smtp.DeliveryOptions{DSN: &smtp.DSNMailOptions{Return: smtp.DSNReturnFull, EnvelopeID: "queue+id=1"}}}); err != nil {
		t.Fatal(err)
	}
	err = c.Rcpt(context.Background(), "recipient@example.test", &smtp.RcptOptions{Delivery: &smtp.RecipientDeliveryOptions{DSN: &smtp.DSNRcptOptions{Notify: []smtp.DSNNotify{smtp.DSNNotifyFailure, smtp.DSNNotifyDelay}, OriginalType: "utf-8", Original: "someone+tag@example.test"}}})
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
	err = c.Mail(context.Background(), "sender@example.test", &smtp.MailOptions{Delivery: &smtp.DeliveryOptions{DSN: &smtp.DSNMailOptions{Return: smtp.DSNReturnFull}}})
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
