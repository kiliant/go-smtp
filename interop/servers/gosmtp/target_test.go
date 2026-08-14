package gosmtp

import (
	"bytes"
	"context"
	"testing"
	"time"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/interop/harness"
	"github.com/kiliant/go-smtp/smtpclient"
)

const testRecipient = "interop@example.test"

func TestTargetAdvertisesProfileAndRoundTripsThroughSink(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	target, err := Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := target.Stop(stopCtx); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})

	client, err := smtpclient.Dial(ctx, &smtpclient.ClientOptions{
		Address:         target.Addr(),
		Identity:        "interop-client.example.test",
		GreetingTimeout: time.Second,
		MailTimeout:     time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, extension := range ExpectedExtensions() {
		if _, ok := client.Extension(extension); !ok {
			t.Errorf("profile claims %s but target did not advertise it", extension)
		}
	}

	body := []byte("From: sender@example.test\r\nTo: interop@example.test\r\nSubject: transparency\r\n\r\nbefore\r\n.\r\n..after\r\n")
	if err := client.Mail(ctx, "sender@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := client.Rcpt(ctx, testRecipient, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Data(ctx, bytes.NewReader(body), nil); err != nil {
		t.Fatal(err)
	}

	message, err := harness.WaitForMessage(ctx, target.Sink(), testRecipient)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(message.Raw, []byte("Received: ")) || !bytes.HasSuffix(message.Raw, body) {
		t.Fatalf("retrieved message differs from submission\nsubmitted: %q\nretrieved: %q", body, message.Raw)
	}
}

func TestProfileIsRegisteredAsInProcessTierOne(t *testing.T) {
	profile, ok := harness.Lookup(Name)
	if !ok {
		t.Fatal("gosmtp profile is not registered")
	}
	if profile.Tier != harness.Tier1 || profile.Start == nil {
		t.Fatalf("profile tier/start = %v/%v", profile.Tier, profile.Start != nil)
	}
	if profile.Run.Image != "" || profile.Run.ContainerfileDir != "" {
		t.Fatalf("in-process profile has container config: %+v", profile.Run)
	}
	port, ok := profile.SMTPPort()
	if !ok || port.Container != SMTPPort {
		t.Fatalf("SMTP port = %+v, %v", port, ok)
	}
}

func TestOptionalParametersReachWrappedMemoryBackendAsTypedValues(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	target, err := Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := target.Stop(stopCtx); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})

	client, err := smtpclient.Dial(ctx, &smtpclient.ClientOptions{
		Address:         target.Addr(),
		Identity:        "interop-client.example.test",
		GreetingTimeout: time.Second,
		MailTimeout:     time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	limits, ok, err := client.Limits()
	if err != nil || !ok || limits != profileLimits {
		t.Fatalf("Limits = %+v, %v, %v; want %+v", limits, ok, err, profileLimits)
	}

	mailOptions := &smtp.MailOptions{
		Delivery: &smtp.DeliveryOptions{
			DSN: &smtp.DSNMailOptions{
				Return:     smtp.DSNReturnFull,
				EnvelopeID: "queue@example.test",
			},
			DeliverBy:     &smtp.DeliverByOptions{Seconds: 60, Mode: "R", Trace: true},
			FutureRelease: &smtp.FutureReleaseOptions{HoldForSeconds: 120},
			MTPriority:    smtp.MTPriority("-2"),
		},
		Legacy: &smtp.LegacyOptions{
			Solicit:   "org.example:ADV",
			TransitID: "YWJjMTIz",
			Submitter: "submitter@example.test",
			ConPerm:   true,
		},
		// The public client's typed REQUIRETLS sender option correctly requires
		// TLS. Extra lets this cleartext receive-side conformance test act as a
		// non-conforming peer and prove the server parser still produces the
		// typed value for its backend.
		Extra: []smtp.Param{{Keyword: "REQUIRETLS"}},
	}
	if err := client.Mail(ctx, "sender@example.test", mailOptions, nil); err != nil {
		t.Fatal(err)
	}
	rcptOptions := &smtp.RcptOptions{
		Delivery: &smtp.RecipientDeliveryOptions{
			DSN: &smtp.DSNRcptOptions{
				Notify:       []smtp.DSNNotify{smtp.DSNNotifyFailure, smtp.DSNNotifyDelay},
				OriginalType: string(smtp.ORcptAddressTypeRFC822),
				Original:     "original@example.test",
			},
			RRVS: &smtp.RRVSOptions{Timestamp: "2025-01-02T03:04:05Z", Disposition: "c"},
		},
		Legacy: &smtp.RecipientLegacyOptions{ConNeg: true},
	}
	if err := client.Rcpt(ctx, testRecipient, rcptOptions, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Data(ctx, bytes.NewReader([]byte("Subject: typed parameters\r\n\r\nbody\r\n")), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.WaitForMessage(ctx, target.Sink(), testRecipient); err != nil {
		t.Fatalf("memory sink was replaced or disconnected: %v", err)
	}

	mail, rcpt := target.params.latest()
	if mail == nil || mail.Delivery == nil || mail.Delivery.DSN == nil || mail.Legacy == nil {
		t.Fatalf("backend MAIL options missing typed groups: %+v", mail)
	}
	if mail.Delivery.DSN.Return != smtp.DSNReturnFull || mail.Delivery.DSN.EnvelopeID != "queue@example.test" {
		t.Errorf("backend DSN MAIL options = %+v", mail.Delivery.DSN)
	}
	if got := mail.Delivery.DeliverBy; got == nil || got.Seconds != 60 || got.Mode != "R" || !got.Trace {
		t.Errorf("backend DELIVERBY options = %+v", got)
	}
	if got := mail.Delivery.FutureRelease; got == nil || got.HoldForSeconds != 120 {
		t.Errorf("backend FUTURERELEASE options = %+v", got)
	}
	if mail.Delivery.MTPriority != smtp.MTPriority("-2") {
		t.Errorf("backend MT-PRIORITY = %q", mail.Delivery.MTPriority)
	}
	if !mail.Delivery.RequireTLS {
		t.Error("backend REQUIRETLS option is false")
	}
	if mail.Legacy.Solicit != "org.example:ADV" || mail.Legacy.TransitID != "YWJjMTIz" ||
		mail.Legacy.Submitter != "submitter@example.test" || !mail.Legacy.ConPerm {
		t.Errorf("backend legacy MAIL options = %+v", mail.Legacy)
	}

	if rcpt == nil || rcpt.Delivery == nil || rcpt.Delivery.DSN == nil || rcpt.Delivery.RRVS == nil || rcpt.Legacy == nil {
		t.Fatalf("backend RCPT options missing typed groups: %+v", rcpt)
	}
	if got := rcpt.Delivery.DSN; len(got.Notify) != 2 || got.Notify[0] != smtp.DSNNotifyFailure ||
		got.Notify[1] != smtp.DSNNotifyDelay || got.OriginalType != string(smtp.ORcptAddressTypeRFC822) ||
		got.Original != "original@example.test" {
		t.Errorf("backend DSN RCPT options = %+v", got)
	}
	if got := rcpt.Delivery.RRVS; got.Timestamp != "2025-01-02T03:04:05Z" || got.Disposition != "C" {
		t.Errorf("backend RRVS options = %+v", got)
	}
	if !rcpt.Legacy.ConNeg {
		t.Error("backend CONNEG option is false")
	}
}

func TestSinkResetIsRecipientScoped(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	target, err := Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		if err := target.Stop(stopCtx); err != nil {
			t.Errorf("Stop: %v", err)
		}
	})

	client, err := smtpclient.Dial(ctx, &smtpclient.ClientOptions{
		Address:         target.Addr(),
		Identity:        "interop-client.example.test",
		GreetingTimeout: time.Second,
		MailTimeout:     time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	const other = "other@example.test"
	if err := client.Mail(ctx, "sender@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	for _, recipient := range []string{testRecipient, other} {
		if err := client.Rcpt(ctx, recipient, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.Data(ctx, bytes.NewReader([]byte("Subject: reset\r\n\r\nbody\r\n")), nil); err != nil {
		t.Fatal(err)
	}
	if err := target.Sink().Reset(ctx, testRecipient); err != nil {
		t.Fatal(err)
	}
	if messages, err := target.Sink().Fetch(ctx, testRecipient); err != nil || len(messages) != 0 {
		t.Fatalf("reset recipient messages = %d, err = %v", len(messages), err)
	}
	if messages, err := target.Sink().Fetch(ctx, other); err != nil || len(messages) != 1 {
		t.Fatalf("other recipient messages = %d, err = %v", len(messages), err)
	}
}
