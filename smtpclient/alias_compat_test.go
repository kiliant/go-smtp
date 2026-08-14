package smtpclient

import (
	"testing"

	smtp "github.com/kiliant/go-smtp"
)

// alias_compat_test.go is the evidence for T16's alias-preserving moves, and it
// exists because `apidiff` cannot supply that evidence.
//
// T16 moved Limits/ParseLimitsParam (RFC 9422) and TraceEvent/TraceDirection
// (RFC 5321 observability) from this package to package smtp, leaving aliases
// behind. docs/API-STABILITY.md §10 requires the moves to be verified rather
// than asserted. Run against the pre-move commit, apidiff reports every one of
// them under "Incompatible changes", including the visibly absurd
//
//	ClientOptions.Trace: changed from func(TraceEvent) to func(TraceEvent)
//
// because it compares two independently loaded copies of the module and treats
// a type whose declaring package changed as a different type — it has no way to
// see that an alias makes the two spellings the same type. That is a tool
// limitation, not a break: Go type identity is what decides whether a caller
// still compiles, and these assertions are that test, at compile time.
//
// Each declaration below fails to compile if the corresponding alias is ever
// replaced by a redeclared type. Assignment between two *distinct* named types
// is illegal in Go even when their underlying types are identical, so these are
// identity checks and not conversions.
var (
	_ Limits              = smtp.Limits{}
	_ smtp.Limits         = Limits{}
	_ TraceEvent          = smtp.TraceEvent{}
	_ smtp.TraceEvent     = TraceEvent{}
	_ TraceDirection      = smtp.TraceSent
	_ smtp.TraceDirection = TraceReceived
	_                     = smtp.Limits{} == smtp.Limits{}

	// The trace hook field is the shape a caller actually writes, and the one
	// apidiff flagged: a hook declared with the package smtp spelling must
	// satisfy a field declared with this package's spelling.
	_ = ClientOptions{Trace: func(smtp.TraceEvent) {}}
	_ = ClientOptions{Trace: func(TraceEvent) {}}
)

// TestAliasedLimitsRoundTripsAcrossPackages proves the aliases at run time as
// well as compile time: a value parsed by the relocated parser is usable
// wherever the old spelling was, with no conversion.
func TestAliasedLimitsRoundTripsAcrossPackages(t *testing.T) {
	parsed, err := ParseLimitsParam("MAILMAX=100 RCPTMAX=50")
	if err != nil {
		t.Fatalf("ParseLimitsParam: %v", err)
	}
	direct, err := smtp.ParseLimitsParam("MAILMAX=100 RCPTMAX=50")
	if err != nil {
		t.Fatalf("smtp.ParseLimitsParam: %v", err)
	}
	if parsed != direct {
		t.Fatalf("smtpclient.ParseLimitsParam = %+v, smtp.ParseLimitsParam = %+v; the forwarding wrapper must not diverge", parsed, direct)
	}
	// Assigned through the old spelling, read through the new one.
	var old Limits = direct
	if old.MailMax != 100 || old.RcptMax != 50 {
		t.Fatalf("Limits = %+v, want MailMax 100 and RcptMax 50", old)
	}
}

// TestAliasedTraceConstantsKeepTheirWireValues pins the constant aliases. A
// constant re-declared with a fresh value would satisfy the type assertions
// above while silently changing what a caller's switch matches.
func TestAliasedTraceConstantsKeepTheirWireValues(t *testing.T) {
	if TraceSent != smtp.TraceSent || string(TraceSent) != "sent" {
		t.Errorf("TraceSent = %q, want smtp.TraceSent (%q)", TraceSent, smtp.TraceSent)
	}
	if TraceReceived != smtp.TraceReceived || string(TraceReceived) != "received" {
		t.Errorf("TraceReceived = %q, want smtp.TraceReceived (%q)", TraceReceived, smtp.TraceReceived)
	}
}
