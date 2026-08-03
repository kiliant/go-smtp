package smtpclient

import (
	"context"
	"encoding/base64"
	"testing"
)

// The two constants below are the discriminating pair documented in
// docs/INTEROP.md: the same password written with the two code points that
// NFKC folds together. U+00B5 MICRO SIGN normalises to U+03BC GREEK SMALL
// LETTER MU under RFC 4013, so the prepared and unprepared forms differ in
// the bytes that reach the wire — which is the only way to prove preparation
// actually happened.
//
// T04 states the requirement directly: "a test that passes with preparation
// disabled is asserting nothing." Both halves are therefore asserted here,
// against exact wire bytes, and a third assertion pins that they differ.
const (
	microSignPassword = "interop-pw-µ" // c2 b5
	greekMuPassword   = "interop-pw-μ" // ce bc
)

func plainInitialResponse(user, password string) string {
	return base64.StdEncoding.EncodeToString([]byte("\x00" + user + "\x00" + password))
}

// TestAuthSASLPrepNormalisesPasswordOnTheWire is the positive half: with
// preparation on, the MICRO SIGN password must be sent as the GREEK MU form.
func TestAuthSASLPrepNormalisesPasswordOnTheWire(t *testing.T) {
	want := plainInitialResponse("user", greekMuPassword)
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 AUTH PLAIN\r\n")},
		{command: "AUTH PLAIN " + want, replies: fakeReplies("235 accepted\r\n")},
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Auth(context.Background(), &AuthOptions{
		Username:          "user",
		Password:          microSignPassword,
		SASLPrep:          true,
		AllowInsecureAuth: true,
	}); err != nil {
		t.Fatalf("Auth with SASLPrep: %v", err)
	}
}

// TestAuthWithoutSASLPrepSendsRawOctets is the negative half. It is what
// makes the test above meaningful: preparation is opt-in precisely because
// many servers compare stored raw octets, so the unprepared path must send
// the MICRO SIGN bytes unchanged.
func TestAuthWithoutSASLPrepSendsRawOctets(t *testing.T) {
	want := plainInitialResponse("user", microSignPassword)
	raw, done := startFakeServer(t, []fakeStep{
		{command: "EHLO client.test", replies: fakeReplies("250-fake.test\r\n", "250 AUTH PLAIN\r\n")},
		{command: "AUTH PLAIN " + want, replies: fakeReplies("235 accepted\r\n")},
		{command: "EHLO client.test", replies: fakeReplies("250 fake.test\r\n")},
	}, nil)
	defer done()
	c, err := NewClient(context.Background(), raw, &ClientOptions{Identity: "client.test"})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Auth(context.Background(), &AuthOptions{
		Username:          "user",
		Password:          microSignPassword,
		AllowInsecureAuth: true,
	}); err != nil {
		t.Fatalf("Auth without SASLPrep: %v", err)
	}
}

// TestSASLPrepFixtureActuallyDiscriminates guards the fixture itself. If an
// editor silently normalised this source file, both constants would collapse
// to the same bytes and the two tests above would still pass while asserting
// nothing — the exact trap docs/INTEROP.md warns about.
func TestSASLPrepFixtureActuallyDiscriminates(t *testing.T) {
	if microSignPassword == greekMuPassword {
		t.Fatal("the SASLprep fixture collapsed: both constants hold the same bytes, so the preparation tests assert nothing")
	}
	if got := []byte(microSignPassword); got[len(got)-2] != 0xc2 || got[len(got)-1] != 0xb5 {
		t.Fatalf("micro-sign fixture = % x, want it to end c2 b5", got)
	}
	if got := []byte(greekMuPassword); got[len(got)-2] != 0xce || got[len(got)-1] != 0xbc {
		t.Fatalf("greek-mu fixture = % x, want it to end ce bc", got)
	}
}
