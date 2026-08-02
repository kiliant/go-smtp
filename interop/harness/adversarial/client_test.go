package adversarial_test

import (
	"context"
	"errors"
	"testing"
	"time"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/interop/harness/adversarial"
	"github.com/kiliant/go-smtp/smtpclient"
)

// TestClientBoundaryScenarios points the hostile peers at the public client
// boundary. This is deliberately a normal test rather than a fuzz target:
// fuzzing opens millions of loopback sockets and can exhaust ephemeral ports,
// while this finite set proves that the documented hostile cases become the
// library's single protocol error type without a panic or a hang.
func TestClientBoundaryScenarios(t *testing.T) {
	for _, scenario := range []adversarial.Scenario{
		adversarial.MalformedCode,
		adversarial.MismatchedReply,
		adversarial.OversizedReplyLine,
		adversarial.BareLineEnding,
		adversarial.NULReplyText,
		adversarial.ManyEHLOKeywords,
		adversarial.LongEHLOKeyword,
		adversarial.Close421,
	} {
		t.Run(string(scenario), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			conn, cleanup := adversarial.Pipe(ctx, scenario)
			defer cleanup()

			client, err := smtpclient.NewClient(ctx, conn, &smtpclient.ClientOptions{
				Identity:        "hardening.test",
				GreetingTimeout: 250 * time.Millisecond,
				MailTimeout:     250 * time.Millisecond,
			})
			if err == nil {
				_ = client.Close()
				return
			}
			var protocol *smtp.Error
			if !errors.As(err, &protocol) {
				t.Fatalf("Dial(%s) returned %T, want *smtp.Error: %v", scenario, err, err)
			}
		})
	}
}
