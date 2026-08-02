package smtpclient

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	smtp "github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/internal/saslprep"
	"github.com/kiliant/go-smtp/internal/smtpsasl"
	"github.com/kiliant/go-smtp/internal/smtpwire"
)

const maxAuthChallenge = 16 << 10

// AuthOptions configures RFC 4954 authentication. A nil *AuthOptions is valid,
// but cannot authenticate because it has no credentials.
//
// Callers constructing an AuthOptions literal must use keyed fields.
type AuthOptions struct {
	// Username and Password are used by password mechanisms.
	Username string
	Password string
	// AuthorizationID is the SASL authorization identity. Empty normally asks
	// the server to use Username. For EXTERNAL it is the asserted identity.
	AuthorizationID string
	// Token is the OAuth bearer token for OAUTHBEARER or XOAUTH2.
	Token string
	// Mechanisms pins or orders mechanisms by their SASL names. If empty, Auth
	// selects the strongest supported mechanism advertised by the server.
	Mechanisms []string
	// AllowInsecureAuth permits credentials over an unencrypted SMTP session.
	// It is disabled by default because it exposes credentials to the network.
	AllowInsecureAuth bool
	// SASLPrep applies RFC 4013 preparation to Username, Password and
	// AuthorizationID. It is opt-in: many deployed servers compare stored raw
	// octets, so enabling it can make an otherwise valid login fail.
	SASLPrep bool

	_ struct{}
}

