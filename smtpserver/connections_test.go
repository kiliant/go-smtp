package smtpserver

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"
)

func TestConnectionRegistryShutdownCancelsAndWaits(t *testing.T) {
	registry := newConnectionRegistry()
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	connectionContext, cancel := context.WithCancel(context.Background())
	if !registry.register(server, cancel) {
		t.Fatal("initial registration refused")
	}

	done := make(chan error, 1)
	go func() { done <- registry.shutdown(context.Background()) }()
	select {
	case <-connectionContext.Done():
	case <-time.After(time.Second):
		t.Fatal("connection context was not cancelled")
	}
	select {
	case err := <-done:
		t.Fatalf("shutdown returned before connection exit: %v", err)
	default:
	}
	registry.unregister(server)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if registry.register(client, func() {}) {
		t.Fatal("registration succeeded after shutdown began")
	}
}

func TestConnectionRegistryShutdownDeadlineForceCloses(t *testing.T) {
	registry := newConnectionRegistry()
	server, client := net.Pipe()
	defer client.Close()
	if !registry.register(server, func() {}) {
		t.Fatal("registration refused")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := registry.shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error = %v", err)
	}
	if _, err := server.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("write after forced close = %v", err)
	}
	registry.unregister(server)
}

func TestConnectionRegistriesAreInstanceScoped(t *testing.T) {
	first := newConnectionRegistry()
	second := newConnectionRegistry()
	firstServer, firstClient := net.Pipe()
	secondServer, secondClient := net.Pipe()
	defer firstClient.Close()
	defer secondClient.Close()
	if !first.register(firstServer, func() {}) || !second.register(secondServer, func() {}) {
		t.Fatal("registration refused")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = first.shutdown(ctx)
	if _, err := secondServer.Write(nil); err != nil {
		t.Fatalf("second registry connection affected by first shutdown: %v", err)
	}
	first.unregister(firstServer)
	second.unregister(secondServer)
	_ = secondServer.Close()
}
