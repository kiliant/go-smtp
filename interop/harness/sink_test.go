package harness

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeSink lets WaitForMessage be tested without a real server: it returns
// nothing until arriveAfter calls, then returns a fixed message.
type fakeSink struct {
	mu          sync.Mutex
	calls       int
	arriveAfter int
	msg         Message
}

func (f *fakeSink) Fetch(ctx context.Context, recipient string) ([]Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls < f.arriveAfter {
		return nil, nil
	}
	return []Message{f.msg}, nil
}

func (f *fakeSink) Reset(ctx context.Context, recipient string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = 0
	return nil
}

func TestWaitForMessageEventualArrival(t *testing.T) {
	restore := setPollInterval(t, time.Millisecond)
	defer restore()

	sink := &fakeSink{arriveAfter: 3, msg: Message{Recipient: "a@example.test", Raw: []byte("hi")}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	got, err := WaitForMessage(ctx, sink, "a@example.test")
	if err != nil {
		t.Fatalf("WaitForMessage: %v", err)
	}
	if string(got.Raw) != "hi" {
		t.Errorf("Raw = %q, want %q", got.Raw, "hi")
	}
}

func TestWaitForMessageTimesOut(t *testing.T) {
	restore := setPollInterval(t, time.Millisecond)
	defer restore()

	sink := &fakeSink{arriveAfter: 1_000_000}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := WaitForMessage(ctx, sink, "a@example.test")
	if err == nil {
		t.Fatal("WaitForMessage should time out when the message never arrives")
	}
	if !errors.Is(err, ErrNoMessage) {
		t.Errorf("error = %v, want it to wrap ErrNoMessage", err)
	}
}

func setPollInterval(t *testing.T, d time.Duration) func() {
	t.Helper()
	prev := pollInterval
	pollInterval = d
	return func() { pollInterval = prev }
}
