package smtpwire

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// Fuzz targets for every parser entry point in this package. The threat model
// is docs/tasks/T11-fuzzing-hardening.md: the server is untrusted, so any byte
// sequence must yield an error rather than a panic, an unbounded allocation or
// a hang.
//
// These targets assert totality and, where a round-trip identity exists, that
// it holds. They deliberately do not assert that any particular input is
// accepted — that is what the table-driven tests are for.

func FuzzReplyReader(f *testing.F) {
	f.Add(readFuzzSeed(f, "testdata/reply/multiline.txt"))
	f.Add(readFuzzSeed(f, "testdata/reply/bare-code.txt"))
	f.Add("250 OK\r\n")
	f.Add("250-first\r\n250 second\r\n")
	f.Add("550 5.7.1 rejected\r\n")
	f.Add("250-mail.example.test\r\n250-SIZE 10485760\r\n250 PIPELINING\r\n")
	// Code mismatch across continuation lines: a protocol error, not a panic.
	f.Add("250-one\r\n251 two\r\n")
	// Degenerate and hostile shapes.
	f.Add("2\r\n")
	f.Add("250\r\n")
	f.Add("250-\r\n")
	f.Add("999 out of range\r\n")
	f.Add("250 \x00NUL in text\r\n")
	f.Add("250 bare LF\n")
	f.Add("250 bare CR\r")
	f.Add("")

	f.Fuzz(func(t *testing.T, s string) {
		lr := NewLineReader(strings.NewReader(s))
		// Small limits keep the fuzzer honest about the pre-allocation
		// checks rather than letting it spend its budget on large inputs.
		limits := Limits{
			MaxReplyLineLength: 512,
			MaxReplyLines:      16,
			MaxReplySize:       4096,
		}
		for i := 0; i < 8; i++ {
			reply, err := lr.ReadReply(time.Time{}, limits)
			if err != nil {
				return
			}
			// A successfully parsed reply must satisfy the invariants the
			// rest of the library relies on.
			if reply.Code < 200 || reply.Code > 599 {
				t.Fatalf("accepted out-of-range code %d from %q", reply.Code, s)
			}
			if len(reply.Lines) == 0 {
				t.Fatalf("accepted reply with no lines from %q", s)
			}
		}
	})
}

func FuzzEHLOParse(f *testing.F) {
	f.Add(readFuzzSeed(f, "testdata/ehlo/extensions.txt"))
	// Captures from the live podman matrix. T11 requires the corpus be seeded
	// from real interop captures as well as invented shapes: real servers
	// disagree in ways a hand-written seed does not anticipate — Postfix
	// advertises a bare SIZE, Stalwart advertises SIZE with a value, and the
	// keyword orderings differ. See testdata/ehlo/interop/README.md.
	for _, server := range []string{"postfix", "exim", "stalwart", "maddy", "mailpit", "greenmail"} {
		f.Add(readFuzzSeed(f, "testdata/ehlo/interop/"+server+".txt"))
	}
	f.Add("mail.example.test\nSIZE 10485760\nPIPELINING\n8BITMIME")
	f.Add("mail.example.test Greetings\nAUTH PLAIN LOGIN")
	f.Add("example.test\nLIMITS RCPTMAX=100 MAILMAX=1000")
	f.Add("")
	f.Add("\n")
	f.Add("host\n\n\n")
	f.Add("host\nKEYWORD-WITH-\x00-NUL")
	f.Add("host\n   \nSIZE")

	f.Fuzz(func(t *testing.T, s string) {
		lines := strings.Split(s, "\n")
		reply, err := ParseEHLOReply(lines)
		if err != nil {
			return
		}
		// The greeting domain is line one and must never be reported as an
		// extension — the specific bug T01's spec calls out.
		for _, ext := range reply.Extensions {
			if ext.Keyword == "" {
				t.Fatalf("accepted empty extension keyword from %q", s)
			}
			if ext.Keyword == reply.Domain && reply.Domain != "" {
				// Not automatically wrong (a server may advertise a keyword
				// equal to its hostname), but the domain must at least have
				// been recorded separately rather than only as an extension.
				if len(lines) > 0 && strings.HasPrefix(lines[0], ext.Keyword) && len(reply.Extensions) == len(lines) {
					t.Fatalf("greeting line parsed as an extension: %q", s)
				}
			}
		}
	})
}

