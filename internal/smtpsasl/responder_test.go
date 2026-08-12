package smtpsasl

import (
	"bytes"
	"crypto/sha1" // #nosec G505 -- test covers the required SCRAM-SHA-1 mechanism.
	"crypto/sha256"
	"hash"
	"testing"
)

func TestPlainResponderPreservesBothIdentities(t *testing.T) {
	client, err := New("PLAIN", Config{Username: "authcid", Password: "secret", AuthorizationID: "authzid"})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := client.Start()
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewResponder("PLAIN", ResponderConfig{})
	if err != nil {
		t.Fatal(err)
	}
	step, err := server.Start(initial)
	if err != nil {
		t.Fatal(err)
	}
	credentials := step.Verification.Credentials
	if credentials.AuthenticationID != "authcid" || credentials.AuthorizationID != "authzid" || credentials.Password != "secret" {
		t.Fatalf("credentials = %#v", credentials)
	}
	step, err = server.Continue(Verification{Accepted: true})
	if err != nil || !step.Done || !step.Accepted {
		t.Fatalf("accepted completion = (%#v, %v)", step, err)
	}
}

func TestLoginResponderConversation(t *testing.T) {
	client, _ := New("LOGIN", Config{Username: "user", Password: "secret"})
	server, _ := NewResponder("LOGIN", ResponderConfig{})
	initial, _ := client.Start()
	step, err := server.Start(initial)
	if err != nil || !bytes.Equal(step.Challenge, []byte("Username:")) {
		t.Fatalf("initial server step = (%#v, %v)", step, err)
	}
	response, _, _ := client.Next(step.Challenge)
	step, err = server.Next(response)
	if err != nil || !bytes.Equal(step.Challenge, []byte("Password:")) {
		t.Fatalf("password challenge = (%#v, %v)", step, err)
	}
	response, done, _ := client.Next(step.Challenge)
	if !done {
		t.Fatal("client did not finish LOGIN response")
	}
	step, err = server.Next(response)
	if err != nil || step.Verification == nil {
		t.Fatalf("LOGIN verification = (%#v, %v)", step, err)
	}
	if got := step.Verification.Credentials; got.AuthenticationID != "user" || got.Password != "secret" {
		t.Fatalf("credentials = %#v", got)
	}
	step, err = server.Continue(Verification{Accepted: true})
	if err != nil || !step.Done || !step.Accepted {
		t.Fatalf("LOGIN completion = (%#v, %v)", step, err)
	}
}

func TestCRAMMD5ResponderSurfacesChallengeResponse(t *testing.T) {
	challenge := []byte("<1896.697170952@postoffice.reston.mci.net>")
	client, _ := New("CRAM-MD5", Config{Username: "tim", Password: "tanstaaftanstaaf"})
	server, _ := NewResponder("CRAM-MD5", ResponderConfig{CRAMChallenge: challenge})
	step, err := server.Start(nil)
	if err != nil || !bytes.Equal(step.Challenge, challenge) {
		t.Fatalf("challenge = (%q, %v)", step.Challenge, err)
	}
	response, _, _ := client.Next(step.Challenge)
	step, err = server.Next(response)
	if err != nil || step.Verification == nil {
		t.Fatalf("verification = (%#v, %v)", step, err)
	}
	request := step.Verification
	if request.Kind != VerifyChallengeResponse || !bytes.Equal(request.Challenge, challenge) || !bytes.Equal(request.Response, response) {
		t.Fatalf("request = %#v", request)
	}
	step, err = server.Continue(Verification{Accepted: true})
	if err != nil || !step.Done || !step.Accepted {
		t.Fatalf("CRAM-MD5 completion = (%#v, %v)", step, err)
	}
}

func TestExternalResponder(t *testing.T) {
	client, _ := New("EXTERNAL", Config{AuthorizationID: "cert-user"})
	server, _ := NewResponder("EXTERNAL", ResponderConfig{})
	initial, _ := client.Start()
	step, err := server.Start(initial)
	if err != nil || step.Verification == nil || step.Verification.Credentials.AuthorizationID != "cert-user" {
		t.Fatalf("EXTERNAL step = (%#v, %v)", step, err)
	}
	step, err = server.Continue(Verification{Accepted: true})
	if err != nil || !step.Done || !step.Accepted {
		t.Fatalf("EXTERNAL completion = (%#v, %v)", step, err)
	}
}

