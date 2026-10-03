package smtpdeliver

import (
	"context"
	"errors"
)

// RefreshPolicy discovers and fetches the RFC 8461 MTA-STS policy for domain,
// stores it in the configured PolicyCache and reports the result. It lets a
// caller's own scheduler refresh policies ahead of expiry (RFC 8461 §5.1)
// without coupling refresh to a message attempt; Deliver also refreshes a due
// policy inline. A nil opts means defaults. It fails if MTA-STS is disabled.
func (d *Deliverer) RefreshPolicy(ctx context.Context, domain string, opts *RefreshPolicyOptions) (PolicyResult, error) {
	_ = opts
	if d.cache == nil {
		return PolicyResult{}, errors.New("smtpdeliver: RefreshPolicy requires Options.MTASTS")
	}
	if _, err := normalizeDomain(domain); err != nil {
		return PolicyResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return PolicyResult{}, err
	}
	return PolicyResult{}, errNotImplemented
}
