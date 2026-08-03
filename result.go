package smtp

// RecipientResult is the server's reply for a recipient-associated command.
// RcptBatch uses it for each RCPT command. After content submission, SMTP
// (RFC 5321 §4.1.1.4) sends one reply for the whole message, applied here to
// every accepted recipient; LMTP (RFC 2033 §4.2) sends one reply per recipient,
// in RCPT order, after the final "." — and recipients can succeed or fail
// independently, which SMTP cannot express.
//
// Callers constructing a RecipientResult literal — for example scripted
// fake-server expectations — must use keyed fields.
type RecipientResult struct {
	// Recipient is the forward-path (RCPT TO address) this reply is for.
	Recipient string
	// Command is the command whose reply this is: "RCPT" for RcptBatch,
	// "DATA" under plain SMTP/LMTP, or "BDAT" under CHUNKING (RFC 3030).
	Command string
	// Code is the three-digit reply code for this recipient (RFC 5321
	// §4.2, RFC 2033 §4.2).
	Code int
	// Enhanced is the RFC 3463 enhanced status code, zero valued if the
	// reply carried none.
	Enhanced EnhancedCode
	// Text is the reply text.
	Text string

	_ struct{}
}

// RcptResult is the RFC 5321 ordered outcome of an smtpclient RcptBatch call: one
// RecipientResult for every requested forward-path, including rejections.
// A transport or protocol failure still returns separately from the slice.
type RcptResult []RecipientResult

// AllAccepted reports whether every RFC 5321 RCPT command succeeded. It reports false
// for an empty result.
func (r RcptResult) AllAccepted() bool {
	if len(r) == 0 {
		return false
	}
	for _, recipient := range r {
		if !recipient.Accepted() {
			return false
		}
	}
	return true
}

// Errors returns the *Error for each rejected RFC 5321 RCPT command in result order.
// It returns an empty, non-nil slice when every recipient was accepted.
func (r RcptResult) Errors() []*Error {
	errs := make([]*Error, 0, len(r))
	for _, recipient := range r {
		if err := recipient.Err(); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}

// Accepted reports whether the server accepted the message for this
// recipient: a 2yz reply code (RFC 5321 §4.2.1).
func (r RecipientResult) Accepted() bool { return r.Code >= 200 && r.Code < 300 }

// Err returns this recipient's RFC 5321 or RFC 2033 reply as an *Error, or nil when Accepted
// reports true.
//
// It returns the concrete *Error rather than error so callers can reach Code
// and Enhanced without errors.As. The usual typed-nil hazard applies: assign
// the result to an error variable and a nil *Error becomes a non-nil error
// interface. Test it as *Error — if err := r.Err(); err != nil — or use
// Accepted.
func (r RecipientResult) Err() *Error {
	if r.Accepted() {
		return nil
	}
	return &Error{Code: r.Code, Enhanced: r.Enhanced, Text: r.Text, Command: r.Command}
}

// DataResult is the outcome of submitting message content: one
// RecipientResult per recipient the server accepted at RCPT time.
//
// This is a per-recipient collection from the first commit
// (docs/API-STABILITY.md §8), not a single "reply to DATA" value. LMTP (RFC
// 2033) returns one reply per recipient; SMTP (RFC 5321) returns one reply
// for the whole message. SMTP is modelled as the single-element case: every
// recipient's RecipientResult carries the identical Code, Enhanced and Text
// copied from that one reply. This shape must not change when LMTP command
// support (T07) lands — LMTP is the N-element case of the same type, not a
// reason to introduce a second return type on the most frequently called
// method in the library.
type DataResult []RecipientResult

// AllAccepted reports whether every RFC 5321 or RFC 2033 recipient in d was Accepted. It reports
// false for an empty DataResult, since there is then nothing to have
// accepted.
func (d DataResult) AllAccepted() bool {
	if len(d) == 0 {
		return false
	}
	for _, r := range d {
		if !r.Accepted() {
			return false
		}
	}
	return true
}

// Errors returns the *Error for each rejected RFC 5321 or RFC 2033 recipient in d, in DataResult
// order. It returns an empty, non-nil slice when every recipient succeeded.
func (d DataResult) Errors() []*Error {
	errs := make([]*Error, 0, len(d))
	for _, r := range d {
		if err := r.Err(); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
