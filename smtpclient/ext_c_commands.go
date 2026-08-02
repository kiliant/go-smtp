package smtpclient

import (
	"context"
	"errors"
	"strings"
	"time"

	smtp "github.com/kiliant/go-smtp"
)

// ETRNOptions configures ETRN. A nil *ETRNOptions is valid.
//
// Callers constructing an ETRNOptions literal must use keyed fields.
type ETRNOptions struct{ _ struct{} }

// ATRNOptions configures ATRN. A nil *ATRNOptions is valid.
//
// Callers constructing an ATRNOptions literal must use keyed fields.
type ATRNOptions struct{ _ struct{} }

// ErrATRNRoleReversal reports that an ATRN request was accepted but the
// connection was closed. RFC 2645 reverses SMTP roles after a successful ATRN:
// the caller would have to become an SMTP server and send a greeting. Client
// intentionally has no receiving/server path, so it closes the connection
// rather than leave a session whose next bytes it cannot safely interpret.
var ErrATRNRoleReversal = errors.New("smtpclient: ATRN accepted; SMTP role reversal is unsupported and the connection was closed")

// ETRN asks the server to start processing queued mail for node (RFC 1985).
// The request does not itself deliver mail: a successful server normally opens
// a separate SMTP connection to the requested node. The returned text is the
// server's queue-status response. ETRN is not valid during a mail transaction.
func (c *Client) ETRN(ctx context.Context, node string, opts *ETRNOptions) (string, error) {
	_ = opts
	if c == nil || c.conn == nil {
		return "", errNilClient
	}
	if !validETRNNode(node) {
		return "", errors.New("smtpclient: ETRN requires one non-empty node without SMTP command framing or whitespace")
	}
	if !c.advertises(string(smtp.ExtETRN)) {
		return "", missingExtension(smtp.ExtETRN)
	}
	reply, err := c.command(ctx, "ETRN", []string{node}, c.conn.mailTimeout, stateGreeted, stateTLS, stateAuthenticated)
	if err != nil {
		return "", err
	}
	// RFC 1985 defines all four 25x codes as successful ETRN outcomes.
	if err := unexpectedReply("ETRN", reply, c.conn.enhancedStatusCodes(), 250, 251, 252, 253); err != nil {
		return "", err
	}
	return reply.Text, nil
}

// ATRN requests on-demand relay for domains (RFC 2645). An empty domains slice
// requests all domains available to the authenticated customer. On a 250
// response RFC 2645 requires the SMTP roles to reverse; Client has no receiving
// path, therefore it closes the connection and returns ErrATRNRoleReversal.
func (c *Client) ATRN(ctx context.Context, domains []string, opts *ATRNOptions) error {
	_ = opts
	if c == nil || c.conn == nil {
		return errNilClient
	}
	for _, domain := range domains {
		if !validATRNDomain(domain) {
			return errors.New("smtpclient: ATRN domains must be RFC 2645 domain names")
		}
	}
	if !c.advertises(string(smtp.ExtATRN)) {
		return missingExtension(smtp.ExtATRN)
	}
	args := []string(nil)
	if len(domains) != 0 {
		args = []string{strings.Join(domains, ",")}
	}
	reply, err := c.command(ctx, "ATRN", args, c.atrnTimeout, stateAuthenticated)
	if err != nil {
		return err
	}
	if err := unexpectedReply("ATRN", reply, c.conn.enhancedStatusCodes(), 250); err != nil {
		return err
	}
	c.conn.poison()
	return ErrATRNRoleReversal
}

// RFC 2645 requires an ATRN timeout of at least ten minutes, allowing the
// provider time to inspect its queue before accepting or declining reversal.
func (c *Client) atrnTimeout() time.Duration {
	if timeout := c.conn.mailTimeout(); timeout > 10*time.Minute {
		return timeout
	}
	return 10 * time.Minute
}

func validETRNNode(node string) bool {
	return node != "" && !strings.ContainsAny(node, " \t\r\n\x00")
}

func validATRNDomain(domain string) bool {
	if domain == "" || len(domain) > 255 || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return false
	}
	for _, label := range strings.Split(domain, ".") {
		if label == "" || len(label) > 63 || !isASCIIAlphaNum(label[0]) || !isASCIIAlphaNum(label[len(label)-1]) {
			return false
		}
		for i := 1; i+1 < len(label); i++ {
			if !isASCIIAlphaNum(label[i]) && label[i] != '-' {
				return false
			}
		}
	}
	return true
}

func isASCIIAlphaNum(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}
