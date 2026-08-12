package smtpsasl

import (
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- SCRAM-SHA-1 is required for interoperability.
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"hash"
	"strings"
)

const maxResponderPayload = 16 << 10

var (
	ErrResponderState   = errors.New("smtpsasl: responder called in the wrong state")
	ErrResponderPayload = errors.New("smtpsasl: malformed client response")
	ErrResponderAborted = errors.New("smtpsasl: client aborted the exchange")
)

// VerificationKind identifies which backend operation a responder reached.
// It is open data within the internal package rather than a method set a
// backend must implement.
type VerificationKind string

const (
	VerifyCredentials       VerificationKind = "credentials"
	VerifyChallengeResponse VerificationKind = "challenge-response"
	VerifySCRAMKeys         VerificationKind = "scram-keys"
)

// Credentials are the mechanism values extracted before a backend decision.
// AuthenticationID and AuthorizationID remain distinct by construction.
type Credentials struct {
	AuthenticationID string
	AuthorizationID  string
	Password         string
	Token            string
}

// VerificationRequest marks the exact point at which the framework calls its
// backend. Challenge and Response are populated for CRAM-MD5; Credentials is
// populated for every other mechanism, including a SCRAM key lookup.
type VerificationRequest struct {
	Kind        VerificationKind
	Mechanism   string
	Credentials Credentials
	Challenge   []byte
	Response    []byte
}

// SCRAMKeys are stored verifier material. Supplying keys is not an
// authentication success: the responder still verifies the client proof.
type SCRAMKeys struct {
	Salt       []byte
	Iterations int
	StoredKey  []byte
	ServerKey  []byte
}

// Verification supplies a backend decision to Continue. FailureChallenge is
// the already-encoded OAuth diagnostic payload to send before an unsuccessful
// OAUTHBEARER/XOAUTH2 outcome. SCRAM must supply Keys when Accepted is true.
type Verification struct {
	Accepted         bool
	FailureChallenge []byte
	Keys             *SCRAMKeys
}

// ResponderStep is one action from the SASL state machine. Challenge is non-nil
// when the framework must send a 334 challenge, including a meaningful empty
// challenge. Verification is non-nil exactly at a backend verification point.
// Done means the exchange has a final Accepted outcome and no further SASL
// round trip is owed.
type ResponderStep struct {
	Challenge    []byte
	Verification *VerificationRequest
	Done         bool
	Accepted     bool
}

// ResponderConfig contains transport facts and challenge inputs. Production
// callers normally leave CRAMChallenge empty and receive a random challenge.
type ResponderConfig struct {
	Hostname       string
	CRAMChallenge  []byte
	ChannelBinding []byte

	// testNonce makes SCRAM server messages deterministic in package tests.
	testNonce string
}

type responderState uint8

const (
	responderNew responderState = iota
	responderPlain
	responderLoginUser
	responderLoginPassword
	responderCRAM
	responderExternal
	responderOAuth
	responderOAuthDummy
	responderSCRAMFirst
	responderSCRAMProof
	responderSCRAMEmpty
	responderVerify
	responderDone
)

// Responder is one server-side SASL conversation. Start consumes an optional
// initial response (nil means absent; an empty non-nil slice is meaningful),
// Next consumes subsequent client responses, and Continue supplies a backend
// decision at a step carrying Verification.
type Responder struct {
	name  string
	cfg   ResponderConfig
	state responderState

	request   *VerificationRequest
	challenge []byte
	username  string

	newHash       func() hash.Hash
	plus          bool
	gs2Header     string
	clientFirst   string
	serverFirst   string
	combinedNonce string
	keys          *SCRAMKeys
}

