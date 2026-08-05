//go:build interop

package examples_test

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/interop/harness"
	_ "github.com/kiliant/go-smtp/interop/servers/dovecot"
	_ "github.com/kiliant/go-smtp/interop/servers/exim"
	_ "github.com/kiliant/go-smtp/interop/servers/greenmail"
	_ "github.com/kiliant/go-smtp/interop/servers/james"
	_ "github.com/kiliant/go-smtp/interop/servers/maddy"
	_ "github.com/kiliant/go-smtp/interop/servers/mailpit"
	_ "github.com/kiliant/go-smtp/interop/servers/postfix"
	_ "github.com/kiliant/go-smtp/interop/servers/stalwart"
	"github.com/kiliant/go-smtp/smtpclient"
)

// TestExamplesAgainstInteropMatrix executes the protocol paths demonstrated by
// the standalone programs against every selected real server. Capability-
// specific paths are skipped visibly when a profile does not advertise them.
func TestExamplesAgainstInteropMatrix(t *testing.T) {
	cfg := harness.LoadConfig()
	profiles := harness.Selected(cfg)
	if len(profiles) == 0 {
		t.Fatal("no interop profiles selected")
	}
	for _, profile := range profiles {
		profile := profile
		t.Run(profile.Name, func(t *testing.T) {
			runExampleProfile(t, cfg, profile)
		})
	}
}

