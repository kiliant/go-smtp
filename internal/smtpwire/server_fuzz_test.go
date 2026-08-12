package smtpwire

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func FuzzReadCommand(f *testing.F) {
	for _, seed := range []string{"NOOP\r\n", "MAIL FROM:<a@example.test> SIZE=1\r\n", "NOOP\n", "BDAT 3 LAST\r\nabc"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, wire string) {
		_, _ = NewLineReader(bytes.NewBufferString(wire)).ReadCommand(time.Time{}, Limits{MaxCommandLineLength: 1024})
	})
}

func FuzzParseHelloCommand(f *testing.F) {
	f.Add("EHLO", "client.example")
	f.Add("LHLO", "[127.0.0.1]")
	f.Fuzz(func(t *testing.T, verb, argument string) {
		_, _ = ParseHelloCommand(Command{Verb: verb, Argument: argument})
	})
}

func FuzzParseBDATArgument(f *testing.F) {
	f.Add("0")
	f.Add("12 LAST")
	f.Fuzz(func(t *testing.T, argument string) {
		_, _ = ParseBDATArgument(argument, Limits{MaxBDATChunkSize: 1 << 20})
	})
}

func FuzzEncodeReply(f *testing.F) {
	f.Add(250, "ok", 2, 0, 0, uint8(0))
	f.Fuzz(func(t *testing.T, code int, line string, class, subject, detail int, context uint8) {
		enhanced := EnhancedCode{Class: class, Subject: subject, Detail: detail}
		var wire bytes.Buffer
		_ = EncodeReply(&wire, Reply{Code: code, Lines: []string{line}}, ReplyOptions{Enhanced: &enhanced, Context: ReplyContext(context % 3)})
	})
}

func FuzzParsePath(f *testing.F) {
	f.Add(false, "FROM:<>", false, 256)
	f.Add(true, "TO:<Postmaster>", false, 256)
	f.Add(false, "FROM:<élise@例え.test>", true, 256)
	f.Fuzz(func(t *testing.T, forward bool, argument string, smtpUTF8 bool, max int) {
		if max < 0 {
			max = -max
		}
		max %= 4097
		opts := PathOptions{SMTPUTF8: smtpUTF8, MaxPathLength: max}
		if forward {
			_, _ = ParseForwardPath(argument, opts)
		} else {
			_, _ = ParseReversePath(argument, opts)
		}
	})
}

func FuzzFormatReceived(f *testing.F) {
	f.Add("client.example", "mx.example", "id", "<a@example.test>", 1, true, false, true, true)
	f.Fuzz(func(t *testing.T, from, by, id, forPath string, recipients int, extended, lmtp, tls, authenticated bool) {
		_, _ = FormatReceived(ReceivedOptions{
			From: from, By: by, ID: id, For: forPath,
			RecipientCount: recipients, Extended: extended, LMTP: lmtp,
			TLS: tls, Authenticated: authenticated,
			Timestamp: time.Unix(0, 0).UTC(),
		})
	})
}

func FuzzEncodeEHLOReply(f *testing.F) {
	f.Add("mx.example", "hello", "X-EXT", "one two")
	f.Fuzz(func(t *testing.T, domain, greeting, keyword, raw string) {
		var wire bytes.Buffer
		_ = EncodeEHLOReply(&wire, EHLOReply{
			Domain: domain, Greeting: greeting,
			Extensions: []Extension{{Keyword: keyword, Raw: raw}},
		})
	})
}

func FuzzReadBDATChunk(f *testing.F) {
	f.Add([]byte("abcNOOP\r\n"), uint16(3))
	f.Fuzz(func(t *testing.T, wire []byte, size uint16) {
		const max = 4096
		n := uint64(size % max)
		lr := NewLineReader(bytes.NewReader(wire))
		var dst strings.Builder
		_, _ = lr.ReadBDATChunk(&dst, n, time.Time{}, Limits{MaxBDATChunkSize: max})
	})
}
