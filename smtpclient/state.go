package smtpclient

import "fmt"

// sessionState records states which matter to command validation. It is kept
// internal: exposing it as a closed enum would freeze a protocol state model
// that extensions can legitimately extend.
type sessionState uint8

const (
	stateConnected sessionState = iota
	stateGreeted
	stateTLS
	stateAuthenticated
	stateTransaction
	stateClosed
)

func (s sessionState) String() string {
	switch s {
	case stateConnected:
		return "connected"
	case stateGreeted:
		return "greeted"
	case stateTLS:
		return "tls"
	case stateAuthenticated:
		return "authenticated"
	case stateTransaction:
		return "transaction"
	case stateClosed:
		return "closed"
	default:
		return "unknown"
	}
}

func invalidState(command string, have sessionState, allowed ...sessionState) error {
	for _, state := range allowed {
		if have == state {
			return nil
		}
	}
	return fmt.Errorf("smtpclient: %s is not valid while session is %s", command, have)
}
