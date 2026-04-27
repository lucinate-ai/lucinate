package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/lucinate-ai/lucinate/app"
	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/config"
	"github.com/lucinate-ai/lucinate/internal/version"
)

// promptAuthFix presents interactive options when the gateway rejects the
// stored device token. Returns true if a fix was applied and a retry should
// be attempted, false if the user chose to quit.
func promptAuthFix(c *client.Client, in io.Reader) bool {
	fmt.Fprintln(os.Stderr, "The stored device token was rejected by the gateway.")
	fmt.Fprintln(os.Stderr, "Choose an option:")
	fmt.Fprintln(os.Stderr, "  1) Clear stored token and retry  (recommended)")
	fmt.Fprintln(os.Stderr, "  2) Reset full identity and retry")
	fmt.Fprintln(os.Stderr, "  3) Quit")
	fmt.Fprint(os.Stderr, "\nChoice [1-3]: ")

	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		return false
	}
	switch strings.TrimSpace(scanner.Text()) {
	case "1", "":
		if err := c.ClearToken(); err != nil {
			fmt.Fprintf(os.Stderr, "error clearing token: %v\n", err)
			return false
		}
		fmt.Fprintln(os.Stderr, "Token cleared. Retrying...")
		return true
	case "2":
		if err := c.ResetIdentity(); err != nil {
			fmt.Fprintf(os.Stderr, "error resetting identity: %v\n", err)
			return false
		}
		fmt.Fprintln(os.Stderr, "Identity reset. Retrying...")
		return true
	default:
		return false
	}
}

// promptForToken asks the user to enter the gateway auth token. Some gateways
// require a pre-shared token for any connection, including fresh device
// registrations. Returns true if a token was stored and a retry should be
// attempted.
func promptForToken(c *client.Client, in io.Reader) bool {
	fmt.Fprintln(os.Stderr, "This gateway requires an auth token for connections.")
	fmt.Fprintln(os.Stderr, "Enter the gateway auth token (ask your gateway operator if needed):")
	fmt.Fprint(os.Stderr, "Token: ")

	scanner := bufio.NewScanner(in)
	if !scanner.Scan() {
		return false
	}
	token := strings.TrimSpace(scanner.Text())
	if token == "" {
		return false
	}
	if err := c.StoreToken(token); err != nil {
		fmt.Fprintf(os.Stderr, "error storing token: %v\n", err)
		return false
	}
	fmt.Fprintln(os.Stderr, "Token stored. Retrying...")
	return true
}

const connectTimeout = 15 * time.Second

// connectWithAuth connects to the gateway, handling auth errors interactively:
//
//  1. Initial connect attempt.
//  2. "token mismatch" → clear/reset → retry (works for gateways without auth.token).
//  3. "token missing" → prompt for gateway auth token → retry (needed when the
//     gateway requires a pre-shared token, either on first connect or after a
//     clear/reset).
func connectWithAuth(c *client.Client, in io.Reader) error {
	ctx, cancel := context.WithTimeout(context.Background(), connectTimeout)
	defer cancel()

	err := c.Connect(ctx)
	if err == nil {
		return nil
	}

	// Token mismatch: stored token is invalid — offer to clear/reset, then retry.
	if isTokenMismatch(err) {
		fmt.Fprintf(os.Stderr, "connection error: %v\n\n", err)
		if !promptAuthFix(c, in) {
			return err
		}
		ctx2, cancel2 := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel2()
		err = c.Connect(ctx2)
		if err == nil {
			return nil
		}
		// Fall through: the retry may have produced "token missing" if this
		// gateway requires a pre-shared auth token.
	}

	// Token missing: gateway requires an auth token that the client doesn't have.
	// This covers both first-time connections and post-clear/reset retries.
	if isTokenMissing(err) {
		fmt.Fprintf(os.Stderr, "connection error: %v\n\n", err)
		if !promptForToken(c, in) {
			return err
		}
		ctx3, cancel3 := context.WithTimeout(context.Background(), connectTimeout)
		defer cancel3()
		return c.Connect(ctx3)
	}

	return err
}

func isTokenMismatch(err error) bool {
	return err != nil && strings.Contains(err.Error(), "gateway token mismatch")
}

func isTokenMissing(err error) bool {
	return err != nil && strings.Contains(err.Error(), "gateway token missing")
}

func main() {
	fs := flag.NewFlagSet("lucinate", flag.ExitOnError)
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.BoolVar(showVersion, "v", false, "print version and exit")
	_ = fs.Parse(os.Args[1:])

	if *showVersion {
		fmt.Printf("lucinate %s\n", version.Version)
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	c, err := client.New(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "client error: %v\n", err)
		os.Exit(1)
	}
	defer c.Close()

	if err := connectWithAuth(c, os.Stdin); err != nil {
		fmt.Fprintf(os.Stderr, "connection error: %v\n", err)
		os.Exit(1)
	}

	if err := app.Run(context.Background(), app.RunOptions{Client: c}); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
