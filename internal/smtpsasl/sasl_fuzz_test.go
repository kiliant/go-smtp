package smtpsasl

import "testing"

// FuzzMechanismConversation feeds hostile server challenges through every
// implemented mechanism. SCRAM's iteration cap is especially important: the
// challenge is server controlled and must not turn into unbounded CPU work.
func FuzzMechanismConversation(f *testing.F) {
	f.Add("PLAIN", []byte("x"), []byte("y"))
	f.Add("LOGIN", []byte("username"), []byte("password"))
	f.Add("CRAM-MD5", []byte("challenge"), []byte("extra"))
	f.Add("SCRAM-SHA-256", []byte("r=bad,s=AA==,i=100001"), []byte(""))
	f.Add("SCRAM-SHA-1", []byte("r=,s=AA==,i=1"), []byte(""))
	f.Add("XOAUTH2", []byte("\x00"), []byte(""))
	f.Add("unknown", []byte(""), []byte(""))

	f.Fuzz(func(t *testing.T, name string, first, second []byte) {
		if len(first) > 64<<10 || len(second) > 64<<10 {
			t.Skip()
		}
		m, err := New(name, Config{Username: "user", Password: "password", Token: "token", ChannelBinding: []byte("binding")})
		if err != nil {
			return
		}
		if _, err := m.Start(); err != nil {
			return
		}
		_, _, _ = m.Next(first)
		_, _, _ = m.Next(second)
		_, _, _ = m.Next(second)
	})
}
