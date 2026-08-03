package greenmail

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResetUsesInstanceMailPurge(t *testing.T) {
	var method, path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := (&sink{baseURL: server.URL}).Reset(context.Background(), "interop@example.test"); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if method != http.MethodPost || path != "/api/mail/purge" {
		t.Fatalf("request = %s %s, want POST /api/mail/purge", method, path)
	}
}

func TestResetRejectsFailedPurge(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "purge failed", http.StatusInternalServerError)
	}))
	defer server.Close()

	err := (&sink{baseURL: server.URL}).Reset(context.Background(), "interop@example.test")
	if err == nil || !strings.Contains(err.Error(), "500 Internal Server Error") || !strings.Contains(err.Error(), "purge failed") {
		t.Fatalf("Reset error = %v, want status and bounded response body", err)
	}
}
