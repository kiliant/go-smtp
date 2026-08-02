// Package smtpsasl implements the SASL mechanisms used by smtpclient.
// It deliberately contains no SMTP framing: callers exchange the returned
// byte strings using the protocol that embeds SASL.
package smtpsasl

import (
	"crypto/hmac"
	"crypto/md5" // #nosec G501 -- CRAM-MD5 is a required legacy SASL mechanism.
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- SCRAM-SHA-1 is retained for interoperability.
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"hash"
	"strings"
)

// Config supplies mechanism credentials.  Password and Token are copied only
// as needed by a mechanism; callers should discard their own credential data
// after authentication.
type Config struct {
	Username, Password, AuthorizationID, Token string
	ChannelBinding                             []byte
}

// Mechanism is one client-side SASL conversation. Start returns the initial
// response (including a meaningful zero-length response). Next consumes a
// decoded server challenge. Done becomes true only after the client has sent
// its final response; callers still must read the server's final status.
type Mechanism interface {
	Name() string
	Start() ([]byte, error)
	Next(challenge []byte) (response []byte, done bool, err error)
}

// New constructs a supported mechanism selected by its case-insensitive name.
func New(name string, cfg Config) (Mechanism, error) {
	switch strings.ToUpper(name) {
	case "PLAIN":
		return &plain{cfg}, nil
	case "LOGIN":
		return &login{cfg: cfg}, nil
	case "CRAM-MD5":
		return &cramMD5{cfg: cfg}, nil
	case "EXTERNAL":
		return &external{cfg}, nil
	case "OAUTHBEARER":
		return &oauthBearer{cfg}, nil
	case "XOAUTH2":
		return &xoauth2{cfg}, nil
	case "SCRAM-SHA-1", "SCRAM-SHA-1-PLUS":
		return newSCRAM(strings.ToUpper(name), cfg, sha1.New)
	case "SCRAM-SHA-256", "SCRAM-SHA-256-PLUS":
		return newSCRAM(strings.ToUpper(name), cfg, sha256.New)
	default:
		return nil, fmt.Errorf("smtpsasl: unsupported mechanism %q", name)
	}
}

type plain struct{ cfg Config }

func (p *plain) Name() string { return "PLAIN" }
func (p *plain) Start() ([]byte, error) {
	return []byte(p.cfg.AuthorizationID + "\x00" + p.cfg.Username + "\x00" + p.cfg.Password), nil
}
func (p *plain) Next([]byte) ([]byte, bool, error) {
	return nil, false, errors.New("smtpsasl: PLAIN received an unexpected challenge")
}

// LOGIN was never standardised, but remains widely deployed by submission
// servers. It asks for username then password in separate base64 challenges.
type login struct {
	cfg  Config
	step int
}

func (p *login) Name() string           { return "LOGIN" }
func (p *login) Start() ([]byte, error) { return nil, nil }
func (p *login) Next([]byte) ([]byte, bool, error) {
	p.step++
	if p.step == 1 {
		return []byte(p.cfg.Username), false, nil
	}
	if p.step == 2 {
		return []byte(p.cfg.Password), true, nil
	}
	return nil, false, errors.New("smtpsasl: LOGIN received too many challenges")
}

type cramMD5 struct{ cfg Config }

func (p *cramMD5) Name() string           { return "CRAM-MD5" }
func (p *cramMD5) Start() ([]byte, error) { return nil, nil }
func (p *cramMD5) Next(challenge []byte) ([]byte, bool, error) {
	h := hmac.New(md5.New, []byte(p.cfg.Password))
	_, _ = h.Write(challenge)
	return []byte(p.cfg.Username + " " + fmt.Sprintf("%x", h.Sum(nil))), true, nil
}

type external struct{ cfg Config }

func (p *external) Name() string           { return "EXTERNAL" }
func (p *external) Start() ([]byte, error) { return []byte(p.cfg.AuthorizationID), nil }
func (p *external) Next([]byte) ([]byte, bool, error) {
	return nil, false, errors.New("smtpsasl: EXTERNAL received an unexpected challenge")
}

type oauthBearer struct{ cfg Config }

func (p *oauthBearer) Name() string { return "OAUTHBEARER" }
func (p *oauthBearer) Start() ([]byte, error) {
	a := p.cfg.AuthorizationID
	if a == "" {
		a = p.cfg.Username
	}
	return []byte("n,a=" + saslName(a) + ",\x01auth=Bearer " + p.cfg.Token + "\x01\x01"), nil
}
func (p *oauthBearer) Next([]byte) ([]byte, bool, error) { return []byte("\x01"), true, nil }

type xoauth2 struct{ cfg Config }

func (p *xoauth2) Name() string { return "XOAUTH2" }
func (p *xoauth2) Start() ([]byte, error) {
	return []byte("user=" + p.cfg.Username + "\x01auth=Bearer " + p.cfg.Token + "\x01\x01"), nil
}
func (p *xoauth2) Next([]byte) ([]byte, bool, error) { return []byte("\x01"), true, nil }

