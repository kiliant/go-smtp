package smtpsasl

import (
	"crypto/sha256"
	"hash"
	"testing"
)

func FuzzResponderConversation(f *testing.F) {
	f.Add("PLAIN", []byte("\x00user\x00password"), []byte(""), true)
	f.Add("OAUTHBEARER", []byte("n,a=user,\x01auth=Bearer token\x01\x01"), []byte("\x01"), false)
	f.Add("SCRAM-SHA-256", []byte("n,,n=user,r=nonce"), []byte("c=biws,r=nonce,p=AA=="), true)
	f.Fuzz(func(t *testing.T, name string, initial, response []byte, accepted bool) {
		responder, err := NewResponder(name, ResponderConfig{ChannelBinding: []byte("binding"), testNonce: "server"})
		if err != nil {
			return
		}
		step, err := responder.Start(initial)
		if err != nil {
			return
		}
		if step.Verification != nil {
			keys := scramTestKeys(funcHash(responder), "password", []byte("salt"), 1)
			step, err = responder.Continue(Verification{Accepted: accepted, Keys: &keys, FailureChallenge: []byte(`{"status":"invalid_token"}`)})
			if err != nil {
				return
			}
		}
		if !step.Done {
			_, _ = responder.Next(response)
		}
	})
}

func funcHash(r *Responder) func() hash.Hash {
	if r.newHash != nil {
		return r.newHash
	}
	return sha256.New
}