// NewResponder constructs the server half of a supported mechanism.
func NewResponder(name string, cfg ResponderConfig) (*Responder, error) {
	name = strings.ToUpper(name)
	r := &Responder{name: name, cfg: cfg, state: responderNew}
	switch name {
	case "PLAIN", "LOGIN", "CRAM-MD5", "EXTERNAL", "OAUTHBEARER", "XOAUTH2":
	case "SCRAM-SHA-1", "SCRAM-SHA-1-PLUS":
		r.newHash = sha1.New
		r.plus = strings.HasSuffix(name, "-PLUS")
	case "SCRAM-SHA-256", "SCRAM-SHA-256-PLUS":
		r.newHash = sha256.New
		r.plus = strings.HasSuffix(name, "-PLUS")
	default:
		return nil, fmt.Errorf("smtpsasl: unsupported responder mechanism %q", name)
	}
	if r.plus && len(cfg.ChannelBinding) == 0 {
		return nil, errors.New("smtpsasl: channel binding is required for SCRAM-PLUS responder")
	}
	return r, nil
}

func (r *Responder) Name() string { return r.name }

// Start begins the server-side exchange.
func (r *Responder) Start(initial []byte) (ResponderStep, error) {
	if r == nil || r.state != responderNew {
		return ResponderStep{}, ErrResponderState
	}
	if len(initial) > maxResponderPayload {
		return ResponderStep{}, ErrResponderPayload
	}
	if string(initial) == "*" {
		r.state = responderDone
		return ResponderStep{}, ErrResponderAborted
	}
	switch r.name {
	case "PLAIN":
		if initial == nil {
			r.state = responderPlain
			return challengeStep(nil), nil
		}
		return r.plain(initial)
	case "LOGIN":
		if initial == nil {
			r.state = responderLoginUser
			return challengeStep([]byte("Username:")), nil
		}
		r.username = string(initial)
		if r.username == "" {
			return ResponderStep{}, ErrResponderPayload
		}
		r.state = responderLoginPassword
		return challengeStep([]byte("Password:")), nil
	case "CRAM-MD5":
		if initial != nil {
			return ResponderStep{}, ErrResponderPayload
		}
		challenge, err := r.cramChallenge()
		if err != nil {
			return ResponderStep{}, err
		}
		r.challenge = challenge
		r.state = responderCRAM
		return challengeStep(challenge), nil
	case "EXTERNAL":
		if initial == nil {
			r.state = responderExternal
			return challengeStep(nil), nil
		}
		return r.external(initial)
	case "OAUTHBEARER", "XOAUTH2":
		if initial == nil {
			r.state = responderOAuth
			return challengeStep(nil), nil
		}
		return r.oauth(initial)
	default:
		if initial == nil {
			r.state = responderSCRAMFirst
			return challengeStep(nil), nil
		}
		return r.scramFirst(initial)
	}
}

// Next consumes a decoded client response.
func (r *Responder) Next(response []byte) (ResponderStep, error) {
	if r == nil {
		return ResponderStep{}, ErrResponderState
	}
	if string(response) == "*" {
		r.state = responderDone
		return ResponderStep{}, ErrResponderAborted
	}
	if len(response) > maxResponderPayload {
		return ResponderStep{}, ErrResponderPayload
	}
	switch r.state {
	case responderPlain:
		return r.plain(response)
	case responderLoginUser:
		if len(response) == 0 {
			return ResponderStep{}, ErrResponderPayload
		}
		r.username = string(response)
		r.state = responderLoginPassword
		return challengeStep([]byte("Password:")), nil
	case responderLoginPassword:
		return r.verify(VerificationRequest{Kind: VerifyCredentials, Mechanism: r.name, Credentials: Credentials{AuthenticationID: r.username, Password: string(response)}}), nil
	case responderCRAM:
		if len(response) == 0 {
			return ResponderStep{}, ErrResponderPayload
		}
		return r.verify(VerificationRequest{Kind: VerifyChallengeResponse, Mechanism: r.name, Challenge: append([]byte(nil), r.challenge...), Response: append([]byte(nil), response...)}), nil
	case responderExternal:
		return r.external(response)
	case responderOAuth:
		return r.oauth(response)
	case responderOAuthDummy:
		if string(response) != "\x01" {
			return ResponderStep{}, ErrResponderPayload
		}
		return r.finish(false), nil
	case responderSCRAMFirst:
		return r.scramFirst(response)
	case responderSCRAMProof:
		return r.scramProof(response)
	case responderSCRAMEmpty:
		if len(response) != 0 {
			return ResponderStep{}, ErrResponderPayload
		}
		return r.finish(true), nil
	default:
		return ResponderStep{}, ErrResponderState
	}
}

