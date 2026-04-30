// probe verifies the Hermes backend wiring against a running Hermes
// API server (typically the Docker container brought up by
// setup-hermes.sh). It builds the same backend the CLI would, calls
// Connect, lists models, prints the count. Exits 0 on success and
// non-zero on failure so setup-hermes.sh can fail fast before the
// full integration suite runs.
//
// Usage:
//
//	LUCINATE_HERMES_BASE_URL=http://localhost:8642/v1 \
//	  LUCINATE_HERMES_API_KEY=lucinate-integration-test \
//	  go run ./test/integration/hermes/probe

//go:build ignore

package main

import (
	"context"
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
	apiKey := os.Getenv("LUCINATE_HERMES_API_KEY")
	defaultModel := os.Getenv("LUCINATE_HERMES_DEFAULT_MODEL")

	b, err := hermesBackend.New(hermesBackend.Options{
		ConnectionID: "probe",
		BaseURL:      baseURL,
		APIKey:       apiKey,
		DefaultModel: defaultModel,
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

	models, err := b.ModelsList(ctx)
	if err != nil {
		return fmt.Errorf("models list: %w", err)
	}
	fmt.Printf("backend probe ok: %d model(s) discovered at %s\n", len(models.Models), baseURL)
	return nil
}
