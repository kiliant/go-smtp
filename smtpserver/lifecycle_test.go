package smtpserver

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/kiliant/go-smtp"
)

func TestSessionLifecycleResetPrecedesReplacingMail(t *testing.T) {
	var events []string
	session := lifecycleTestSession(&events)
	lifecycle := newSessionLifecycle(session)
	if err := lifecycle.mail(context.Background(), "first@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.mail(context.Background(), "second@example.test", nil, nil); err != nil {
		t.Fatal(err)
	}
	want := []string{"mail:first@example.test", "reset:1", "mail:second@example.test"}
	if fmt.Sprint(events) != fmt.Sprint(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
}

func TestSessionLifecycleCloseOrderingAndIdempotence(t *testing.T) {
	ctx := context.Background()
	t.Run("open transaction", func(t *testing.T) {
		var events []string
		lifecycle := newSessionLifecycle(lifecycleTestSession(&events))
		if err := lifecycle.mail(ctx, "sender@example.test", nil, nil); err != nil {
			t.Fatal(err)
		}
		lifecycle.close(ctx)
		lifecycle.close(ctx)
		want := []string{"mail:sender@example.test", "reset:5", "close"}
		if fmt.Sprint(events) != fmt.Sprint(want) {
			t.Fatalf("events = %v, want %v", events, want)
		}
	})
	t.Run("already completed", func(t *testing.T) {
		var events []string
		lifecycle := newSessionLifecycle(lifecycleTestSession(&events))
		if err := lifecycle.mail(ctx, "sender@example.test", nil, nil); err != nil {
			t.Fatal(err)
		}
		lifecycle.reset(ctx, ResetCompleted)
		lifecycle.close(ctx)
		want := []string{"mail:sender@example.test", "reset:2", "close"}
		if fmt.Sprint(events) != fmt.Sprint(want) {
			t.Fatalf("events = %v, want %v", events, want)
		}
	})
}

func TestSessionLifecycleAllResetReasonsReachBackend(t *testing.T) {
	for reason := ResetExplicit; reason <= ResetSessionEnd; reason++ {
		var events []string
		lifecycle := newSessionLifecycle(lifecycleTestSession(&events))
		if err := lifecycle.mail(context.Background(), "sender@example.test", nil, nil); err != nil {
			t.Fatal(err)
		}
		lifecycle.reset(context.Background(), reason)
		want := fmt.Sprintf("reset:%d", reason)
		if events[len(events)-1] != want {
			t.Fatalf("reason %d events = %v", reason, events)
		}
	}
}

func TestSessionLifecycleReleasesSpoolBeforeBackendResetPanic(t *testing.T) {
	opts := testSpoolOptions(t)
	manager, err := newSpoolManager(opts)
	if err != nil {
		t.Fatal(err)
	}
	spool, err := manager.newSpool()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(spool, "message"); err != nil {
		t.Fatal(err)
	}
	session := lifecycleTestSession(nil)
	session.Reset = func(context.Context, ResetReason, *ResetOptions) { panic("backend panic") }
	lifecycle := newSessionLifecycle(session)
	lifecycle.transactionOpen = true
	lifecycle.attachSpool(spool)
	func() {
		defer func() { _ = recover() }()
		lifecycle.reset(context.Background(), ResetFailed)
	}()
	if total, memory, concurrent := manager.usage(); total != 0 || memory != 0 || concurrent != 0 {
		t.Fatalf("spool usage after reset panic = (%d, %d, %d)", total, memory, concurrent)
	}
}

func lifecycleTestSession(events *[]string) *Session {
	record := func(event string) {
		if events != nil {
			*events = append(*events, event)
		}
	}
	return &Session{
		Mail: func(_ context.Context, reverse string, _ *smtp.MailOptions, _ *MailOptions) error {
			record("mail:" + reverse)
			return nil
		},
		Rcpt: func(context.Context, string, *smtp.RcptOptions, *RcptOptions) error { return nil },
		Data: func(context.Context, io.Reader, *DataOptions) (smtp.DataResult, error) { return nil, nil },
		Reset: func(_ context.Context, reason ResetReason, _ *ResetOptions) {
			record(fmt.Sprintf("reset:%d", reason))
		},
		Close: func(context.Context, *CloseOptions) { record("close") },
	}
}
