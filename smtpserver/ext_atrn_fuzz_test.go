package smtpserver

import "testing"

func FuzzParseATRNDomains(f *testing.F) {
	for _, seed := range []string{
		"",
		"example.test",
		"example.test,mail.example.test",
		"-invalid.example",
		"example.test bad.example",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, argument string) {
		if len(argument) > 4096 {
			return
		}
		_, _ = parseATRNDomains(argument)
	})
}
