package gosmtp

import (
	"context"
	"sync"

	"github.com/kiliant/go-smtp/interop/harness"
	"github.com/kiliant/go-smtp/smtpserver/memory"
)

// Sink adapts smtpserver/memory to the shared interop retrieval contract. A
// reset advances a per-recipient cursor instead of mutating the backend, so a
// scenario cannot delete evidence another concurrently executing recipient
// scenario still needs to inspect.
type Sink struct {
	source *memory.Sink

	mu     sync.Mutex
	cutoff map[string]int
}

func newSink(source *memory.Sink) *Sink {
	return &Sink{source: source, cutoff: make(map[string]int)}
}

// Fetch returns messages delivered at or after recipient's latest Reset.
func (s *Sink) Fetch(ctx context.Context, recipient string) ([]harness.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	cutoff := s.cutoff[recipient]
	s.mu.Unlock()

	stored := s.source.Messages()
	if cutoff > len(stored) {
		cutoff = len(stored)
	}
	messages := make([]harness.Message, 0, len(stored)-cutoff)
	for _, message := range stored[cutoff:] {
		if !hasRecipient(message.Recipients, recipient) {
			continue
		}
		messages = append(messages, harness.Message{
			Recipient: recipient,
			Raw:       append([]byte(nil), message.Data...),
		})
	}
	return messages, nil
}

// Reset hides messages currently visible to recipient from later Fetch calls.
func (s *Sink) Reset(ctx context.Context, recipient string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	cutoff := len(s.source.Messages())
	s.mu.Lock()
	s.cutoff[recipient] = cutoff
	s.mu.Unlock()
	return nil
}

func hasRecipient(recipients []string, want string) bool {
	for _, recipient := range recipients {
		if recipient == want {
			return true
		}
	}
	return false
}

var _ harness.Sink = (*Sink)(nil)
