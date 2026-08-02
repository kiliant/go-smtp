//go:build interop

package smtpclient_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/interop/harness"
	"github.com/kiliant/go-smtp/smtpclient"

	_ "github.com/kiliant/go-smtp/interop/servers/postfix"
	_ "github.com/kiliant/go-smtp/interop/servers/stalwart"
)

const interopRecipient = "interop@example.test"

// TestDataTransparencyInterop drives the public smtpclient transaction path
// through two independent real servers. It covers DATA transparency fixtures
// and, where advertised, sends the same bytes through CHUNKING/BDAT and checks
// the retrieved content is equivalent.
func TestDataTransparencyInterop(t *testing.T) {
	cfg := harness.LoadConfig()
	profiles := harness.Selected(cfg)
	if len(profiles) == 0 {
		t.Skip("no Postfix or Stalwart profile selected")
	}
	for _, profile := range profiles {
		t.Run(profile.Name, func(t *testing.T) {
			runDataTransparencyProfile(t, cfg, profile)
		})
	}
}

func runDataTransparencyProfile(t *testing.T, cfg harness.Config, profile harness.Profile) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), cfg.StartTimeout+cfg.HealthTimeout+8*cfg.SinkTimeout)
	defer cancel()

	run := profile.Run
	run.Name = harness.ContainerName("smtpclient-" + profile.Name)
	h, err := harness.Run(ctx, run)
	if err != nil {
		t.Fatalf("starting %s: %v", profile.Name, err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), cfg.StopTimeout)
		defer stopCancel()
		if t.Failed() {
			if logs, logErr := h.Logs(stopCtx); logErr == nil {
				t.Logf("%s container logs:\n%s", profile.Name, logs)
			}
		}
		if err := h.Stop(stopCtx); err != nil {
			t.Errorf("stopping %s: %v", profile.Name, err)
		}
	})

	assertion := harness.AssertProfile(ctx, cfg, h, profile)
	if assertion.Outcome != harness.OutcomeOK {
		t.Fatalf("%s profile assertion: %s: %v", profile.Name, assertion.Outcome, assertion.Err)
	}
	sink, err := profile.NewSink(ctx, h)
	if err != nil {
		t.Fatalf("%s sink: %v", profile.Name, err)
	}
	port, ok := profile.SMTPPort()
	if !ok {
		t.Fatalf("%s has no SMTP port", profile.Name)
	}
	addr, ok := h.HostAddr(port.Container)
	if !ok {
		t.Fatalf("%s has no mapped SMTP port", profile.Name)
	}
	var client *smtpclient.Client
	fixturesOnClient := 0
	defer func() {
		if client != nil {
			_ = client.Close()
		}
	}()
	for _, fixture := range harness.Fixtures {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			if fixture.Name == "binary-with-nul" {
				t.Skip("no default profile advertises BINARYMIME")
			}
			if fixture.Name == "streaming-200mib" && os.Getenv("GO_SMTP_INTEROP_LARGE") != "1" {
				t.Skip("set GO_SMTP_INTEROP_LARGE=1 for the 200 MiB real-server transfer")
			}
			if fixture.RequiresExtension != "" && !profileAdvertises(profile, fixture.RequiresExtension) {
				t.Skipf("%s does not advertise %s", profile.Name, fixture.RequiresExtension)
			}
			// Stalwart bounds both connections and messages per session. Reuse a
			// session for four two-transfer fixtures, then rotate before reaching
			// either bound. This also tests multiple transactions per connection.
			if client == nil || fixturesOnClient == 4 {
				if client != nil {
					_ = client.Close()
				}
				var err error
				client, err = smtpclient.Dial(ctx, &smtpclient.ClientOptions{
					Address:         addr,
					Identity:        "interop-client.example.test",
					GreetingTimeout: 10 * time.Second,
					MailTimeout:     10 * time.Second,
				})
				if err != nil {
					t.Fatalf("dialing %s: %v", profile.Name, err)
				}
				fixturesOnClient = 0
			}
			fixturesOnClient++

			dataBody, accepted := deliverFixture(t, ctx, cfg, sink, client, fixture, false)
			if !accepted {
				return // strict servers may reject the fixture's deliberate 1001-octet line
			}
			if !profileAdvertises(profile, string(smtp.ExtChunking)) {
				return
			}
			chunkedBody, chunkedAccepted := deliverFixture(t, ctx, cfg, sink, client, fixture, true)
			if !chunkedAccepted {
				t.Fatal("server accepted DATA but rejected the equivalent BDAT transfer")
			}
			if !bytes.Equal(dataBody, chunkedBody) {
				t.Fatalf("DATA/BDAT retrieved bodies differ\nDATA: %q\nBDAT: %q", dataBody, chunkedBody)
			}
		})
	}
}