type scram struct {
	name        string
	cfg         Config
	newHash     func() hash.Hash
	plus        bool
	nonce       string
	firstBare   string
	serverFirst string
	expected    []byte
	step        int
}

func newSCRAM(name string, cfg Config, newHash func() hash.Hash) (*scram, error) {
	if strings.HasSuffix(name, "-PLUS") && len(cfg.ChannelBinding) == 0 {
		return nil, errors.New("smtpsasl: channel binding is required for SCRAM-PLUS")
	}
	return &scram{name: name, cfg: cfg, newHash: newHash, plus: strings.HasSuffix(name, "-PLUS")}, nil
}
func (s *scram) Name() string { return s.name }
func (s *scram) Start() ([]byte, error) {
	nonce := make([]byte, 18)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("smtpsasl: SCRAM nonce: %w", err)
	}
	s.nonce = base64.RawStdEncoding.EncodeToString(nonce)
	s.firstBare = "n=" + saslName(s.cfg.Username) + ",r=" + s.nonce
	gs2 := "n,,"
	if s.plus {
		gs2 = "p=tls-exporter,,"
	}
	return []byte(gs2 + s.firstBare), nil
}
func (s *scram) Next(challenge []byte) ([]byte, bool, error) {
	if s.step == 0 {
		s.step++
		return s.clientFinal(string(challenge))
	}
	if s.step == 1 {
		s.step++
		fields := parseFields(string(challenge))
		if subtle.ConstantTimeCompare([]byte(fields["v"]), []byte(base64.StdEncoding.EncodeToString(s.expected))) != 1 {
			return nil, false, errors.New("smtpsasl: SCRAM server signature mismatch")
		}
		return nil, true, nil
	}
	return nil, false, errors.New("smtpsasl: SCRAM received too many challenges")
}
func (s *scram) clientFinal(serverFirst string) ([]byte, bool, error) {
	f := parseFields(serverFirst)
	nonce, salt64, iter := f["r"], f["s"], 0
	if nonce == "" || !strings.HasPrefix(nonce, s.nonce) || salt64 == "" {
		return nil, false, errors.New("smtpsasl: malformed SCRAM server-first message")
	}
	if _, err := fmt.Sscanf(f["i"], "%d", &iter); err != nil || iter < 1 {
		return nil, false, errors.New("smtpsasl: invalid SCRAM iteration count")
	}
	salt, err := base64.StdEncoding.DecodeString(salt64)
	if err != nil {
		return nil, false, errors.New("smtpsasl: invalid SCRAM salt")
	}
	salted := hi(s.newHash, []byte(s.cfg.Password), salt, iter)
	clientKey := hmacSum(s.newHash, salted, []byte("Client Key"))
	stored := hashSum(s.newHash, clientKey)
	cbind := "biws"
	if s.plus {
		cbind = base64.StdEncoding.EncodeToString(append([]byte("p=tls-exporter,,"), s.cfg.ChannelBinding...))
	}
	withoutProof := "c=" + cbind + ",r=" + nonce
	auth := s.firstBare + "," + serverFirst + "," + withoutProof
	clientSig := hmacSum(s.newHash, stored, []byte(auth))
	proof := xor(clientKey, clientSig)
	serverKey := hmacSum(s.newHash, salted, []byte("Server Key"))
	s.expected = hmacSum(s.newHash, serverKey, []byte(auth))
	return []byte(withoutProof + ",p=" + base64.StdEncoding.EncodeToString(proof)), false, nil
}
func parseFields(v string) map[string]string {
	out := map[string]string{}
	for _, x := range strings.Split(v, ",") {
		if len(x) >= 3 && x[1] == '=' {
			out[x[:1]] = x[2:]
		}
	}
	return out
}
func hi(newHash func() hash.Hash, password, salt []byte, iterations int) []byte {
	mac := hmac.New(newHash, password)
	_, _ = mac.Write(append(salt, 0, 0, 0, 1))
	out := mac.Sum(nil)
	previous := append([]byte(nil), out...)
	for i := 1; i < iterations; i++ {
		mac = hmac.New(newHash, password)
		_, _ = mac.Write(previous)
		previous = mac.Sum(nil)
		for j := range out {
			out[j] ^= previous[j]
		}
	}
	return out
}
func hmacSum(newHash func() hash.Hash, key, in []byte) []byte {
	h := hmac.New(newHash, key)
	_, _ = h.Write(in)
	return h.Sum(nil)
}
func hashSum(newHash func() hash.Hash, in []byte) []byte {
	h := newHash()
	_, _ = h.Write(in)
	return h.Sum(nil)
}
func xor(a, b []byte) []byte {
	o := make([]byte, len(a))
	for i := range a {
		o[i] = a[i] ^ b[i]
	}
	return o
}
func saslName(v string) string { return strings.NewReplacer("=", "=3D", ",", "=2C").Replace(v) }
