// pair connects to the OpenClaw gateway to register or verify a device.
// It exits 0 on success and non-zero on failure.
//
// Usage:
//
//	OPENCLAW_GATEWAY_URL=http://localhost:18789 go run ./test/integration/pair

//go:build ignore

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "pair: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	c, err := client.New(cfg)
	if err != nil {
		return fmt.Errorf("client: %w", err)
	}
	defer c.Close()

	// A freshly started gateway lazily compiles its protocol validators on
	// the first WS connect, which can exceed the SDK's default 10s handshake
	// deadline. Give the handshake generous headroom so the first connect
	// after startup does not spuriously time out.
	c.SetConnectTimeout(30 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// When OPENCLAW_BOOTSTRAP_TOKEN is set (the setup-code flow), run the
	// node→operator handoff first to establish the device and persist an
	// operator device token; the Connect below then authenticates with it.
	if bt := os.Getenv("OPENCLAW_BOOTSTRAP_TOKEN"); bt != "" {
		if err := c.Bootstrap(ctx, bt); err != nil {
			return fmt.Errorf("bootstrap: %w", err)
		}
	}

	if err := c.Connect(ctx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	fmt.Println("device paired successfully")
	return nil
}
