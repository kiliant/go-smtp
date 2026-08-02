package harness

import (
	"errors"
	"testing"
)

func TestResultErrorFormatting(t *testing.T) {
	r := NewResult("postfix", "assert-profile", OutcomeProfileViolation, errors.New("missing PIPELINING"), nil)
	got := r.Error()
	want := "postfix[assert-profile]: profile-violation: missing PIPELINING"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestResultUnwrap(t *testing.T) {
	cause := errors.New("boom")
	r := NewResult("mailpit", "sink", OutcomeHarnessFailure, cause, nil)
	if !errors.Is(r, cause) {
		t.Error("errors.Is should see through Result to its wrapped cause")
	}
}

func TestResultNilSafe(t *testing.T) {
	var r *Result
	if r.Error() != "" {
		t.Error("nil *Result.Error() should be empty")
	}
	if r.Unwrap() != nil {
		t.Error("nil *Result.Unwrap() should be nil")
	}
}

func TestOutcomeStringExhaustive(t *testing.T) {
	outcomes := []Outcome{
		OutcomeOK, OutcomeProtocolFailure, OutcomeIncompatible,
		OutcomeProfileViolation, OutcomeEnvironmental, OutcomeTimeout,
		OutcomeHarnessFailure,
	}
	seen := map[string]bool{}
	for _, o := range outcomes {
		s := o.String()
		if s == "" || s == "unknown" {
			t.Errorf("Outcome(%d).String() = %q", o, s)
		}
		if seen[s] {
			t.Errorf("duplicate Outcome string %q", s)
		}
		seen[s] = true
	}
}
