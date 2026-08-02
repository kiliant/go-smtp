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
