package smtpserver

import "testing"

func TestCommandRulesCoverModesAndFailedBDAT(t *testing.T) {
	tests := []struct {
		name  string
		mode  listenerMode
		phase sessionPhase
		verb  string
		want  commandLegality
	}{
		{name: "SMTP EHLO", mode: modeSMTP, phase: phaseConnected, verb: "EHLO", want: commandAllowed},
		{name: "SMTP rejects LHLO", mode: modeSMTP, phase: phaseConnected, verb: "LHLO", want: commandWrongMode},
		{name: "LMTP LHLO", mode: modeLMTP, phase: phaseConnected, verb: "LHLO", want: commandAllowed},
		{name: "LMTP rejects EHLO", mode: modeLMTP, phase: phaseConnected, verb: "EHLO", want: commandWrongMode},
		{name: "MAIL needs greeting", mode: modeSMTP, phase: phaseConnected, verb: "MAIL", want: commandWrongState},
		{name: "DATA needs recipient", mode: modeSMTP, phase: phaseMail, verb: "DATA", want: commandWrongState},
		{name: "DATA after recipient", mode: modeSMTP, phase: phaseRecipients, verb: "DATA", want: commandAllowed},
		{name: "failed BDAT consumes BDAT", mode: modeSMTP, phase: phaseFailedBDAT, verb: "BDAT", want: commandAllowed},
		{name: "failed BDAT allows RSET", mode: modeSMTP, phase: phaseFailedBDAT, verb: "RSET", want: commandAllowed},
		{name: "failed BDAT allows NOOP", mode: modeSMTP, phase: phaseFailedBDAT, verb: "NOOP", want: commandAllowed},
		{name: "failed BDAT allows QUIT", mode: modeSMTP, phase: phaseFailedBDAT, verb: "QUIT", want: commandAllowed},
		{name: "failed BDAT rejects MAIL", mode: modeSMTP, phase: phaseFailedBDAT, verb: "MAIL", want: commandWrongState},
		{name: "unknown verb", mode: modeSMTP, phase: phaseReady, verb: "FUTURE", want: commandUnknown},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := newProtocolState(test.mode)
			state.phase = test.phase
			if got := state.legality(test.verb); got != test.want {
				t.Fatalf("legality(%q) = %d, want %d", test.verb, got, test.want)
			}
		})
	}
}

func TestCommandRulesApplyTLSAndAuthenticationConditions(t *testing.T) {
	state := newProtocolState(modeSMTP)
	state.phase = phaseReady
	if got := state.legality("STARTTLS"); got != commandAllowed {
		t.Fatalf("STARTTLS before TLS = %d, want allowed", got)
	}
	state.tls = true
	if got := state.legality("STARTTLS"); got != commandWrongState {
		t.Fatalf("STARTTLS after TLS = %d, want wrong state", got)
	}
	if got := state.legality("AUTH"); got != commandAllowed {
		t.Fatalf("AUTH before authentication = %d, want allowed", got)
	}
	state.authenticated = true
	if got := state.legality("AUTH"); got != commandWrongState {
		t.Fatalf("AUTH after authentication = %d, want wrong state", got)
	}
}

func TestProtocolStateTransactionSequence(t *testing.T) {
	state := newProtocolState(modeSMTP)
	for _, event := range []stateEvent{
		eventHello,
		eventMailAccepted,
		eventRecipientAccepted,
		eventTransactionComplete,
	} {
		if err := state.transition(event); err != nil {
			t.Fatalf("transition %d: %v", event, err)
		}
	}
	if state.phase != phaseReady {
		t.Fatalf("phase = %s, want ready", state.phase)
	}
}

func TestProtocolStateFailedBDATRequiresReset(t *testing.T) {
	state := newProtocolState(modeSMTP)
	state.phase = phaseRecipients
	if err := state.transition(eventBDATFailed); err != nil {
		t.Fatal(err)
	}
	if err := state.transition(eventMailAccepted); err == nil {
		t.Fatal("MAIL transition succeeded from failed BDAT")
	}
	if err := state.transition(eventReset); err != nil {
		t.Fatal(err)
	}
	if state.phase != phaseReady {
		t.Fatalf("phase = %s, want ready", state.phase)
	}
}

func TestProtocolStateSTARTTLSDiscardsKnowledge(t *testing.T) {
	state := newProtocolState(modeSMTP)
	state.phase = phaseReady
	state.authenticated = true
	state.hello = "client.example"
	if err := state.transition(eventStartTLS); err != nil {
		t.Fatal(err)
	}
	if state.phase != phaseConnected || !state.tls || state.authenticated || state.hello != "" {
		t.Fatalf("state after STARTTLS = %+v", state)
	}
}

func TestProtocolStateRejectsIllegalTransition(t *testing.T) {
	state := newProtocolState(modeSMTP)
	if err := state.transition(eventRecipientAccepted); err == nil {
		t.Fatal("recipient accepted before MAIL")
	}
	if state.phase != phaseConnected {
		t.Fatalf("illegal transition changed phase to %s", state.phase)
	}
}
