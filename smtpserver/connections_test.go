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
	if _, err := server.Write([]byte("x")); err == nil {
		t.Fatal("write succeeded after forced close")
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
	read := make(chan error, 1)
	go func() {
		buffer := make([]byte, 1)
		_, err := secondClient.Read(buffer)
		if err == nil && buffer[0] != 'x' {
			err = errors.New("unexpected byte read from second connection")
		}
		read <- err
	}()
	if _, err := secondServer.Write([]byte("x")); err != nil {
		t.Fatalf("second registry connection affected by first shutdown: %v", err)
	}
	if err := <-read; err != nil {
		t.Fatalf("read from second registry connection: %v", err)
	}
	first.unregister(firstServer)
	second.unregister(secondServer)
	_ = secondServer.Close()
}

func TestConnectionRegistryEnforcesInstanceLimit(t *testing.T) {
	registry := newConnectionRegistry(1)
	firstServer, firstClient := net.Pipe()
	secondServer, secondClient := net.Pipe()
	defer firstServer.Close()
	defer firstClient.Close()
	defer secondServer.Close()
	defer secondClient.Close()
	if !registry.register(firstServer, func() {}) {
		t.Fatal("first registration refused")
	}
	if registry.register(secondServer, func() {}) {
		t.Fatal("second registration exceeded the instance limit")
	}
	registry.unregister(firstServer)
	if !registry.register(secondServer, func() {}) {
		t.Fatal("registration remained blocked after release")
	}
	registry.unregister(secondServer)
}
