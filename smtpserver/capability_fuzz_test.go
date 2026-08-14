package smtpserver

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

func FuzzServerCapabilityAdvertisement(f *testing.F) {
	f.Add([]byte("FUTURE"), []byte("opaque"), []byte("value"), uint32(10), uint32(100), uint32(20))
	f.Add([]byte{}, []byte{}, []byte{}, uint32(999999), uint32(0), uint32(1))
	f.Fuzz(func(t *testing.T, keywordSeed, paramsSeed, extraSeed []byte, mailMax, rcptMax, domainMax uint32) {
		if len(keywordSeed)+len(paramsSeed)+len(extraSeed) > 256 {
			return
		}
		keyword := smtp.Extension("X-FUZZ-" + upperHex(keywordSeed))
		params := "P=" + upperHex(paramsSeed)
		extra := "X_FUZZ=" + upperHex(extraSeed)
		limits := &smtp.Limits{
			MailMax:       mailMax % 1000000,
			RcptMax:       rcptMax % 1000000,
			RcptDomainMax: domainMax % 1000000,
			Extra:         extra,
		}
		session := extensionTestSession()
		session.ParameterExtensions = []ParameterExtension{{Keyword: keyword, Params: params}}
		session.Limits = limits
		if err := validateSession(session); err != nil {
			t.Fatalf("generated valid session rejected: %v", err)
		}

		server := &Server{mode: modeSMTP, maxMessage: 1 << 20}
		command := &commandSession{
			server:   server,
			backend:  session,
			state:    newProtocolState(modeSMTP),
			tlsState: &connectionTLSState{},
			extended: true,
		}
		extensions := command.capabilities()
		var wire bytes.Buffer
		if err := smtpwire.EncodeEHLOReply(&wire, smtpwire.EHLOReply{Domain: "server.example", Extensions: extensions}); err != nil {
			t.Fatalf("EncodeEHLOReply: %v", err)
		}
		advertisement := wire.String()
		if !strings.Contains(advertisement, string(keyword)+" "+params+"\r\n") {
			t.Fatalf("open extension missing from %q", advertisement)
		}
		if !strings.Contains(advertisement, "LIMITS "+formatLimits(*limits)+"\r\n") {
			t.Fatalf("LIMITS missing from %q", advertisement)
		}
		for i := range advertisement {
			if advertisement[i] == '\n' && (i == 0 || advertisement[i-1] != '\r') {
				t.Fatalf("bare LF in advertisement %q", advertisement)
			}
			if advertisement[i] == '\r' && (i+1 >= len(advertisement) || advertisement[i+1] != '\n') {
				t.Fatalf("bare CR in advertisement %q", advertisement)
			}
		}
		reply, err := smtpwire.NewLineReader(bytes.NewReader(wire.Bytes())).ReadReply(time.Time{}, smtpwire.Limits{})
		if err != nil {
			t.Fatalf("encoded advertisement is not a valid reply: %v", err)
		}
		if reply.Code != 250 {
			t.Fatalf("reply code = %d, want 250", reply.Code)
		}
	})
}

func upperHex(value []byte) string {
	if len(value) == 0 {
		return "0"
	}
	return strings.ToUpper(hex.EncodeToString(value))
}
