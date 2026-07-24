// probe verifies the Hermes backend wiring against a running Hermes
// gateway (typically the Docker container brought up by
// setup-hermes.sh). It builds the same backend the CLI would, dials
// the WebSocket, awaits the gateway.ready handshake, and round-trips
// session.create + session.list. Exits 0 on success and non-zero on
// failure so setup-hermes.sh can fail fast before the full
// integration suite runs.
//
// Usage:
//
//	LUCINATE_HERMES_BASE_URL=http://localhost:19119 \
//	  LUCINATE_HERMES_TOKEN=lucinate \
//	  go run ./test/integration/hermes/probe

//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	hermesBackend "github.com/lucinate-ai/lucinate/internal/backend/hermes"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "probe: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	baseURL := os.Getenv("LUCINATE_HERMES_BASE_URL")
	if baseURL == "" {
		return fmt.Errorf("LUCINATE_HERMES_BASE_URL is not set")
	}
	token := os.Getenv("LUCINATE_HERMES_TOKEN")

	b, err := hermesBackend.New(hermesBackend.Options{
		ConnectionID:   "probe",
		BaseURL:        baseURL,
		APIKey:         token,
		ConnectTimeout: 20 * time.Second,
	})
	if err != nil {
		return fmt.Errorf("backend: %w", err)
	}
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := b.Connect(ctx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	key, err := b.CreateSession(ctx, "hermes", "")
	if err != nil {
		return fmt.Errorf("session.create: %w", err)
	}

	raw, err := b.SessionsList(ctx, "hermes")
	if err != nil {
		return fmt.Errorf("session.list: %w", err)
	}
	var list struct {
		Sessions []json.RawMessage `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &list); err != nil {
		return fmt.Errorf("decode session list: %w", err)
	}

	fmt.Printf("backend probe ok: gateway handshake + session round-trip (session %s, %d listed) at %s\n",
		key, len(list.Sessions), baseURL)
	return nil
}