func runExampleProfile(t *testing.T, cfg harness.Config, profile harness.Profile) {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.StartTimeout+cfg.HealthTimeout+4*cfg.CommandTimeout)
	defer cancel()
	run := profile.Run
	run.Name = harness.ContainerName("examples-" + profile.Name)
	h, err := harness.Run(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopCtx, stop := context.WithTimeout(context.Background(), cfg.StopTimeout)
		defer stop()
		if err := h.Stop(stopCtx); err != nil {
			t.Errorf("stop: %v", err)
		}
	})
	assertion := harness.AssertProfile(ctx, cfg, h, profile)
	if assertion.Outcome != harness.OutcomeOK {
		t.Fatalf("profile assertion: %v", assertion.Err)
	}
	// Some profiles (james) require runtime provisioning of the recipient
	// mailbox before any RCPT to it will be accepted; NewSink is where that
	// provisioning lives (interop/matrix_test.go relies on the same call).
	// Other profiles' NewSink is a cheap, side-effect-free constructor, so
	// calling it unconditionally here is safe.
	if profile.NewSink != nil {
		if _, err := profile.NewSink(ctx, h); err != nil {
			t.Fatalf("provisioning sink: %v", err)
		}
	}
	port, lmtp := profile.LMTPPort()
	if smtpPort, ok := profile.SMTPPort(); ok {
		port, lmtp = smtpPort, false
	} else if !lmtp {
		t.Skip("profile has no plain SMTP or LMTP endpoint")
	}
	addr, ok := h.HostAddr(port.Container)
	if !ok {
		t.Fatalf("no host address for %d", port.Container)
	}

	c, err := smtpclient.Dial(ctx, &smtpclient.ClientOptions{Address: addr, Identity: "examples.interop.test", LMTP: lmtp, GreetingTimeout: 10 * time.Second, MailTimeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, ok := c.Extension(smtp.ExtStartTLS); ok {
		if err := c.StartTLS(ctx, &smtpclient.StartTLSOptions{InsecureSkipVerify: true}); err != nil {
			t.Fatalf("STARTTLS example: %v", err)
		}
	} else {
		t.Log("SKIP submission-starttls: STARTTLS not advertised")
	}
	if profile.Name == "greenmail" {
		if err := c.Auth(ctx, &smtpclient.AuthOptions{Username: "interop", Password: "interop-pw", Mechanisms: []string{"PLAIN"}, AllowInsecureAuth: true}); err != nil {
			t.Fatalf("AUTH example: %v", err)
		}
	} else {
		t.Log("SKIP AUTH: selected profile has no harness credentials")
	}
	t.Log("SKIP implicit-tls: current harness profiles expose no smtps endpoint")

	if err := c.Mail(ctx, "interop@example.test", nil); err != nil {
		t.Fatal(err)
	}
	recipients := []smtpclient.Recipient{{Address: "interop@example.test"}, {Address: "interop@example.test"}}
	if lmtp {
		recipients[1].Address = "not-provisioned@example.test"
	}
	rcpts, err := c.RcptBatch(ctx, recipients, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rcpts) != 2 {
		t.Fatalf("RcptBatch returned %d results", len(rcpts))
	}
	if lmtp {
		if len(rcpts.Errors()) != 1 {
			t.Fatalf("partial-rejection example returned %d rejected recipients, want 1", len(rcpts.Errors()))
		}
	} else {
		t.Log("SKIP forced partial rejection: this profile has no documented rejecting recipient; RcptBatch result handling still ran")
	}
	// A bounded synthetic reader proves Data consumes a caller-sized stream;
	// it does not allocate the 1 MiB message in memory. A minimal header
	// section precedes the body: Maddy, Mailpit and Stalwart all reject a
	// message that never resolves to a valid RFC 5322 header/body split.
	if _, err := c.Data(ctx, syntheticMessage('x', 1<<20), nil); err != nil {
		t.Fatal(err)
	}

	if _, ok := c.Extension(smtp.ExtDSN); ok {
		if err := c.Mail(ctx, "interop@example.test", &smtp.MailOptions{Delivery: &smtp.DeliveryOptions{DSN: &smtp.DSNMailOptions{Return: smtp.DSNReturnHeaders, EnvelopeID: "examples-42"}}}); err != nil {
			t.Fatalf("DSN MAIL: %v", err)
		}
		if err := c.Rcpt(ctx, "interop@example.test", &smtp.RcptOptions{Delivery: &smtp.RecipientDeliveryOptions{DSN: &smtp.DSNRcptOptions{Notify: []smtp.DSNNotify{smtp.DSNNotifyFailure}}}}); err != nil {
			t.Fatalf("DSN RCPT: %v", err)
		}
		if _, err := c.Data(ctx, syntheticMessage('\n', 1024), nil); err != nil {
			t.Fatalf("DSN DATA: %v", err)
		}

		// RET= is deliberately sent through Extra, despite having a typed
		// field, to exercise the unmodelled-parameter transport path.
		if err := c.Mail(ctx, "interop@example.test", &smtp.MailOptions{Extra: []smtp.Param{{Keyword: "RET", Value: "HDRS"}}}); err != nil {
			t.Fatalf("Extra MAIL: %v", err)
		}
		if err := c.Rcpt(ctx, "interop@example.test", nil); err != nil {
			t.Fatalf("Extra RCPT: %v", err)
		}
		if _, err := c.Data(ctx, syntheticMessage('\n', 1024), nil); err != nil {
			t.Fatalf("Extra DATA: %v", err)
		}
	} else {
		t.Log("SKIP dsn and extra-parameter runtime: DSN not advertised")
	}

	if _, ok := c.Extension(smtp.ExtChunking); !ok {
		t.Log("SKIP stream-bdat: CHUNKING not advertised")
		return
	}
	if err := c.Mail(ctx, "interop@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt(ctx, "interop@example.test", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Data(ctx, syntheticMessage('b', 1<<20), &smtpclient.DataOptions{UseChunking: true, ChunkSize: 64 << 10}); err != nil {
		t.Fatal(err)
	}
}

// syntheticMessage prepends a minimal RFC 5322 header section to a bounded
// repeatingReader body, so strict server-side parsers see a well-formed
// message rather than an unparsable stream of raw bytes.
func syntheticMessage(b byte, bodyLen int64) io.Reader {
	const header = "Subject: go-smtp interop synthetic message\r\n\r\n"
	return io.MultiReader(strings.NewReader(header), io.LimitReader(newRepeatingReader(b), bodyLen))
}

// repeatingReaderLineWidth keeps synthetic message bodies within RFC 5321
// §4.5.3.1.6's recommended line length. Without a periodic line break, a
// large repeatingReader stream is a single oversized SMTP line, which
// Maddy, Mailpit and Stalwart reject as unparsable rather than accept.
const repeatingReaderLineWidth = 78

type repeatingReader struct {
	b   byte
	col int
}

func newRepeatingReader(b byte) *repeatingReader {
	return &repeatingReader{b: b}
}

func (r *repeatingReader) Read(p []byte) (int, error) {
	for i := range p {
		if r.col == repeatingReaderLineWidth {
			p[i] = '\n'
			r.col = 0
			continue
		}
		p[i] = r.b
		r.col++
	}
	return len(p), nil
}