func TestOAuthResponderSuccessAndDiagnosticFailure(t *testing.T) {
	for _, name := range []string{"OAUTHBEARER", "XOAUTH2"} {
		t.Run(name, func(t *testing.T) {
			client, _ := New(name, Config{Username: "user", AuthorizationID: "authz", Token: "token"})
			initial, _ := client.Start()

			server, _ := NewResponder(name, ResponderConfig{})
			step, err := server.Start(initial)
			if err != nil || step.Verification == nil || step.Verification.Credentials.Token != "token" {
				t.Fatalf("verification = (%#v, %v)", step, err)
			}
			accepted, err := server.Continue(Verification{Accepted: true})
			if err != nil || !accepted.Done || !accepted.Accepted {
				t.Fatalf("success = (%#v, %v)", accepted, err)
			}

			server, _ = NewResponder(name, ResponderConfig{})
			step, _ = server.Start(initial)
			diagnostic := []byte(`{"status":"invalid_token"}`)
			step, err = server.Continue(Verification{FailureChallenge: diagnostic})
			if err != nil || !bytes.Equal(step.Challenge, diagnostic) {
				t.Fatalf("failure challenge = (%#v, %v)", step, err)
			}
			dummy, _, _ := client.Next(step.Challenge)
			step, err = server.Next(dummy)
			if err != nil || !step.Done || step.Accepted {
				t.Fatalf("failure completion = (%#v, %v)", step, err)
			}
		})
	}
}

func TestSCRAMResponderConversation(t *testing.T) {
	tests := []struct {
		name           string
		newHash        func() hash.Hash
		channelBinding []byte
	}{
		{name: "SCRAM-SHA-1", newHash: sha1.New},
		{name: "SCRAM-SHA-1-PLUS", newHash: sha1.New, channelBinding: []byte("tls-exporter-value")},
		{name: "SCRAM-SHA-256", newHash: sha256.New},
		{name: "SCRAM-SHA-256-PLUS", newHash: sha256.New, channelBinding: []byte("tls-exporter-value")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, err := New(tt.name, Config{Username: "user", Password: "pencil", ChannelBinding: tt.channelBinding, testNonce: "clientnonce"})
			if err != nil {
				t.Fatal(err)
			}
			server, err := NewResponder(tt.name, ResponderConfig{ChannelBinding: tt.channelBinding, testNonce: "servernonce"})
			if err != nil {
				t.Fatal(err)
			}
			clientFirst, _ := client.Start()
			step, err := server.Start(clientFirst)
			if err != nil || step.Verification == nil || step.Verification.Kind != VerifySCRAMKeys {
				t.Fatalf("key lookup = (%#v, %v)", step, err)
			}
			keys := scramTestKeys(tt.newHash, "pencil", []byte("salt"), 4096)
			step, err = server.Continue(Verification{Accepted: true, Keys: &keys})
			if err != nil || step.Done || step.Challenge == nil {
				t.Fatalf("server-first = (%#v, %v); key lookup must not authenticate", step, err)
			}
			clientFinal, _, err := client.Next(step.Challenge)
			if err != nil {
				t.Fatalf("client final: %v", err)
			}
			step, err = server.Next(clientFinal)
			if err != nil || step.Done || step.Challenge == nil {
				t.Fatalf("server final = (%#v, %v)", step, err)
			}
			empty, done, err := client.Next(step.Challenge)
			if err != nil || !done || len(empty) != 0 {
				t.Fatalf("client verifies server = (%q, %t, %v)", empty, done, err)
			}
			step, err = server.Next(empty)
			if err != nil || !step.Done || !step.Accepted {
				t.Fatalf("completion = (%#v, %v)", step, err)
			}
		})
	}
}

func TestSCRAMResponderRejectsWrongProof(t *testing.T) {
	client, _ := New("SCRAM-SHA-256", Config{Username: "user", Password: "wrong", testNonce: "clientnonce"})
	server, _ := NewResponder("SCRAM-SHA-256", ResponderConfig{testNonce: "servernonce"})
	clientFirst, _ := client.Start()
	_, _ = server.Start(clientFirst)
	keys := scramTestKeys(sha256.New, "right", []byte("salt"), 4096)
	step, _ := server.Continue(Verification{Accepted: true, Keys: &keys})
	clientFinal, _, _ := client.Next(step.Challenge)
	step, err := server.Next(clientFinal)
	if err != nil || !step.Done || step.Accepted {
		t.Fatalf("wrong proof = (%#v, %v)", step, err)
	}
}

func scramTestKeys(newHash func() hash.Hash, password string, salt []byte, iterations int) SCRAMKeys {
	salted := hi(newHash, []byte(password), salt, iterations)
	clientKey := hmacSum(newHash, salted, []byte("Client Key"))
	return SCRAMKeys{
		Salt:       append([]byte(nil), salt...),
		Iterations: iterations,
		StoredKey:  hashSum(newHash, clientKey),
		ServerKey:  hmacSum(newHash, salted, []byte("Server Key")),
	}
}
