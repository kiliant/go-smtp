package smtpdeliver

import (
	"context"
	"errors"
)

// errNotImplemented marks the T25 skeleton. T29 replaces Deliver's body and
// T27 RefreshPolicy's; the integration branch is not merged or tagged while
// either remains (docs/tasks/T25-delivery-skeleton.md, "Release hazard").
var errNotImplemented = errors.New("smtpdeliver: not implemented yet")

// Deliver makes one bounded delivery attempt of request to every destination
// (RFC 5321 §5): it resolves MX hosts, applies MTA-STS (RFC 8461) and DANE
// (RFC 7672) where configured, and sends one copy per destination for all its
// recipients. A nil opts means defaults.
//
// A remote refusal is not a call error: it returns a Result and a nil error.
// An invalid request returns an empty Result and an error before any DNS or
// wire I/O. Context cancellation or a MessageSource failure returns the
// partial Result together with that error, with untouched recipients marked
// DispositionNotAttempted.
func (d *Deliverer) Deliver(ctx context.Context, request *Request, opts *DeliverOptions) (Result, error) {
	_ = opts
	if _, err := d.validateRequest(request); err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	return Result{}, errNotImplemented
}
