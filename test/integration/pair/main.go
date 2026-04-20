// pair connects to the OpenClaw gateway to trigger device pairing.
// It blocks until the device is approved (or the timeout expires),
// then saves the device token and exits.
//
// Usage:
//
//	OPENCLAW_GATEWAY_URL=http://localhost:18789 go run ./test/integration/pair
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/outofcoffee/repclaw/internal/client"
	"github.com/outofcoffee/repclaw/internal/config"
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

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	fmt.Println("connecting to gateway (waiting for device approval)...")
	if err := c.Connect(ctx); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	fmt.Println("device paired successfully")
	return nil
}