func FuzzDotStuffRoundTrip(f *testing.F) {
	f.Add([]byte(readFuzzSeed(f, "testdata/dotstuff/leading-dot.bin")))
	f.Add([]byte("hello world\r\n"))
	f.Add([]byte(".\r\n"))
	f.Add([]byte("..leading\r\n"))
	f.Add([]byte("no trailing terminator"))
	f.Add([]byte(".\r\n.\r\n.\r\n"))
	f.Add([]byte("a\n.\nb\r\n"))
	f.Add([]byte("a\r.b\r\n"))
	f.Add([]byte(""))
	f.Add([]byte("\r\n"))
	f.Add([]byte{0x00, 0x0d, 0x0a, 0x2e, 0x0d, 0x0a})
	// Bare CR/LF vectors: RFC 5321 §2.3.8 normalisation, and the
	// "<CR>.<CR><LF>" smuggling shape the writer used to pass through
	// unstuffed.
	f.Add([]byte("a\r.\r\n"))
	f.Add([]byte("\r"))
	f.Add([]byte("\n"))
	f.Add([]byte("\r\r\n\n"))

	f.Fuzz(func(t *testing.T, content []byte) {
		var stuffed bytes.Buffer
		w := NewDotStuffWriter(&stuffed)
		// Write one byte at a time: the filter is stateful across calls, and
		// a "." at the end of one Write with the newline in the next is the
		// bug a single-Write test never catches.
		for _, b := range content {
			if _, err := w.Write([]byte{b}); err != nil {
				t.Fatalf("Write: %v", err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}

		lr := NewLineReader(bytes.NewReader(stuffed.Bytes()))
		got, err := io.ReadAll(NewDotUnstuffReader(lr))
		if err != nil {
			t.Fatalf("unstuff %q (stuffed %q): %v", content, stuffed.Bytes(), err)
		}

		// The writer normalises line endings (RFC 5321 §2.3.8), so the
		// round-trip identity is unstuff(stuff(x)) == normalizeCRLF(x),
		// not == x. See wantRoundTrip in dotstuff_test.go.
		want := wantRoundTrip(content)
		if !bytes.Equal(got, want) {
			t.Fatalf("round-trip mismatch: content %q -> stuffed %q -> got %q, want %q",
				content, stuffed.Bytes(), got, want)
		}

		// Independently of the round trip: no bare CR and no bare LF may
		// ever reach the wire.
		out := stuffed.Bytes()
		for i := range out {
			if out[i] == '\r' && (i+1 >= len(out) || out[i+1] != '\n') {
				t.Fatalf("bare CR at offset %d in %q (content %q)", i, out, content)
			}
			if out[i] == '\n' && (i == 0 || out[i-1] != '\r') {
				t.Fatalf("bare LF at offset %d in %q (content %q)", i, out, content)
			}
		}
	})
}

func FuzzParamEncode(f *testing.F) {
	f.Add("SIZE", "10485760")
	f.Add("REQUIRETLS", "")
	f.Add("BODY", "8BITMIME")
	f.Add("ENVID", "QQ@example.test")
	f.Add("", "")
	f.Add("KEY WITH SPACE", "v")
	f.Add("KEY", "value with space")
	f.Add("KEY", "value=with=equals")
	f.Add("KEY", "\x00")
	f.Add("KEY", "\r\n250 injected")

	f.Fuzz(func(t *testing.T, keyword, value string) {
		out, err := EncodeParam(Param{Keyword: keyword, Value: value})
		if err != nil {
			return
		}
		// Anything that encodes must be safe to put on the wire: no CR, LF
		// or space can appear, or the command line desynchronises.
		if strings.ContainsAny(out, "\r\n ") {
			t.Fatalf("EncodeParam(%q,%q) = %q, which contains a command-line delimiter",
				keyword, value, out)
		}
	})
}

func FuzzXtextRoundTrip(f *testing.F) {
	f.Add("plain")
	f.Add("QQ@example.test")
	f.Add("has space")
	f.Add("has+plus")
	f.Add("has=equals")
	f.Add("")
	f.Add("\x00\x01\x7f")
	f.Add("üñïçödé")

	f.Fuzz(func(t *testing.T, raw string) {
		enc := EncodeXtext(raw)
		// Encoded xtext must itself be safe on a command line.
		if strings.ContainsAny(enc, "\r\n ") {
			t.Fatalf("EncodeXtext(%q) = %q, which contains a command-line delimiter", raw, enc)
		}
		got, err := DecodeXtext(enc)
		if err != nil {
			t.Fatalf("DecodeXtext(EncodeXtext(%q)) = error %v", raw, err)
		}
		if got != raw {
			t.Fatalf("xtext round-trip: %q -> %q -> %q", raw, enc, got)
		}
	})
}

func FuzzDecodeXtext(f *testing.F) {
	f.Add("plain")
	f.Add("a+20b")
	f.Add("+")
	f.Add("+2")
	f.Add("+ZZ")
	f.Add("+2G")
	f.Add("\x00")
	f.Add("a b")

	// Totality only: arbitrary xtext must never panic.
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = DecodeXtext(s)
	})
}

func FuzzExtractEnhancedCode(f *testing.F) {
	f.Add("2.1.0 OK")
	f.Add("5.7.1 Access denied")
	f.Add("OK")
	f.Add("")
	f.Add("5.7 truncated")
	f.Add("9.9.9 out of range class")
	f.Add("5.7.1")
	f.Add("999.999.999 huge")
	f.Add("5.7.1234567890123456789012345 overflow")

	f.Fuzz(func(t *testing.T, text string) {
		code, rest, ok := ExtractEnhancedCode(text)
		if !ok {
			return
		}
		// The raw form must always be retained — flattening an unrecognised
		// code is the data loss docs/API-STABILITY.md §1c forbids.
		if code.Raw == "" {
			t.Fatalf("ExtractEnhancedCode(%q) reported ok with an empty Raw", text)
		}
		if len(rest) > len(text) {
			t.Fatalf("ExtractEnhancedCode(%q) returned rest longer than input: %q", text, rest)
		}
	})
}

// readFuzzSeed loads a checked-in corpus entry. Keeping representative wire
// inputs under testdata makes them available to `go test -fuzz` as durable
// regression seeds, rather than leaving every useful input embedded only in
// source code.
func readFuzzSeed(f *testing.F, name string) string {
	f.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		f.Fatalf("read fuzz seed %s: %v", name, err)
	}
	return string(b)
}
