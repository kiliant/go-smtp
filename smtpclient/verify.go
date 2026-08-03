package smtpclient

import (
	"context"
	"errors"
)

// VerifyOptions configures RFC 5321 VRFY. A nil *VerifyOptions means defaults.
//
// Callers constructing a VerifyOptions literal must use keyed fields.
type VerifyOptions struct{ _ struct{} }

// ExpandOptions configures RFC 5321 EXPN. A nil *ExpandOptions means defaults.
//
// Callers constructing an ExpandOptions literal must use keyed fields.
type ExpandOptions struct{ _ struct{} }

// HelpOptions configures RFC 5321 HELP. A nil *HelpOptions means defaults.
//
// Callers constructing a HelpOptions literal must use keyed fields.
type HelpOptions struct{ _ struct{} }

// Verify asks the server to verify an address with VRFY (RFC 5321 §4.1.1.6).
// Modern servers commonly disable it or return intentionally uninformative
// answers, so applications must not use it as an authorization check.
func (c *Client) Verify(ctx context.Context, address string, opts *VerifyOptions) (string, error) {
	_ = opts
	if c == nil || c.conn == nil {
		return "", errNilClient
	}
	if address == "" {
		return "", errors.New("smtpclient: VRFY address is required")
	}
	reply, err := c.command(ctx, "VRFY", []string{address}, c.conn.mailTimeout, stateGreeted, stateTLS, stateAuthenticated)
	if err != nil {
		return "", err
	}
	if err := unexpectedReply("VRFY", reply, c.conn.enhancedStatusCodes(), 250, 251, 252); err != nil {
		return "", err
	}
	return reply.Text, nil
}

// Expand asks the server to expand a mailing list with EXPN (RFC 5321
// §4.1.1.7). Modern servers commonly disable this command or deliberately
// suppress membership information, so applications must not depend on it.
func (c *Client) Expand(ctx context.Context, list string, opts *ExpandOptions) (string, error) {
	_ = opts
	if c == nil || c.conn == nil {
		return "", errNilClient
	}
	if list == "" {
		return "", errors.New("smtpclient: EXPN list is required")
	}
	reply, err := c.command(ctx, "EXPN", []string{list}, c.conn.mailTimeout, stateGreeted, stateTLS, stateAuthenticated)
	if err != nil {
		return "", err
	}
	if err := unexpectedReply("EXPN", reply, c.conn.enhancedStatusCodes(), 250); err != nil {
		return "", err
	}
	return reply.Text, nil
}

// Help asks the server for HELP text (RFC 5321 §4.1.1.8). topic may be empty.
func (c *Client) Help(ctx context.Context, topic string, opts *HelpOptions) (string, error) {
	_ = opts
	if c == nil || c.conn == nil {
		return "", errNilClient
	}
	args := []string(nil)
	if topic != "" {
		args = []string{topic}
	}
	reply, err := c.command(ctx, "HELP", args, c.conn.mailTimeout, stateGreeted, stateTLS, stateAuthenticated, stateTransaction)
	if err != nil {
		return "", err
	}
	if err := unexpectedReply("HELP", reply, c.conn.enhancedStatusCodes(), 211, 214); err != nil {
		return "", err
	}
	return reply.Text, nil
}
