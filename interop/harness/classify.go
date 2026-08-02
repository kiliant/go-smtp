package harness

import "fmt"

// Outcome classifies why a harness step did not simply succeed. Callers
// switch on it to decide whether a result means the client is wrong, the
// server is incompatible, the local environment is unusable, or the harness
// itself is broken — four very different responses collapsed into a single
// error would hide which one applies.
type Outcome int

const (
	// OutcomeOK reports success; there is no failure to classify.
	OutcomeOK Outcome = iota
	// OutcomeProtocolFailure means the client and a spec-compliant server
	// disagree: a real client bug against this server.
	OutcomeProtocolFailure
	// OutcomeIncompatible means the server does not implement something its
	// profile does not claim either — the correct response is Skip, not
	// Fail.
	OutcomeIncompatible
	// OutcomeProfileViolation means the server's profile claims an
	// extension or behavior the server did not actually provide. This is a
	// hard failure: a silently downgraded container must not present as a
	// skip.
	OutcomeProfileViolation
	// OutcomeEnvironmental means the local environment (podman absent, no
	// machine running, image pull failed, port in use) prevented the test
	// from exercising the client at all.
	OutcomeEnvironmental
	// OutcomeTimeout means a bounded operation did not complete in time.
	OutcomeTimeout
	// OutcomeHarnessFailure means the harness's own code — not the client,
	// not the server — is at fault (e.g. a malformed profile, a sink that
	// paniced).
	OutcomeHarnessFailure
)

// String renders the outcome for diagnostics and test names.
func (o Outcome) String() string {
	switch o {
	case OutcomeOK:
		return "ok"
	case OutcomeProtocolFailure:
		return "protocol-failure"
	case OutcomeIncompatible:
		return "incompatible"
	case OutcomeProfileViolation:
		return "profile-violation"
	case OutcomeEnvironmental:
		return "environmental"
	case OutcomeTimeout:
		return "timeout"
	case OutcomeHarnessFailure:
		return "harness-failure"
	default:
		return "unknown"
	}
}

// Result is a classified harness outcome. It implements error so callers can
// return it directly, but callers that need the classification (rather than
// just a message) should keep the concrete type rather than going through
// the error interface, since a wrapped Result loses none of Outcome, Server,
// or Transcript but errors.As still works for that case too.
type Result struct {
	Outcome    Outcome
	Server     string
	Step       string
	Err        error
	Transcript *Transcript
}

func (r *Result) Error() string {
	if r == nil {
		return ""
	}
	msg := fmt.Sprintf("%s[%s]: %s", r.Server, r.Step, r.Outcome)
	if r.Err != nil {
		msg += ": " + r.Err.Error()
	}
	return msg
}

func (r *Result) Unwrap() error {
	if r == nil {
		return nil
	}
	return r.Err
}

// NewResult builds a classified Result, attaching a transcript when one was
// recorded for the step.
func NewResult(server, step string, outcome Outcome, err error, t *Transcript) *Result {
	return &Result{Outcome: outcome, Server: server, Step: step, Err: err, Transcript: t}
}
