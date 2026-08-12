package smtpserver

import "fmt"

// listenerMode is fixed when a listener is constructed. It is intentionally
// private: SMTP and LMTP are a closed implementation choice, not an extensible
// wire keyword set.
type listenerMode uint8

const (
	modeSMTP listenerMode = iota
	modeLMTP
)

type sessionPhase uint8

const (
	phaseConnected sessionPhase = iota
	phaseReady
	phaseMail
	phaseRecipients
	phaseFailedBDAT
	phaseClosed
)

func (p sessionPhase) String() string {
	switch p {
	case phaseConnected:
		return "connected"
	case phaseReady:
		return "ready"
	case phaseMail:
		return "mail"
	case phaseRecipients:
		return "recipients"
	case phaseFailedBDAT:
		return "failed BDAT"
	case phaseClosed:
		return "closed"
	default:
		return "unknown"
	}
}

type protocolState struct {
	mode          listenerMode
	phase         sessionPhase
	tls           bool
	authenticated bool
	hello         string
}

func newProtocolState(mode listenerMode) protocolState {
	return protocolState{mode: mode, phase: phaseConnected}
}

type commandLegality uint8

const (
	commandUnknown commandLegality = iota
	commandAllowed
	commandWrongMode
	commandWrongState
)

type modeSet uint8

const (
	modeSetSMTP modeSet = 1 << iota
	modeSetLMTP
	modeSetBoth = modeSetSMTP | modeSetLMTP
)

type phaseSet uint8

func phases(values ...sessionPhase) phaseSet {
	var set phaseSet
	for _, value := range values {
		set |= 1 << value
	}
	return set
}

func (set phaseSet) contains(phase sessionPhase) bool {
	return set&(1<<phase) != 0
}

type commandRule struct {
	modes                  modeSet
	phases                 phaseSet
	requirePlaintext       bool
	requireUnauthenticated bool
}

// commandRules is the single source of sequencing truth. Command handlers
// perform syntax and backend work; they do not grow their own state checks.
// Extension commands add a row here when their implementation lands.
var commandRules = map[string]commandRule{
	"HELO":     {modes: modeSetSMTP, phases: phases(phaseConnected, phaseReady, phaseMail, phaseRecipients)},
	"EHLO":     {modes: modeSetSMTP, phases: phases(phaseConnected, phaseReady, phaseMail, phaseRecipients)},
	"LHLO":     {modes: modeSetLMTP, phases: phases(phaseConnected, phaseReady, phaseMail, phaseRecipients)},
	"MAIL":     {modes: modeSetBoth, phases: phases(phaseReady, phaseMail, phaseRecipients)},
	"RCPT":     {modes: modeSetBoth, phases: phases(phaseMail, phaseRecipients)},
	"DATA":     {modes: modeSetBoth, phases: phases(phaseRecipients)},
	"BDAT":     {modes: modeSetBoth, phases: phases(phaseRecipients, phaseFailedBDAT)},
	"RSET":     {modes: modeSetBoth, phases: phases(phaseReady, phaseMail, phaseRecipients, phaseFailedBDAT)},
	"NOOP":     {modes: modeSetBoth, phases: phases(phaseConnected, phaseReady, phaseMail, phaseRecipients, phaseFailedBDAT)},
	"QUIT":     {modes: modeSetBoth, phases: phases(phaseConnected, phaseReady, phaseMail, phaseRecipients, phaseFailedBDAT)},
	"VRFY":     {modes: modeSetBoth, phases: phases(phaseReady, phaseMail, phaseRecipients)},
	"EXPN":     {modes: modeSetBoth, phases: phases(phaseReady, phaseMail, phaseRecipients)},
	"HELP":     {modes: modeSetBoth, phases: phases(phaseReady, phaseMail, phaseRecipients)},
	"ETRN":     {modes: modeSetSMTP, phases: phases(phaseReady)},
	"STARTTLS": {modes: modeSetBoth, phases: phases(phaseReady), requirePlaintext: true},
	"AUTH":     {modes: modeSetBoth, phases: phases(phaseReady), requireUnauthenticated: true},
}

func (s protocolState) legality(verb string) commandLegality {
	rule, ok := commandRules[verb]
	if !ok {
		return commandUnknown
	}
	mode := modeSetSMTP
	if s.mode == modeLMTP {
		mode = modeSetLMTP
	}
	if rule.modes&mode == 0 {
		return commandWrongMode
	}
	if !rule.phases.contains(s.phase) || rule.requirePlaintext && s.tls || rule.requireUnauthenticated && s.authenticated {
		return commandWrongState
	}
	return commandAllowed
}

type stateEvent uint8

const (
	eventHello stateEvent = iota
	eventMailAccepted
	eventRecipientAccepted
	eventTransactionComplete
	eventTransactionFailed
	eventBDATFailed
	eventReset
	eventStartTLS
	eventAuthenticated
	eventClose
)

type phaseTransition struct {
	from phaseSet
	to   sessionPhase
}

var phaseTransitions = map[stateEvent]phaseTransition{
	eventHello:               {from: phases(phaseConnected, phaseReady, phaseMail, phaseRecipients), to: phaseReady},
	eventMailAccepted:        {from: phases(phaseReady, phaseMail, phaseRecipients), to: phaseMail},
	eventRecipientAccepted:   {from: phases(phaseMail, phaseRecipients), to: phaseRecipients},
	eventTransactionComplete: {from: phases(phaseRecipients), to: phaseReady},
	eventTransactionFailed:   {from: phases(phaseMail, phaseRecipients), to: phaseReady},
	eventBDATFailed:          {from: phases(phaseRecipients), to: phaseFailedBDAT},
	eventReset:               {from: phases(phaseReady, phaseMail, phaseRecipients, phaseFailedBDAT), to: phaseReady},
	eventStartTLS:            {from: phases(phaseReady), to: phaseConnected},
	eventAuthenticated:       {from: phases(phaseReady), to: phaseReady},
	eventClose:               {from: phases(phaseConnected, phaseReady, phaseMail, phaseRecipients, phaseFailedBDAT), to: phaseClosed},
}

func (s *protocolState) transition(event stateEvent) error {
	transition, ok := phaseTransitions[event]
	if !ok {
		return fmt.Errorf("smtpserver: unknown state transition %d", event)
	}
	if !transition.from.contains(s.phase) {
		return fmt.Errorf("smtpserver: state transition %d is not valid while session is %s", event, s.phase)
	}
	s.phase = transition.to
	s.applyEvent(event)
	return nil
}

func (s *protocolState) applyEvent(event stateEvent) {
	switch event {
	case eventHello:
		// The command handler sets the validated hello argument after this
		// transition. A new greeting abandons the current transaction.
		s.hello = ""
	case eventStartTLS:
		// RFC 3207 section 4.2: successful STARTTLS discards all knowledge
		// obtained before the handshake and requires a fresh greeting.
		s.tls = true
		s.authenticated = false
		s.hello = ""
	case eventAuthenticated:
		s.authenticated = true
	case eventClose:
		s.hello = ""
	}
}