// Continue supplies the backend answer for the preceding verification step.
func (r *Responder) Continue(result Verification) (ResponderStep, error) {
	if r == nil || r.state != responderVerify || r.request == nil {
		return ResponderStep{}, ErrResponderState
	}
	request := r.request
	r.request = nil
	if request.Kind == VerifySCRAMKeys {
		if !result.Accepted {
			return r.finish(false), nil
		}
		if result.Keys == nil {
			return ResponderStep{}, errors.New("smtpsasl: accepted SCRAM lookup supplied no keys")
		}
		return r.scramServerFirst(result.Keys)
	}
	if !result.Accepted && (r.name == "OAUTHBEARER" || r.name == "XOAUTH2") && result.FailureChallenge != nil {
		if len(result.FailureChallenge) > maxResponderPayload {
			r.state = responderDone
			return ResponderStep{}, ErrResponderPayload
		}
		r.state = responderOAuthDummy
		return challengeStep(result.FailureChallenge), nil
	}
	return r.finish(result.Accepted), nil
}

func (r *Responder) plain(payload []byte) (ResponderStep, error) {
	parts := strings.Split(string(payload), "\x00")
	if len(parts) != 3 || parts[1] == "" {
		return ResponderStep{}, ErrResponderPayload
	}
	request := VerificationRequest{Kind: VerifyCredentials, Mechanism: r.name, Credentials: Credentials{AuthorizationID: parts[0], AuthenticationID: parts[1], Password: parts[2]}}
	return r.verify(request), nil
}

func (r *Responder) external(payload []byte) (ResponderStep, error) {
	request := VerificationRequest{Kind: VerifyCredentials, Mechanism: r.name, Credentials: Credentials{AuthorizationID: string(payload)}}
	return r.verify(request), nil
}

func (r *Responder) oauth(payload []byte) (ResponderStep, error) {
	var credentials Credentials
	var err error
	if r.name == "OAUTHBEARER" {
		credentials, err = parseOAuthBearer(payload)
	} else {
		credentials, err = parseXOAuth2(payload)
	}
	if err != nil {
		return ResponderStep{}, err
	}
	return r.verify(VerificationRequest{Kind: VerifyCredentials, Mechanism: r.name, Credentials: credentials}), nil
}

func parseOAuthBearer(payload []byte) (Credentials, error) {
	parts := strings.Split(string(payload), "\x01")
	if len(parts) < 3 || parts[len(parts)-1] != "" || parts[len(parts)-2] != "" {
		return Credentials{}, ErrResponderPayload
	}
	authzid, err := parseOAuthGS2(parts[0])
	if err != nil {
		return Credentials{}, err
	}
	var token string
	for _, part := range parts[1 : len(parts)-2] {
		if strings.HasPrefix(part, "auth=Bearer ") {
			token = strings.TrimPrefix(part, "auth=Bearer ")
		}
	}
	if token == "" {
		return Credentials{}, ErrResponderPayload
	}
	return Credentials{AuthorizationID: authzid, Token: token}, nil
}

func parseOAuthGS2(header string) (string, error) {
	if !strings.HasPrefix(header, "n,") || !strings.HasSuffix(header, ",") {
		return "", ErrResponderPayload
	}
	middle := strings.TrimSuffix(strings.TrimPrefix(header, "n,"), ",")
	if middle == "" {
		return "", nil
	}
	if !strings.HasPrefix(middle, "a=") {
		return "", ErrResponderPayload
	}
	return decodeSASLName(strings.TrimPrefix(middle, "a="))
}