// Auth authenticates the session using RFC 4954. It may be called only once,
// before a mail transaction. Authentication exchange payloads are intentionally
// never included in returned errors.
func (c *Client) Auth(ctx context.Context, opts *AuthOptions) error {
	if c == nil || c.conn == nil {
		return transportError("AUTH", errNilClient)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var cfg AuthOptions
	if opts != nil {
		cfg = *opts
	}
	if cfg.SASLPrep {
		var err error
		if cfg.Username, err = saslprep.Prepare(cfg.Username); err != nil {
			return fmt.Errorf("smtpclient: prepare AUTH username: %w", err)
		}
		if cfg.Password, err = saslprep.Prepare(cfg.Password); err != nil {
			return fmt.Errorf("smtpclient: prepare AUTH password: %w", err)
		}
		if cfg.AuthorizationID, err = saslprep.Prepare(cfg.AuthorizationID); err != nil {
			return fmt.Errorf("smtpclient: prepare AUTH authorization identity: %w", err)
		}
	}

	c.conn.opMu.Lock()
	defer c.conn.opMu.Unlock()
	c.conn.mu.Lock()
	state := c.conn.state
	params, advertised := c.conn.ext[string(smtp.ExtAuth)]
	raw := c.conn.raw
	c.conn.mu.Unlock()
	if err := invalidState("AUTH", state, stateGreeted, stateTLS); err != nil {
		return err
	}
	if !advertised {
		return errors.New("smtpclient: server did not advertise AUTH")
	}
	available := authMechanisms(params)
	if len(cfg.Mechanisms) == 0 && !isTLS(raw) {
		for mechanism := range available {
			if strings.HasSuffix(mechanism, "-PLUS") {
				delete(available, mechanism)
			}
		}
	}
	name, err := selectMechanism(cfg.Mechanisms, available)
	if err != nil {
		return err
	}
	if needsSecret(name) && !isTLS(raw) && !cfg.AllowInsecureAuth {
		return errors.New("smtpclient: refusing AUTH credentials over an unencrypted connection; set AllowInsecureAuth to override")
	}
	channelBinding, err := tlsExporter(raw, name)
	if err != nil {
		return err
	}
	mech, err := smtpsasl.New(name, smtpsasl.Config{Username: cfg.Username, Password: cfg.Password, AuthorizationID: cfg.AuthorizationID, Token: cfg.Token, ChannelBinding: channelBinding})
	if err != nil {
		return err
	}
	initial, err := mech.Start()
	if err != nil {
		return fmt.Errorf("smtpclient: AUTH %s: %w", name, err)
	}
	args := []string{name}
	if initial != nil {
		args = append(args, encodeSASL(initial))
	}
	replies, err := c.conn.pipeline.executeLocked(ctx, []queuedCommand{{verb: "AUTH", args: args, syncPoint: true, timeout: c.conn.mailTimeout()}})
	if err != nil {
		return err
	}
	reply := replies[0]
	for reply.Code == 334 {
		challenge, err := decodeChallenge(reply.Text)
		if err != nil {
			c.conn.poison()
			return transportError("AUTH", err)
		}
		response, done, err := mech.Next(challenge)
		if err != nil {
			c.conn.poison()
			return fmt.Errorf("smtpclient: AUTH %s: %w", name, err)
		}
		if err := c.authResponse(ctx, encodeSASL(response)); err != nil {
			return err
		}
		reply, err = c.conn.pipeline.read(ctx, "AUTH", c.conn.mailTimeout())
		if err != nil {
			return err
		}
		if done && reply.Code == 334 {
			c.conn.poison()
			return transportError("AUTH", errors.New("server requested an unexpected additional SASL response"))
		}
	}
	if err := unexpectedReply("AUTH", reply, c.conn.enhancedStatusCodes(), 235); err != nil {
		return err
	}
	c.conn.mu.Lock()
	if c.conn.state != stateClosed {
		c.conn.state = stateAuthenticated
	}
	c.conn.mu.Unlock()
	// RFC 4954 permits the extension list to change after authentication.
	return c.ehloLocked(ctx)
}

func (c *Client) authResponse(ctx context.Context, response string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	stop := c.conn.cancelWatcher(ctx)
	c.conn.mu.Lock()
	raw := c.conn.raw
	c.conn.mu.Unlock()
	err := smtpwire.EncodeCommand(raw, response)
	stop()
	if ctx.Err() != nil {
		c.conn.poison()
		return ctx.Err()
	}
	if err != nil {
		c.conn.poison()
		return transportError("AUTH", err)
	}
	return nil
}

func authMechanisms(params string) map[string]bool {
	out := make(map[string]bool)
	for _, name := range strings.Fields(strings.TrimPrefix(strings.TrimSpace(params), "=")) {
		out[strings.ToUpper(name)] = true
	}
	return out
}
func selectMechanism(preferred []string, available map[string]bool) (string, error) {
	if len(preferred) == 0 {
		preferred = []string{"SCRAM-SHA-256-PLUS", "SCRAM-SHA-256", "SCRAM-SHA-1-PLUS", "SCRAM-SHA-1", "CRAM-MD5", "OAUTHBEARER", "XOAUTH2", "EXTERNAL", "PLAIN", "LOGIN"}
	}
	for _, name := range preferred {
		if available[strings.ToUpper(name)] {
			// Selection only establishes whether the mechanism is implemented.
			// Give the constructor a non-empty placeholder binding so that a
			// supported SCRAM-PLUS mechanism is not mistakenly filtered out
			// before tlsExporter obtains its real binding below.
			if _, err := smtpsasl.New(name, smtpsasl.Config{ChannelBinding: []byte{1}}); err == nil {
				return strings.ToUpper(name), nil
			}
		}
	}
	return "", errors.New("smtpclient: no requested AUTH mechanism is advertised")
}
func encodeSASL(v []byte) string {
	if len(v) == 0 {
		return "="
	}
	return base64.StdEncoding.EncodeToString(v)
}
func decodeChallenge(v string) ([]byte, error) {
	v = strings.TrimSpace(v)
	if v == "" || v == "=" {
		return nil, nil
	}
	if len(v) > maxAuthChallenge*2 {
		return nil, errors.New("AUTH challenge exceeds limit")
	}
	out, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(out) > maxAuthChallenge {
		return nil, errors.New("invalid AUTH challenge")
	}
	return out, nil
}
func needsSecret(name string) bool { return name != "EXTERNAL" }
func isTLS(raw any) bool           { _, ok := raw.(*tls.Conn); return ok }
func tlsExporter(raw any, name string) ([]byte, error) {
	if !strings.HasSuffix(name, "-PLUS") {
		return nil, nil
	}
	conn, ok := raw.(*tls.Conn)
	if !ok {
		return nil, errors.New("smtpclient: SCRAM-PLUS requires TLS")
	}
	state := conn.ConnectionState()
	return state.ExportKeyingMaterial("EXPORTER-Channel-Binding", nil, 32)
}
