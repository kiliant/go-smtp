package smtpserver

import "testing"

func FuzzDecodeSASLResponse(f *testing.F) {
	for _, seed := range []string{
		"",
		"=",
		"*",
		"AHVzZXIAcGFzc3dvcmQ=",
		"not base64",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, response string) {
		// The production decoder rejects decoded responses above 16 KiB. Keep
		// the encoded input similarly bounded so the fuzz harness itself cannot
		// become the source of unbounded allocation.
		if len(response) > 32<<10 {
			return
		}
		_, _ = decodeSASLResponse(response)
	})
}
