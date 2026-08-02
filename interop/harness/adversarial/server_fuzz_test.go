package adversarial

import (
	"context"
	"net"
	"testing"
	"time"
)

func FuzzScenarioServer(f *testing.F) {
	for _, scenario := range []Scenario{MalformedCode, MismatchedReply, BareLineEnding, NULReplyText, ManyEHLOKeywords, LongEHLOKeyword, Close421} {
		f.Add(string(scenario))
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 64 {
			t.Skip()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		defer cancel()
		s, err := Start(ctx, Scenario(raw))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		conn, err := net.DialTimeout("tcp", s.Addr(), 100*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(100 * time.Millisecond))
		_, _ = conn.Write([]byte("EHLO fuzz.test\r\n"))
		buf := make([]byte, 1024)
		_, _ = conn.Read(buf)
		_ = conn.Close()
	})
}
