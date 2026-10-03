package smtpdeliver

import (
	"context"
	"errors"
)

// RefreshPolicy discovers and fetches the RFC 8461 MTA-STS policy for domain,
// stores it in the configured PolicyCache and reports the result. It lets a
// caller's own scheduler refresh policies ahead of expiry (RFC 8461 §3.3)
// without coupling refresh to a message attempt; Deliver also refreshes a due
// policy inline. A nil opts means defaults.
//
// A refresh that could not obtain a policy is not a call error: the returned
// PolicyResult carries the reason in Cause, and a valid cached policy is
// reported without its expiry being extended. The error is non-nil only when
// MTA-STS is disabled, domain is invalid, or ctx ended.
func (d *Deliverer) RefreshPolicy(ctx context.Context, domain string, opts *RefreshPolicyOptions) (PolicyResult, error) {
	_ = opts
	if d.mtasts == nil {
		return PolicyResult{}, errors.New("smtpdeliver: RefreshPolicy requires Options.MTASTS")
	}
	normalized, err := normalizeDomain(domain)
	if err != nil {
		return PolicyResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return PolicyResult{}, err
	}
	decision, err := d.mtasts.evaluate(ctx, normalized, true)
	if err != nil {
		return PolicyResult{}, err
	}
	return decision.result, nil
}
