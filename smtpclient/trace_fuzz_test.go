package smtpclient

import (
	"strconv"
	"strings"
	"testing"

	"github.com/kiliant/go-smtp/internal/smtpwire"
)

// FuzzTraceRedaction guards the rule that a SASL payload never reaches a
// trace, for arbitrary command and reply shapes. Unit tests cover the
// mechanisms this client implements; the invariant has to hold for a
// mechanism, argument count or reply shape nobody has written yet, which is
// what a target buys over another table-driven test.
func FuzzTraceRedaction(f *testing.F) {
	f.Add("AUTH", "PLAIN", "AGFsaWNlAHMzY3JldA==", 334, "c2VydmVyLWNoYWxsZW5nZQ==")
	f.Add("AUTH", "LOGIN", "", 235, "2.7.0 authenticated")
	f.Add("auth", "SCRAM-SHA-256", "biwsbj11c2VyLA==", 334, "cj1mb28scz1iYXIsaT00MDk2")
	f.Add("AUTH", "", "", 501, "syntax error")
	f.Add("MAIL", "FROM:<a@b.test>", "", 250, "ok")
	f.Add("AUTHENTICATE", "PLAIN", "secret", 250, "ok")

	f.Fuzz(func(t *testing.T, verb, mechanism, payload string, code int, line string) {
		if len(verb) > 4<<10 || len(mechanism) > 4<<10 || len(payload) > 64<<10 || len(line) > 64<<10 {
			t.Skip()
		}

		var args []string
		switch {
		case mechanism == "" && payload == "":
		case payload == "":
			args = []string{mechanism}
		default:
			args = []string{mechanism, payload}
		}
		got := traceCommandLine(queuedCommand{verb: verb, args: args})

		if strings.EqualFold(verb, "AUTH") {
			// Every argument after the mechanism is payload and must be
			// replaced wholesale. Asserting the exact rendering, rather than
			// only absence of the payload, also catches a redaction that
			// leaks the payload's length.
			want := verb
			for i := range args {
				want += " "
				if i > 0 {
					want += redactedPayload
				} else {
					want += args[i]
				}
			}
			if got != want {
				t.Fatalf("traceCommandLine = %q, want %q", got, want)
			}
			// The containment check is the property that survives a rewrite
			// of the rendering above. It is skipped when the payload also
			// occurs in a non-redacted position, where a match says nothing.
			if len(payload) >= 8 && !strings.Contains(verb, payload) && !strings.Contains(mechanism, payload) {
				if strings.Contains(got, payload) {
					t.Fatalf("traceCommandLine leaked the AUTH payload: %q", got)
				}
			}
		} else if !strings.Contains(got, verb) {
			t.Fatalf("traceCommandLine = %q, want it to retain the verb %q", got, verb)
		}

		reply := smtpwire.Reply{Code: code, Text: line}
		if line != "" {
			reply.Lines = []string{line}
		}
		gotReply := traceReplyLine(reply)

		if code == 334 {
			// A 334 carries the SASL challenge. The whole line goes, so this
			// equality is the complete statement of the invariant: no server
			// text can reach the trace regardless of what the server sent.
			if want := "334 " + redactedPayload; gotReply != want {
				t.Fatalf("traceReplyLine = %q, want %q", gotReply, want)
			}
			return
		}
		// Every other reply is rendered, and a trace without the reply code is
		// not a protocol trace.
		if !strings.HasPrefix(gotReply, strconv.Itoa(code)) {
			t.Fatalf("traceReplyLine = %q, want it to start with the code %d", gotReply, code)
		}
	})
}
