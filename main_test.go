package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	openaiBackend "github.com/lucinate-ai/lucinate/internal/backend/openai"
	"github.com/lucinate-ai/lucinate/internal/config"
)

// TestSecretAwareBackend_StoreAPIKeyPersistsToDisk verifies the
// disk-write side effect of the wrapper used in main.go: when the
// auth-modal resolution stores a key, it lands in the secrets file so
// the next launch picks it up via config.GetAPIKey without
// re-prompting. The connecting-view tests cover the TUI plumbing
// against an in-memory fake; this test covers the production wrapper
// end-to-end against a real OpenAI backend.
func TestSecretAwareBackend_StoreAPIKeyPersistsToDisk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()

	connID := "conn-fixture"
	inner, err := openaiBackend.New(openaiBackend.Options{
		ConnectionID: connID,
		BaseURL:      srv.URL + "/v1",
		HTTPClient:   srv.Client(),
	})
	if err != nil {
		t.Fatalf("openai.New: %v", err)
	}
	wrapper := &secretAwareBackend{Backend: inner, connID: connID}

	if got := config.GetAPIKey(connID); got != "" {
		t.Fatalf("precondition: expected no stored key, got %q", got)
	}

	if err := wrapper.StoreAPIKey("sk-from-modal"); err != nil {
		t.Fatalf("StoreAPIKey: %v", err)
	}

	// Disk persistence: a fresh load sees the key.
	if got := config.GetAPIKey(connID); got != "sk-from-modal" {
		t.Errorf("config.GetAPIKey(%q) = %q, want sk-from-modal", connID, got)
	}

	// In-memory propagation: a subsequent backend call sends the new
	// Authorization header.
	var seen string
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		_, _ = io.WriteString(w, `{"data":[]}`)
	})
	if _, err := wrapper.ModelsList(context.Background()); err != nil {
		t.Fatalf("ModelsList: %v", err)
	}
	if seen != "Bearer sk-from-modal" {
		t.Errorf("Authorization header = %q, want Bearer sk-from-modal", seen)
	}
}

// TestSecretAwareBackend_StoreAPIKeyClear verifies the empty-key path
// removes the entry from disk so users can fully unset a stored key
// (e.g. by submitting an empty value at the modal — currently the TUI
// does not expose this but the contract is worth pinning).
func TestSecretAwareBackend_StoreAPIKeyClear(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()

	connID := "conn-fixture"
	inner, err := openaiBackend.New(openaiBackend.Options{
		ConnectionID: connID,
		BaseURL:      srv.URL + "/v1",
		HTTPClient:   srv.Client(),
		APIKey:       "initial",
	})
	if err != nil {
		t.Fatalf("openai.New: %v", err)
	}
	wrapper := &secretAwareBackend{Backend: inner, connID: connID}

	if err := wrapper.StoreAPIKey("on-disk"); err != nil {
		t.Fatalf("StoreAPIKey: %v", err)
	}
	if got := config.GetAPIKey(connID); got != "on-disk" {
		t.Fatalf("precondition: GetAPIKey = %q", got)
	}

	if err := wrapper.StoreAPIKey(""); err != nil {
		t.Fatalf("StoreAPIKey(\"\"): %v", err)
	}
	if got := config.GetAPIKey(connID); got != "" {
		t.Errorf("expected key cleared from disk, got %q", got)
	}
}
