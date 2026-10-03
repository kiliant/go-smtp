package smtpdeliver

import (
	"context"
	"errors"
)

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
	dests, err := d.validateRequest(request)
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	result := Result{Destinations: make([]DestinationResult, len(dests))}
	for i, dest := range dests {
		dr, callErr := d.deliverDestination(ctx, request, dest)
		result.Destinations[i] = dr
		if callErr != nil {
			// The call ends here; later destinations were never reached.
			for j := i + 1; j < len(dests); j++ {
				o := newDestinationOutcomes(dests[j].domain, dests[j].recipients)
				o.finishAll(DispositionNotAttempted, statusNone, callErr)
				result.Destinations[j] = o.result
			}
			return result, callErr
		}
	}
	return result, nil
}

// deliverDestination runs the DELIVERY-DESIGN.md §7 loop for one destination:
// policy, route, then candidates in order until no recipient is pending. A
// non-nil error ends the whole call; the returned result is complete either
// way, with every recipient decided.
func (d *Deliverer) deliverDestination(ctx context.Context, request *Request, dest destination) (DestinationResult, error) {
	o := newDestinationOutcomes(dest.domain, dest.recipients)
	stop := func(err error) (DestinationResult, error) {
		o.finishAll(DispositionNotAttempted, statusNone, err)
		return o.result, err
	}

	var sts *mtastsDecision
	if d.mtasts != nil {
		dec, err := d.mtasts.evaluate(ctx, dest.domain, false)
		if err != nil {
			return stop(err)
		}
		o.result.Policies = append(o.result.Policies, dec.result)
		if dec.deferred {
			// Proceeding could ignore a policy still in force (§5).
			o.finishAll(DispositionTemporary, statusLocalFailure, dec.result.Cause)
			return o.result, nil
		}
		sts = &dec
	}

	plan, failure, err := d.newRoutePlanner().plan(ctx, dest.domain)
	if err != nil {
		return stop(err)
	}
	if failure != nil {
		for _, s := range failure.steps {
			o.result.Attempts = append(o.result.Attempts, resolveAttempt(s, s.skip, nil))
		}
		o.finishAll(failure.disposition, failure.status, failure.cause)
		return o.result, nil
	}

	if d.dane && d.daneMode == DANEMandatory && len(plan.steps) > 0 && plan.steps[0].host.security != DNSSECSecure {
		// A destination-level DANE outcome: every candidate will be skipped
		// because the MX RRset itself is not secure (RFC 7672 §2.2.1).
		o.result.Policies = append(o.result.Policies, PolicyResult{Kind: PolicyDANE, Mode: string(d.daneMode), Source: PolicySourceDNS, Applied: true, Cause: errDANEInsecureMX})
	}
	tally := requireTLSTally{enabled: wantsRequireTLS(request.MailOptions)}
	for _, step := range plan.steps {
		pending := o.pending()
		if len(pending) == 0 {
			break
		}
		if step.skipped() {
			o.result.Attempts = append(o.result.Attempts, resolveAttempt(step, step.skip, nil))
			deferAll(o, pending, step.skip, len(o.result.Attempts)-1)
			tally.otherFailures++
			continue
		}
		dane, err := d.lookupDANE(ctx, dest.domain, step)
		if err != nil {
			return stop(err)
		}
		tlsPlan := d.planTLS(tlsInput{domain: dest.domain, step: step, sts: sts, dane: dane, requireTLS: tally.enabled})
		if tlsPlan.skip != nil {
			o.result.Attempts = append(o.result.Attempts, resolveAttempt(step, tlsPlan.skip, tlsPlan.report.policies()))
			deferAll(o, pending, tlsPlan.skip, len(o.result.Attempts)-1)
			if errors.Is(tlsPlan.skip, errRequireTLSMXNotValid) {
				tally.tlsFailures++
			} else {
				tally.otherFailures++
			}
			continue
		}

		out := d.runAttempt(ctx, attemptInput{domain: dest.domain, request: request, step: step, tls: tlsPlan, recipients: dest.recipients, indices: pending})
		o.result.Attempts = append(o.result.Attempts, out.record)
		attempt := len(o.result.Attempts) - 1
		switch out.requireTLS {
		case requireTLSTLS:
			tally.tlsFailures++
		case requireTLSNotAdvertised:
			tally.notAdvertised++
		case requireTLSOther:
			tally.otherFailures++
		}
		for _, dec := range out.decisions {
			if dec.disposition == DispositionTemporary {
				o.defer_(dec.index, dec.reply, dec.cause, attempt)
				if dec.reply != nil {
					tally.otherFailures++
				}
				continue
			}
			o.finish(dec.index, dec.disposition, dec.reply, dec.status, dec.cause, attempt)
		}
		if out.callErr != nil {
			return stop(out.callErr)
		}
	}
	o.exhaust(tally)
	return o.result, nil
}

func deferAll(o *destinationOutcomes, pending []int, cause error, attempt int) {
	for _, i := range pending {
		o.defer_(i, nil, cause, attempt)
	}
}

// resolveAttempt records an MX skipped before any connection.
func resolveAttempt(s routeStep, cause error, policies []PolicyResult) AttemptResult {
	return AttemptResult{MX: s.host.name, Preference: s.host.pref, Stage: StageResolve, Policies: policies, Cause: cause}
}
