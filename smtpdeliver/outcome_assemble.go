package smtpdeliver

import (
	"context"
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
// each keeps its last temporary failure, unless the tally shows that no
// candidate could ever take the message (see exhaustionTally).
func (o *destinationOutcomes) exhaust(t exhaustionTally) {
	permanent, status, cause := t.verdict()
	for _, i := range o.pending() {
		last := o.last[i]
		if permanent {
			o.finish(i, DispositionPermanent, last.reply, status, errors.Join(cause, last.cause), last.attempt)
			continue
		}
		o.finish(i, DispositionTemporary, last.reply, smtp.EnhancedCode{}, last.cause, last.attempt)
	}
}

// stopPending finishes recipients still pending when the call ends early.
// One that already received a failure from an earlier attempt keeps it as a
// temporary outcome; one never reached is not attempted.
func (o *destinationOutcomes) stopPending(cause error) {
	for _, i := range o.pending() {
		last := o.last[i]
		if last.attempt >= 0 && (last.reply != nil || (last.cause != nil && !isContextError(last.cause))) {
			o.finish(i, DispositionTemporary, last.reply, smtp.EnhancedCode{}, last.cause, last.attempt)
			continue
		}
		o.finish(i, DispositionNotAttempted, nil, statusNone, cause, -1)
	}
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// exhaustionTally counts how a destination's candidates failed.
//
// RFC 8689 §4.2.1 makes a REQUIRETLS message undeliverable, and so
// permanently failed, when no MX host can meet its requirements. RFC 6531
// §3.5 and RFC 6152 §3 likewise leave a message needing SMTPUTF8 or 8-bit
// transport undeliverable when no MX supports it. Either verdict needs every
// failure to be of that kind: one candidate that failed for an unrelated
// reason (a refused connection, a 4yz, a network error) or that met the
// REQUIRETLS requirements leaves the ordinary temporary outcome.
type exhaustionTally struct {
	requireTLS       bool
	metRequireTLS    bool
	tlsRequirement   int
	notAdvertised    int
	capability       int
	capabilityStatus smtp.EnhancedCode
	other            int
}

func (t *exhaustionTally) add(kind failureKind, out *attemptOutcome) {
	switch kind {
	case failureTLSRequirement:
		t.tlsRequirement++
	case failureRequireTLSNotAdvertised:
		t.notAdvertised++
	case failureCapability:
		t.capability++
		if out != nil {
			t.capabilityStatus = out.capabilityStatus
		}
	case failureOther:
		t.other++
	}
}

func (t exhaustionTally) verdict() (bool, smtp.EnhancedCode, error) {
	if t.other > 0 {
		return false, statusNone, nil
	}
	if t.requireTLS && !t.metRequireTLS && t.capability == 0 && t.tlsRequirement+t.notAdvertised > 0 {
		if t.notAdvertised > 0 {
			return true, statusRequireTLSNeeded, errRequireTLSExhausted
		}
		return true, statusEncryptionNeeded, errRequireTLSExhausted
	}
	if t.capability > 0 && t.tlsRequirement == 0 && t.notAdvertised == 0 {
		return true, t.capabilityStatus, errCapabilityExhausted
	}
	return false, statusNone, nil
}

var (
	errRequireTLSExhausted = errors.New("smtpdeliver: no MX host meets the REQUIRETLS requirements (RFC 8689 §4.2.1)")
	errCapabilityExhausted = errors.New("smtpdeliver: no MX host supports an extension the message requires")
)
