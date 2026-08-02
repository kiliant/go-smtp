package smtp

import (
	"strconv"
	"strings"
)

// Error is the single error type for every protocol failure this module
// surfaces, per docs/API-STABILITY.md §5. Extensions only ever add reply
// codes and enhanced-status detail values — a data change — so they never
// need a distinct error type; per-recipient failures are a collection of
// *Error values (DataResult), not a new type either. See §8 and result.go.
//
// Callers match with errors.As and compare Code or Enhanced.
//
// Callers constructing an Error literal — chiefly in tests — must use keyed
// fields.
type Error struct {
	// Code is the three-digit SMTP/LMTP reply code, e.g. 550 (RFC 5321
	// §4.2, RFC 2033). It is an int, not an enum: the code space is open
	// by construction and servers emit codes no RFC lists.
	Code int
	// Enhanced is the RFC 3463 class.subject.detail code (RFC 2034,
	// ENHANCEDSTATUSCODES), zero valued if the reply carried none.
	Enhanced EnhancedCode
	// Text is the reply text, newline-joined across a multiline reply
	// (RFC 5321 §4.2).
	Text string
	// Command is the command that provoked the reply, e.g. "RCPT".
	Command string
	// Err is an optional underlying protocol or transport cause, e.g. a
	// *smtpwire parse error or a net.Error. Unwrap returns it.
	Err error

	_ struct{}
}

// Error implements the error interface. It formats as
// "COMMAND: code enhanced text: cause", omitting any part that is empty and
// never introducing a separator next to nothing.
func (e *Error) Error() string {
	var body strings.Builder
	if e.Code != 0 {
		body.WriteString(strconv.Itoa(e.Code))
	}
	if raw := e.Enhanced.Raw; raw != "" {
		if body.Len() > 0 {
			body.WriteString(" ")
		}
		body.WriteString(raw)
	} else if e.Enhanced.Valid() {
		if body.Len() > 0 {
			body.WriteString(" ")
		}
		body.WriteString(e.Enhanced.String())
	}
	if e.Text != "" {
		if body.Len() > 0 {
			body.WriteString(" ")
		}
		body.WriteString(e.Text)
	}
	if e.Err != nil {
		if body.Len() > 0 {
			body.WriteString(": ")
		}
		body.WriteString(e.Err.Error())
	}

	if e.Command == "" {
		if body.Len() == 0 {
			return "smtp: error"
		}
		return body.String()
	}
	if body.Len() == 0 {
		return e.Command + ": smtp: error"
	}
	return e.Command + ": " + body.String()
}

// Unwrap returns Err, so errors.Is and errors.As see through an *Error to
// its underlying cause when there is one.
func (e *Error) Unwrap() error { return e.Err }

// IsTransient reports whether Code is a 4yz reply (RFC 5321 §4.2.1): the
// server considers the condition temporary, and the same command may
// succeed on a later attempt.
func (e *Error) IsTransient() bool { return e.Code >= 400 && e.Code < 500 }

// IsPermanent reports whether Code is a 5yz reply (RFC 5321 §4.2.1): the
// server considers the condition permanent, and repeating the command
// unchanged will not help.
func (e *Error) IsPermanent() bool { return e.Code >= 500 && e.Code < 600 }
