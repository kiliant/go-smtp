package smtpclient

import (
	"errors"

	smtp "github.com/kiliant/go-smtp"
)

// ErrFinalStatusUnknown reports that the operation completing a mail
// transaction may have reached the server but no final status was obtained
// for some or all recipients. RFC 5321 §4.2.5 transfers responsibility for a
// message only when the client receives the positive reply after end of data,
// so after this failure the server may or may not have accepted the message.
// Retrying it unchanged risks a duplicate delivery. The completing operations
// are currently the DATA terminator, a BDAT LAST chunk (RFC 3030) and a BURL
// LAST command (RFC 4468); an operation added later that completes a
// transaction follows the same rule.
//
// It is never returned directly: the error is an *smtp.Error whose Err chain
// contains ErrFinalStatusUnknown as well as the original cause. Use errors.Is
// and errors.As rather than comparing Err itself; errors.Is(err,
// context.Canceled), for example, still reports true after a cancellation.
//
// A failure while sending ordinary DATA content, a non-LAST BDAT chunk or a
// non-LAST BURL cannot complete a transaction and does not carry it. A final
// reply received for an SMTP transaction is authoritative and does not carry
// it either.
//
// Together with smtp.DataResult this forms a three-way contract that every
// call completing a mail transaction keeps:
//
//   - nil error: the result holds a final status for every recipient;
//   - an error wrapping ErrFinalStatusUnknown: the result is an authoritative
//     prefix, possibly nil, and every recipient after it has unknown status;
//   - any other error: the message was not accepted for any recipient by
//     this call, and the result is nil.
//
// A call that does not complete a transaction, such as BURL without Last,
// returns a nil result; a nil error then only means the operation was
// accepted, and a non-nil error never wraps ErrFinalStatusUnknown.
//
// In LMTP mode (RFC 2033) a reply is authoritative for the recipient it
// answers. When the per-recipient reply stream ends early, including with a
// session-level 421 reply, the replies already received are returned as the
// prefix. When the replies cannot be matched to recipients with confidence,
// for example because the server sent more replies than accepted recipients,
// the result is nil and the status of every recipient is unknown. A later
// release may return authoritative results where it can prove the match.
var ErrFinalStatusUnknown = errors.New("smtpclient: final status unknown; the server may have accepted the message")

// finalStatusUnknownError joins ErrFinalStatusUnknown with the failure that
// caused it, keeping both reachable through errors.Is and errors.As.
type finalStatusUnknownError struct {
	cause error
}

func (e *finalStatusUnknownError) Error() string {
	if e.cause == nil {
		return ErrFinalStatusUnknown.Error()
	}
	return ErrFinalStatusUnknown.Error() + ": " + e.cause.Error()
}

func (e *finalStatusUnknownError) Unwrap() []error {
	if e.cause == nil {
		return []error{ErrFinalStatusUnknown}
	}
	return []error{ErrFinalStatusUnknown, e.cause}
}

// finalStatusUnknown classifies err, raised after the operation completing an
// SMTP transaction may have reached the peer, as ErrFinalStatusUnknown. A nil
// err, and an *smtp.Error carrying a received reply code, are authoritative
// for the single final reply and returned unchanged. Everything else is
// classified as by finalStatusUnknownStream.
func finalStatusUnknown(command string, err error) error {
	if smtpErr, ok := err.(*smtp.Error); ok && smtpErr.Code != 0 {
		return err
	}
	return finalStatusUnknownStream(command, err)
}

// finalStatusUnknownStream classifies a failure that ends an RFC 2033
// per-recipient reply stream early. Unlike finalStatusUnknown it wraps a
// coded reply too: a session-level 421 answers no recipient, so the
// recipients still waiting for a reply have unknown status. An *smtp.Error
// keeps its Command, Code, Enhanced and Text, so IsTransient still works, and
// gains the classification beneath them; any other error, such as a context
// error, is wrapped in a new *smtp.Error for command.
func finalStatusUnknownStream(command string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrFinalStatusUnknown) {
		return err
	}
	if smtpErr, ok := err.(*smtp.Error); ok {
		classified := *smtpErr
		classified.Err = &finalStatusUnknownError{cause: smtpErr.Err}
		return &classified
	}
	return &smtp.Error{Command: command, Err: &finalStatusUnknownError{cause: err}}
}

// authoritativePrefix returns the first n per-recipient results, the ones
// whose final replies were received before an LMTP reply stream failed. It
// returns nil when none were, as smtp.DataResult documents.
func authoritativePrefix(result smtp.DataResult, n int) smtp.DataResult {
	if n == 0 {
		return nil
	}
	return result[:n:n]
}

// classifyFinalReplyFailure classifies a failure reading the first final reply
// after a content-completion operation: one authoritative reply in SMTP mode,
// the start of the per-recipient stream in LMTP mode.
func (c *Client) classifyFinalReplyFailure(command string, err error) error {
	c.conn.mu.Lock()
	lmtp := c.conn.options.LMTP
	c.conn.mu.Unlock()
	if lmtp {
		return finalStatusUnknownStream(command, err)
	}
	return finalStatusUnknown(command, err)
}
