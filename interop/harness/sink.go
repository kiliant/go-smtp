package harness

import (
	"context"
	"errors"
)

// ErrNoMessage is returned by a Sink when polling exhausted its timeout
// without the expected message appearing.
var ErrNoMessage = errors.New("harness: message not found in sink before timeout")

// Message is one delivered message as a sink retrieved it.
type Message struct {
	// Recipient is the mailbox the message was fetched from.
	Recipient string
	// Raw is the full RFC 5322 message exactly as the sink read it,
	// including any trace headers ("Received:", etc.) the server
	// prepended. Callers comparing against a submitted fixture must strip
	// those before comparing bytes.
	Raw []byte
}

// Sink retrieves mail actually delivered to a mailbox on a running server —
// the read side of the round trip that proves the client's write side
// (dot-stuffing, line-length handling, 8-bit/binary transparency) was not
// silently mangled in transit. Every server profile supplies one
// implementation, over whatever retrieval mechanism that server supports: an
// HTTP API (Mailpit, GreenMail) or a maildir read via "podman exec"
// (Postfix, Exim, Dovecot, maddy).
//
// Implementations must be safe for the harness to call repeatedly (e.g. while
// polling) and must not retain state across a container's lifetime.
type Sink interface {
	// Fetch retrieves every message currently visible for recipient. It
	// does not poll; callers wanting to wait for a message that has not
	// arrived yet use WaitForMessage.
	Fetch(ctx context.Context, recipient string) ([]Message, error)

	// Reset clears any messages the sink has accumulated for recipient, so
	// scenarios do not observe mail left over from an earlier one in the
	// same container.
	Reset(ctx context.Context, recipient string) error
}

// WaitForMessage polls sink until it returns at least one message for
// recipient or ctx is done, and returns the first one. Callers supply a
// context carrying the harness's SinkTimeout rather than this function
// sleeping on a fixed schedule, so a fast server resolves immediately and a
// slow one still respects the caller's bound.
func WaitForMessage(ctx context.Context, sink Sink, recipient string) (Message, error) {
	for {
		msgs, err := sink.Fetch(ctx, recipient)
		if err != nil {
			return Message{}, err
		}
		if len(msgs) > 0 {
			return msgs[0], nil
		}
		select {
		case <-ctx.Done():
			return Message{}, errors.Join(ErrNoMessage, ctx.Err())
		case <-pollTick():
		}
	}
}

// pollTick is a variable so tests can shrink the poll interval without
// depending on wall-clock time in the harness's own unit tests.
var pollTick = defaultPollTick
