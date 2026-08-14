package harness

import (
	"context"
	"errors"
	"testing"
)

type runtimeTestSink struct{}

func (runtimeTestSink) Fetch(context.Context, string) ([]Message, error) { return nil, nil }
func (runtimeTestSink) Reset(context.Context, string) error              { return nil }

func TestStartProfileUsesInProcessRuntimeWithoutPodman(t *testing.T) {
	ctx := context.Background()
	wantSink := runtimeTestSink{}
	started := false
	stopped := false
	profile := Profile{
		Name: "local",
		Start: func(context.Context) (*RuntimeConfig, error) {
			started = true
			return &RuntimeConfig{
				Addresses: map[int]string{2525: "127.0.0.1:40252"},
				Sink:      wantSink,
				Stop: func(context.Context) error {
					stopped = true
					return nil
				},
				Logs: func(context.Context) (string, error) { return "local diagnostics", nil },
			}, nil
		},
		Ports: []Port{{Container: 2525, Kind: "smtp"}},
	}
	handle, err := StartProfile(ctx, profile)
	if err != nil {
		t.Fatal(err)
	}
	if !started {
		t.Fatal("in-process Start callback was not called")
	}
	if addr, ok := handle.HostAddr(2525); !ok || addr != "127.0.0.1:40252" {
		t.Fatalf("HostAddr = %q, %v", addr, ok)
	}
	if port, ok := handle.HostPort(2525); !ok || port != 40252 {
		t.Fatalf("HostPort = %d, %v", port, ok)
	}
	sink, err := handle.NewSink(ctx)
	if err != nil || sink != wantSink {
		t.Fatalf("NewSink = %#v, %v", sink, err)
	}
	if logs, err := handle.Logs(ctx); err != nil || logs != "local diagnostics" {
		t.Fatalf("Logs = %q, %v", logs, err)
	}
	if _, err := handle.Exec(ctx, "true"); err == nil {
		t.Fatal("Exec on an in-process runtime succeeded")
	}
	if err := handle.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if !stopped {
		t.Fatal("in-process Stop callback was not called")
	}
}

func TestStartProfileRejectsIncompleteInProcessRuntimeAndStopsIt(t *testing.T) {
	stopped := false
	profile := Profile{
		Name:  "incomplete",
		Ports: []Port{{Container: 25, Kind: "smtp"}},
		Start: func(context.Context) (*RuntimeConfig, error) {
			return &RuntimeConfig{
				Addresses: map[int]string{25: "not-an-address"},
				Stop: func(context.Context) error {
					stopped = true
					return nil
				},
			}, nil
		},
	}
	if _, err := StartProfile(context.Background(), profile); err == nil {
		t.Fatal("StartProfile accepted an invalid in-process address")
	}
	if !stopped {
		t.Fatal("invalid in-process runtime was not stopped")
	}
}

func TestStartProfileReportsStartFailure(t *testing.T) {
	want := errors.New("start failed")
	profile := Profile{
		Name: "failed",
		Start: func(context.Context) (*RuntimeConfig, error) {
			return nil, want
		},
	}
	_, err := StartProfile(context.Background(), profile)
	if !errors.Is(err, want) {
		t.Fatalf("StartProfile error = %v, want wrapped %v", err, want)
	}
}

func TestRuntimeHandleStopIsUnconditionallyIdempotent(t *testing.T) {
	want := errors.New("stop failed")
	calls := 0
	profile := Profile{
		Name:  "stop-once",
		Ports: []Port{{Container: 25, Kind: "smtp"}},
		Start: func(context.Context) (*RuntimeConfig, error) {
			return &RuntimeConfig{
				Addresses: map[int]string{25: "127.0.0.1:40253"},
				Stop: func(context.Context) error {
					calls++
					return want
				},
			}, nil
		},
	}
	handle, err := StartProfile(context.Background(), profile)
	if err != nil {
		t.Fatal(err)
	}
	first := handle.Stop(context.Background())
	second := handle.Stop(context.Background())
	if calls != 1 {
		t.Fatalf("Stop callback calls = %d, want 1", calls)
	}
	if !errors.Is(first, want) || first != second {
		t.Fatalf("Stop results = (%v, %v), want the same cached %v", first, second, want)
	}
}
