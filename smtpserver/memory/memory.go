// Package memory provides a non-durable, in-process RFC 5321 SMTP and RFC 2033
// LMTP sink for tests and development. It is not a queue, mailbox, relay, or
// production backend.
package memory

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/kiliant/go-smtp"
	"github.com/kiliant/go-smtp/smtpserver"
)

// Options configures an RFC 5321 SMTP or RFC 2033 LMTP Sink. Nil means
// defaults.
// Callers constructing an Options literal must use keyed fields.
type Options struct {
	_ struct{}
}

// Message is one RFC 5321 SMTP or RFC 2033 LMTP message accepted by the
// in-process sink. Data is the complete transparent message content presented
// to Session.Data. The recipient slice preserves accepted RCPT order and
// duplicates.
//
// Callers constructing a Message literal must use keyed fields.
type Message struct {
	// Mode is the SMTP or LMTP mode of the accepting session.
	Mode smtpserver.Mode
	// ReversePath is the parsed MAIL FROM path, empty for the null path.
	ReversePath string
	// Recipients contains parsed RCPT TO paths in issue order.
	Recipients []string
	// Data is a private copy of the accepted message content.
	Data []byte

	_ struct{}
}

// Sink stores accepted RFC 5321 SMTP or RFC 2033 LMTP messages in memory. It is
// safe for concurrent server sessions. A Sink is intentionally non-durable and
// must not be used in production.
type Sink struct {
	mu       sync.Mutex
	messages []Message
	backend  *smtpserver.Backend
}

// New constructs a non-durable RFC 5321 SMTP and RFC 2033 LMTP in-memory sink
// and its smtpserver backend.
func New(opts *Options) *Sink {
	_ = opts
	sink := &Sink{}
	sink.backend = &smtpserver.Backend{NewSession: sink.newSession}
	return sink
}

// Backend returns the concurrency-safe RFC 5321 SMTP and RFC 2033 LMTP backend
// owned by the sink. The returned pointer remains valid for the lifetime of
// Sink.
func (s *Sink) Backend() *smtpserver.Backend {
	if s == nil {
		return nil
	}
	return s.backend
}

// Messages returns a deep snapshot of accepted RFC 5321 SMTP or RFC 2033 LMTP
// messages in delivery order. Mutating the returned values does not affect the
// Sink.
func (s *Sink) Messages() []Message {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	messages := make([]Message, len(s.messages))
	for i, message := range s.messages {
		messages[i] = cloneMessage(message)
	}
	return messages
}

func (s *Sink) newSession(_ context.Context, conn *smtpserver.ConnInfo, _ *smtpserver.NewSessionOptions) (*smtpserver.Session, error) {
	mode := smtpserver.ModeSMTP
	if conn != nil && conn.Mode != "" {
		mode = conn.Mode
	}
	state := &sessionState{sink: s, mode: mode}
	return &smtpserver.Session{
		Mail:  state.mail,
		Rcpt:  state.rcpt,
		Data:  state.data,
		Reset: state.reset,
		Close: state.close,
	}, nil
}

type sessionState struct {
	sink       *Sink
	mode       smtpserver.Mode
	reverse    string
	recipients []string
	open       bool
	closed     bool
}

func (s *sessionState) mail(_ context.Context, reverse string, _ *smtp.MailOptions, _ *smtpserver.MailOptions) error {
	if s.closed {
		return errors.New("memory: Mail called after Close")
	}
	s.reverse = reverse
	s.recipients = s.recipients[:0]
	s.open = true
	return nil
}

func (s *sessionState) rcpt(_ context.Context, recipient string, _ *smtp.RcptOptions, _ *smtpserver.RcptOptions) error {
	if !s.open {
		return &smtp.Error{Code: 503, Enhanced: smtp.EnhancedCode{Class: 5, Subject: 5, Detail: 1}, Text: "MAIL required", Command: "RCPT"}
	}
	s.recipients = append(s.recipients, recipient)
	return nil
}

func (s *sessionState) data(_ context.Context, reader io.Reader, _ *smtpserver.DataOptions) (smtp.DataResult, error) {
	if !s.open || len(s.recipients) == 0 {
		return nil, errors.New("memory: Data called without an accepted recipient")
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	message := Message{
		Mode:        s.mode,
		ReversePath: s.reverse,
		Recipients:  append([]string(nil), s.recipients...),
		Data:        append([]byte(nil), data...),
	}
	s.sink.mu.Lock()
	s.sink.messages = append(s.sink.messages, message)
	s.sink.mu.Unlock()

	count := 1
	if s.mode == smtpserver.ModeLMTP {
		count = len(s.recipients)
	}
	result := make(smtp.DataResult, count)
	for i := range result {
		recipient := ""
		if s.mode == smtpserver.ModeLMTP {
			recipient = s.recipients[i]
		}
		result[i] = smtp.RecipientResult{
			Recipient: recipient,
			Command:   "DATA",
			Code:      250,
			Enhanced:  smtp.EnhancedCode{Class: 2, Subject: 0, Detail: 0},
			Text:      "OK",
		}
	}
	return result, nil
}

func (s *sessionState) reset(_ context.Context, _ smtpserver.ResetReason, _ *smtpserver.ResetOptions) {
	s.reverse = ""
	s.recipients = nil
	s.open = false
}

func (s *sessionState) close(_ context.Context, _ *smtpserver.CloseOptions) {
	if s.closed {
		return
	}
	s.reverse = ""
	s.recipients = nil
	s.open = false
	s.closed = true
}

func cloneMessage(message Message) Message {
	message.Recipients = append([]string(nil), message.Recipients...)
	message.Data = append([]byte(nil), message.Data...)
	return message
}
