package smtpdeliver

import (
	"errors"

	smtp "github.com/kiliant/go-smtp"
)

// Enhanced status codes the engine assigns, RFC 3463 §3.4 and RFC 8689 §4.2.1.
var (
	statusNone             = smtp.EnhancedCode{}
	statusLocalFailure     = smtp.ParseEnhancedCode("4.3.0")
	statusEncryptionNeeded = smtp.ParseEnhancedCode("5.7.10")
	statusRequireTLSNeeded = smtp.ParseEnhancedCode("5.7.30")
)

// destinationOutcomes tracks every recipient of one destination from
// "pending" to a final disposition. It is the single place outcomes are
// written, which is what keeps the "exactly once, in input order" and
// "never retried after an authoritative success" invariants checkable.
type destinationOutcomes struct {
	result DestinationResult
	// final marks recipients whose outcome may no longer change.
	final []bool
	// last holds the most recent temporary failure of each pending
	// recipient, reported if candidates run out.
	last []pendingFailure
}

// pendingFailure is a temporary failure kept for a pending recipient.
type pendingFailure struct {
	reply   *smtp.RecipientResult
	cause   error
	attempt int
}

func newDestinationOutcomes(domain string, recipients []Recipient) *destinationOutcomes {
	o := &destinationOutcomes{
		result: DestinationResult{Domain: domain, Recipients: make([]RecipientOutcome, len(recipients))},
		final:  make([]bool, len(recipients)),
		last:   make([]pendingFailure, len(recipients)),
	}
	for i, r := range recipients {
		o.result.Recipients[i] = RecipientOutcome{Address: r.Address, Attempt: -1}
		o.last[i].attempt = -1
	}
	return o
}

// pending returns the indices of recipients without a final outcome, in
// input order.
func (o *destinationOutcomes) pending() []int {
	var out []int
	for i, done := range o.final {
		if !done {
			out = append(out, i)
		}
	}
	return out
}

// finish makes recipient i's outcome final. Finishing a recipient twice is a
// programming error: it would mean a recipient was retried after its outcome
// was decided, the duplicate-delivery bug this engine exists to prevent.
func (o *destinationOutcomes) finish(i int, d Disposition, reply *smtp.RecipientResult, status smtp.EnhancedCode, cause error, attempt int) {
	if o.final[i] {
		panic("smtpdeliver: recipient outcome finished twice")
	}
	o.final[i] = true
	if !status.Valid() && reply != nil {
		status = reply.Enhanced
	}
	o.result.Recipients[i] = RecipientOutcome{
		Address:     o.result.Recipients[i].Address,
		Disposition: d,
		Reply:       reply,
		Status:      status,
		Cause:       cause,
		Attempt:     attempt,
	}
}

// defer records a temporary failure for pending recipient i; another
// candidate may still deliver it.
func (o *destinationOutcomes) defer_(i int, reply *smtp.RecipientResult, cause error, attempt int) {
	o.last[i] = pendingFailure{reply: reply, cause: cause, attempt: attempt}
}

// finishAll gives every pending recipient the same final outcome.
func (o *destinationOutcomes) finishAll(d Disposition, status smtp.EnhancedCode, cause error) {
	for _, i := range o.pending() {
		o.finish(i, d, nil, status, cause, -1)
	}
}

// exhaust finishes recipients still pending once candidates are used up:
// each keeps its last temporary failure. REQUIRETLS exhaustion is permanent
// (RFC 8689 §4.2.1), with 5.7.30 when servers lacked REQUIRETLS and 5.7.10
// otherwise.
func (o *destinationOutcomes) exhaust(requireTLS requireTLSTally) {
	for _, i := range o.pending() {
		last := o.last[i]
		if requireTLS.exhausted() {
			status := statusEncryptionNeeded
			if requireTLS.notAdvertised > 0 {
				status = statusRequireTLSNeeded
			}
			o.finish(i, DispositionPermanent, last.reply, status, errors.Join(errRequireTLSExhausted, last.cause), last.attempt)
			continue
		}
		o.finish(i, DispositionTemporary, last.reply, smtp.EnhancedCode{}, last.cause, last.attempt)
	}
}

// requireTLSTally counts, for a REQUIRETLS message, how candidates failed.
// RFC 8689 §4.2.1 makes the message undeliverable when no MX host meets the
// requirements; a candidate that failed for an unrelated reason (a refused
// connection, a 4yz reply) leaves the ordinary temporary outcome in place.
type requireTLSTally struct {
	enabled       bool
	tlsFailures   int
	notAdvertised int
	otherFailures int
}

func (t requireTLSTally) exhausted() bool {
	return t.enabled && t.tlsFailures+t.notAdvertised > 0 && t.otherFailures == 0
}

var errRequireTLSExhausted = errors.New("smtpdeliver: no MX host meets the REQUIRETLS requirements (RFC 8689 §4.2.1)")
