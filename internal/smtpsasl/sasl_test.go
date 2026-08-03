package smtpsasl

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestCRAMMD5Vector(t *testing.T) {
	m, err := New("CRAM-MD5", Config{Username: "tim", Password: "tanstaaftanstaaf"})
	if err != nil {
		t.Fatal(err)
	}
	v, done, err := m.Next([]byte("<1896.697170952@postoffice.reston.mci.net>"))
	if err != nil || !done {
		t.Fatalf("Next = %q, %v, %v", v, done, err)
	}
	if got, want := string(v), "tim b913a602c7eda7a495b4e6e7334d3890"; got != want {
		t.Fatalf("response = %q, want %q", got, want)
	}
}

func TestSCRAMSHA256Vector(t *testing.T) {
	m, err := New("SCRAM-SHA-256", Config{Username: "user", Password: "pencil"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.Start()
	if err != nil {
		t.Fatal(err)
	}
	nonce := strings.Split(string(first), "r=")[1]
	serverFirst := "r=" + nonce + "srv,s=QSXCR+Q6sek8bf92,i=4096"
	response, done, err := m.Next([]byte(serverFirst))
	if err != nil || done || !strings.Contains(string(response), "p=") {
		t.Fatalf("client-final = %q, %v, %v", response, done, err)
	}
	// The server verifier is deliberately checked by the mechanism; a wrong one
	// must not silently authenticate.
	if _, _, err := m.Next([]byte("v=" + base64.StdEncoding.EncodeToString([]byte("wrong")))); err == nil {
		t.Fatal("accepted invalid SCRAM verifier")
	}
}

// TestSCRAMSHA1KnownAnswer replays the exact exchange published in RFC 5802
// §5 (username "user", password "pencil") and checks the client-final
// message — including the "p=" proof — and the accepted server signature
// against the RFC's published bytes verbatim. Unlike TestSCRAMSHA256Vector,
// this pins the client nonce (via the unexported Config.testNonce test seam)
// so the entire message can be compared byte-for-byte, not merely inspected
// for shape. That is the only way to catch a crypto-construction bug such as
// a swapped Client Key/Server Key label, which a self-consistency check
// cannot detect.
func TestSCRAMSHA1KnownAnswer(t *testing.T) {
	m, err := New("SCRAM-SHA-1", Config{
		Username:  "user",
		Password:  "pencil",
		testNonce: "fyko+d2lbbFgONRv9qkxdawL",
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := m.Start()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(first), "n,,n=user,r=fyko+d2lbbFgONRv9qkxdawL"; got != want {
		t.Fatalf("client-first = %q, want %q", got, want)
	}

	const serverFirst = "r=fyko+d2lbbFgONRv9qkxdawL3rfcNHYJY1ZVvWVs7j,s=QSXCR+Q6sek8bf92,i=4096"
	clientFinal, done, err := m.Next([]byte(serverFirst))
	if err != nil {
		t.Fatal(err)
	}
	if done {
		t.Fatal("done after client-final, want false")
	}
	const wantClientFinal = "c=biws,r=fyko+d2lbbFgONRv9qkxdawL3rfcNHYJY1ZVvWVs7j,p=v0X8v3Bz2T0CJGbJQyF0X+HI4Ts="
	if got := string(clientFinal); got != wantClientFinal {
		t.Fatalf("client-final = %q, want %q", got, wantClientFinal)
	}

	const serverFinal = "v=rmF9pqV8S7suAoZWja4dJRkFsKQ="
	resp, done, err := m.Next([]byte(serverFinal))
	if err != nil {
		t.Fatalf("rejected the RFC 5802 server signature: %v", err)
	}
	if !done {
		t.Fatal("done = false after a correct server signature, want true")
	}
	if resp != nil {
		t.Fatalf("response = %q, want nil", resp)
	}
}

// TestSCRAMSHA256KnownAnswer replays the exact exchange published in RFC 7677
// §3 (username "user", password "pencil") and checks the client-final
// message and accepted server signature against the RFC's published bytes
// verbatim. See TestSCRAMSHA1KnownAnswer for why this is load-bearing beyond
// TestSCRAMSHA256Vector's self-consistency check.
func TestSCRAMSHA256KnownAnswer(t *testing.T) {
	m, err := New("SCRAM-SHA-256", Config{
		Username:  "user",
		Password:  "pencil",
		testNonce: "rOprNGfwEbeRWgbNEkqO",
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := m.Start()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(first), "n,,n=user,r=rOprNGfwEbeRWgbNEkqO"; got != want {
		t.Fatalf("client-first = %q, want %q", got, want)
	}

	const serverFirst = "r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096"
	clientFinal, done, err := m.Next([]byte(serverFirst))
	if err != nil {
		t.Fatal(err)
	}
	if done {
		t.Fatal("done after client-final, want false")
	}
	const wantClientFinal = "c=biws,r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,p=dHzbZapWIk4jUhN+Ute9ytag9zjfMHgsqmmiz7AndVQ="
	if got := string(clientFinal); got != wantClientFinal {
		t.Fatalf("client-final = %q, want %q", got, wantClientFinal)
	}

	const serverFinal = "v=6rriTRBi23WpRR/wtup+mMhUZUn/dB5nLTJRsjl95G4="
	resp, done, err := m.Next([]byte(serverFinal))
	if err != nil {
		t.Fatalf("rejected the RFC 7677 server signature: %v", err)
	}
	if !done {
		t.Fatal("done = false after a correct server signature, want true")
	}
	if resp != nil {
		t.Fatalf("response = %q, want nil", resp)
	}
}

func TestSCRAMRejectsExcessiveIterations(t *testing.T) {
	m, err := New("SCRAM-SHA-256", Config{Username: "user", Password: "pencil"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := m.Start()
	if err != nil {
		t.Fatal(err)
	}
	nonce := strings.Split(string(first), "r=")[1]
	if _, _, err := m.Next([]byte("r=" + nonce + "srv,s=QSXCR+Q6sek8bf92,i=100001")); err == nil {
		t.Fatal("accepted an excessive SCRAM iteration count")
	}
}