func parseXOAuth2(payload []byte) (Credentials, error) {
	parts := strings.Split(string(payload), "\x01")
	if len(parts) < 3 || parts[len(parts)-1] != "" || parts[len(parts)-2] != "" {
		return Credentials{}, ErrResponderPayload
	}
	var username, token string
	for _, part := range parts[:len(parts)-2] {
		switch {
		case strings.HasPrefix(part, "user="):
			username = strings.TrimPrefix(part, "user=")
		case strings.HasPrefix(part, "auth=Bearer "):
			token = strings.TrimPrefix(part, "auth=Bearer ")
		}
	}
	if username == "" || token == "" {
		return Credentials{}, ErrResponderPayload
	}
	return Credentials{AuthenticationID: username, Token: token}, nil
}

func (r *Responder) verify(request VerificationRequest) ResponderStep {
	requestCopy := request
	r.request = &requestCopy
	r.state = responderVerify
	return ResponderStep{Verification: &requestCopy}
}

func (r *Responder) finish(accepted bool) ResponderStep {
	r.state = responderDone
	return ResponderStep{Done: true, Accepted: accepted}
}

func challengeStep(challenge []byte) ResponderStep {
	if challenge == nil {
		challenge = []byte{}
	}
	return ResponderStep{Challenge: append([]byte{}, challenge...)}
}

func (r *Responder) cramChallenge() ([]byte, error) {
	if r.cfg.CRAMChallenge != nil {
		return append([]byte(nil), r.cfg.CRAMChallenge...), nil
	}
	nonce := make([]byte, 18)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("smtpsasl: CRAM-MD5 challenge: %w", err)
	}
	hostname := r.cfg.Hostname
	if hostname == "" {
		hostname = "localhost"
	}
	return []byte("<" + base64.RawURLEncoding.EncodeToString(nonce) + "@" + hostname + ">"), nil
}

func (r *Responder) scramFirst(payload []byte) (ResponderStep, error) {
	gs2, bare, authzid, err := parseSCRAMClientFirst(string(payload), r.plus)
	if err != nil {
		return ResponderStep{}, err
	}
	fields, err := parseSCRAMFields(bare)
	if err != nil || fields["n"] == "" || fields["r"] == "" || fields["m"] != "" {
		return ResponderStep{}, ErrResponderPayload
	}
	username, err := decodeSASLName(fields["n"])
	if err != nil {
		return ResponderStep{}, err
	}
	r.gs2Header = gs2
	r.clientFirst = bare
	r.combinedNonce = fields["r"]
	request := VerificationRequest{Kind: VerifySCRAMKeys, Mechanism: r.name, Credentials: Credentials{AuthenticationID: username, AuthorizationID: authzid}}
	return r.verify(request), nil
}

func parseSCRAMClientFirst(message string, plus bool) (header, bare, authzid string, err error) {
	first := strings.IndexByte(message, ',')
	if first < 0 {
		return "", "", "", ErrResponderPayload
	}
	secondRel := strings.IndexByte(message[first+1:], ',')
	if secondRel < 0 {
		return "", "", "", ErrResponderPayload
	}
	second := first + 1 + secondRel
	header = message[:second+1]
	bare = message[second+1:]
	flag, authz := message[:first], message[first+1:second]
	if plus {
		if flag != "p=tls-exporter" {
			return "", "", "", ErrResponderPayload
		}
	} else if flag != "n" && flag != "y" {
		return "", "", "", ErrResponderPayload
	}
	if authz != "" {
		if !strings.HasPrefix(authz, "a=") {
			return "", "", "", ErrResponderPayload
		}
		authzid, err = decodeSASLName(strings.TrimPrefix(authz, "a="))
		if err != nil {
			return "", "", "", err
		}
	}
	if bare == "" {
		return "", "", "", ErrResponderPayload
	}
	return header, bare, authzid, nil
}