func deliverFixture(t *testing.T, parent context.Context, cfg harness.Config, sink harness.Sink, client *smtpclient.Client, fixture harness.Fixture, chunking bool) ([]byte, bool) {
	t.Helper()
	if err := sink.Reset(parent, interopRecipient); err != nil {
		t.Fatalf("resetting sink: %v", err)
	}
	body := append([]byte("From: sender@example.test\r\nTo: "+interopRecipient+"\r\nSubject: "+fixture.Name+"\r\n\r\n"), fixture.Body...)
	commandCtx, commandCancel := context.WithTimeout(parent, maxDuration(cfg.CommandTimeout, 2*time.Minute))
	defer commandCancel()
	err := sendFixture(commandCtx, client, fixture, body, chunking)
	if err != nil {
		var smtpErr *smtp.Error
		if fixture.Name == "line-length-1000-1001" && errors.As(err, &smtpErr) && smtpErr.Code >= 500 && smtpErr.Code < 600 {
			t.Logf("server rejected the deliberate 1001-octet line with %d", smtpErr.Code)
			return nil, false
		}
		t.Fatalf("sending fixture (chunking=%v): %v", chunking, err)
	}
	sinkCtx, sinkCancel := context.WithTimeout(parent, cfg.SinkTimeout)
	defer sinkCancel()
	message, err := harness.WaitForMessage(sinkCtx, sink, interopRecipient)
	if err != nil {
		t.Fatalf("retrieving fixture (chunking=%v): %v", chunking, err)
	}
	want := bytes.TrimRight(normalizeLines(fixture.Body), "\n")
	have := normalizeLines(message.Raw)
	subject := []byte("Subject: " + fixture.Name + "\n")
	subjectAt := bytes.Index(have, subject)
	if subjectAt < 0 {
		t.Fatalf("retrieved message lacks submitted header %q\nmessage: %q", subject, message.Raw)
	}
	// MTAs may add Message-Id, Date, or filtering headers after the submitted
	// Subject. Find the actual header/body separator following that subject
	// instead of assuming Subject remains the final header.
	separatorAt := bytes.Index(have[subjectAt:], []byte("\n\n"))
	if separatorAt < 0 {
		t.Fatalf("retrieved message has no header/body separator after %q\nmessage: %q", subject, message.Raw)
	}
	bodyAt := subjectAt + separatorAt + 2
	actual := bytes.TrimRight(have[bodyAt:], "\n")
	if !bytes.Equal(actual, want) {
		t.Fatalf("retrieved body differs from submitted fixture\nwant: %q\ngot:  %q", want, actual)
	}
	return actual, true
}

func sendFixture(ctx context.Context, client *smtpclient.Client, fixture harness.Fixture, body []byte, chunking bool) error {
	var mailOptions *smtp.MailOptions
	if fixture.Name == "eight-bit-body" {
		mailOptions = &smtp.MailOptions{Transport: &smtp.TransportOptions{Body: smtp.BodyType8BitMIME}}
	} else if fixture.Name == "smtp-utf8-recipient" {
		mailOptions = &smtp.MailOptions{Transport: &smtp.TransportOptions{SMTPUTF8: true}}
	}
	if err := client.Mail(ctx, "sender@example.test", mailOptions); err != nil {
		return err
	}
	recipientBatch := []smtpclient.Recipient{
		{Address: interopRecipient},
		{Address: interopRecipient},
	}
	switch fixture.Name {
	case "smtp-utf8-recipient":
		recipientBatch[1].Address = "intéröp@éxample.test"
	case "multi-recipient-one-invalid":
		recipientBatch[1].Address = "nobody@example.invalid"
	}
	recipients, err := client.RcptBatch(ctx, recipientBatch, nil)
	if err != nil {
		return err
	}
	if len(recipients) != 2 || !recipients[0].Accepted() {
		return fmt.Errorf("RcptBatch did not accept the provisioned recipient: %#v", recipients)
	}
	if fixture.Name == "multi-recipient-one-invalid" && recipients[1].Accepted() {
		return fmt.Errorf("RcptBatch unexpectedly accepted %s", recipientBatch[1].Address)
	}
	if fixture.Name != "smtp-utf8-recipient" && fixture.Name != "multi-recipient-one-invalid" && !recipients.AllAccepted() {
		return fmt.Errorf("RcptBatch rejected a real-server recipient: %v", recipients.Errors())
	}
	if _, err := client.Data(ctx, bytes.NewReader(body), &smtpclient.DataOptions{UseChunking: chunking}); err != nil {
		return err
	}
	return nil
}

func profileAdvertises(profile harness.Profile, keyword string) bool {
	for _, extension := range profile.ExpectedExtensions {
		if strings.EqualFold(string(extension), keyword) {
			return true
		}
	}
	return false
}

func normalizeLines(data []byte) []byte {
	return bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
}

func maxDuration(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}
