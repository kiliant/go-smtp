package smtpserver

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/kiliant/go-smtp/internal/smtpwire"
)

// TestDATAPathRejectsSMTPSmuggling proves the strict transparency reader is
// actually used by the server's DATA path. Parser-only tests cannot detect a
// server that accidentally bypasses DotUnstuffReader.
func TestDATAPathRejectsSMTPSmuggling(t *testing.T) {
	baseline := stableGoroutineBaseline()
	defer assertGoroutinesReturn(t, baseline, 4)
	tests := []struct {
		name string
		wire string
		want error
	}{
		{name: "LF dot LF", wire: "body\n.\nNOOP\r\n", want: smtpwire.ErrBareLFTerminator},
		{name: "CR dot CRLF", wire: "body\r.\r\nNOOP\r\n.\r\n", want: smtpwire.ErrMalformedTerminator},
		{name: "CRLF dot CR", wire: "body\r\n.\rNOOP\r\n", want: smtpwire.ErrMalformedTerminator},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := newCommandTestBackend(ModeSMTP)
			logged := make(chan error, 4)
			harness := newRawTestServer(t, ModeSMTP, backend.backend(), func(opts *ServerOptions) {
				opts.ErrorLog = func(event ErrorEvent) { logged <- event.Err }
			})
			harness.wantCommand("EHLO client.example", 250)
			harness.wantCommand("MAIL FROM:<sender@example.test>", 250)
			harness.wantCommand("RCPT TO:<recipient@example.test>", 250)
			harness.wantCommand("DATA", 354)

			writeTestCommand(t, harness.conn, test.wire)
			if err := harness.conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if line, err := harness.reader.ReadString('\n'); err == nil {
				t.Fatalf("server replied after smuggling marker: %q", line)
			} else {
				var timeout net.Error
				if errors.As(err, &timeout) && timeout.Timeout() {
					t.Fatalf("server hung after smuggling marker: %v", err)
				}
			}

			select {
			case err := <-logged:
				if !errors.Is(err, test.want) {
					t.Fatalf("logged error = %v, want %v", err, test.want)
				}
			case <-time.After(time.Second):
				t.Fatal("smuggling rejection was not logged")
			}
			if calls := backend.dataCallCount(); calls != 1 {
				t.Fatalf("Session.Data calls = %d, want 1", calls)
			}
		})
	}
}