func (r *Responder) scramServerFirst(keys *SCRAMKeys) (ResponderStep, error) {
	hashSize := r.newHash().Size()
	if keys.Iterations < 1 || keys.Iterations > maxSCRAMIterations || len(keys.Salt) == 0 || len(keys.StoredKey) != hashSize || len(keys.ServerKey) != hashSize {
		return ResponderStep{}, errors.New("smtpsasl: invalid SCRAM verifier material")
	}
	nonce := r.cfg.testNonce
	if nonce == "" {
		random := make([]byte, 18)
		if _, err := rand.Read(random); err != nil {
			return ResponderStep{}, fmt.Errorf("smtpsasl: SCRAM nonce: %w", err)
		}
		nonce = base64.RawStdEncoding.EncodeToString(random)
	}
	r.combinedNonce += nonce
	r.serverFirst = "r=" + r.combinedNonce + ",s=" + base64.StdEncoding.EncodeToString(keys.Salt) + ",i=" + fmt.Sprint(keys.Iterations)
	r.keys = &SCRAMKeys{Salt: append([]byte(nil), keys.Salt...), Iterations: keys.Iterations, StoredKey: append([]byte(nil), keys.StoredKey...), ServerKey: append([]byte(nil), keys.ServerKey...)}
	r.state = responderSCRAMProof
	return challengeStep([]byte(r.serverFirst)), nil
}

func (r *Responder) scramProof(payload []byte) (ResponderStep, error) {
	message := string(payload)
	proofAt := strings.LastIndex(message, ",p=")
	if proofAt < 0 || strings.Contains(message[proofAt+3:], ",") {
		return ResponderStep{}, ErrResponderPayload
	}
	withoutProof := message[:proofAt]
	fields, err := parseSCRAMFields(withoutProof)
	if err != nil || fields["c"] == "" || fields["r"] != r.combinedNonce {
		return ResponderStep{}, ErrResponderPayload
	}
	channel, err := base64.StdEncoding.DecodeString(fields["c"])
	if err != nil {
		return ResponderStep{}, ErrResponderPayload
	}
	wantChannel := []byte(r.gs2Header)
	if r.plus {
		wantChannel = append(wantChannel, r.cfg.ChannelBinding...)
	}
	if subtle.ConstantTimeCompare(channel, wantChannel) != 1 {
		return r.finish(false), nil
	}
	proof, err := base64.StdEncoding.DecodeString(message[proofAt+3:])
	if err != nil || len(proof) != r.newHash().Size() {
		return ResponderStep{}, ErrResponderPayload
	}
	authMessage := r.clientFirst + "," + r.serverFirst + "," + withoutProof
	clientSignature := hmacSum(r.newHash, r.keys.StoredKey, []byte(authMessage))
	clientKey := xor(proof, clientSignature)
	stored := hashSum(r.newHash, clientKey)
	if subtle.ConstantTimeCompare(stored, r.keys.StoredKey) != 1 {
		return r.finish(false), nil
	}
	serverSignature := hmacSum(r.newHash, r.keys.ServerKey, []byte(authMessage))
	r.state = responderSCRAMEmpty
	return challengeStep([]byte("v=" + base64.StdEncoding.EncodeToString(serverSignature))), nil
}

func parseSCRAMFields(message string) (map[string]string, error) {
	fields := make(map[string]string)
	for _, field := range strings.Split(message, ",") {
		if len(field) < 3 || field[1] != '=' {
			return nil, ErrResponderPayload
		}
		name := field[:1]
		if _, exists := fields[name]; exists {
			return nil, ErrResponderPayload
		}
		fields[name] = field[2:]
	}
	return fields, nil
}

func decodeSASLName(value string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '=' {
			b.WriteByte(value[i])
			continue
		}
		if i+2 >= len(value) {
			return "", ErrResponderPayload
		}
		switch value[i+1 : i+3] {
		case "2C":
			b.WriteByte(',')
		case "3D":
			b.WriteByte('=')
		default:
			return "", ErrResponderPayload
		}
		i += 2
	}
	return b.String(), nil
}
