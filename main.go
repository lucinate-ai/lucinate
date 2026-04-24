package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/config"
	"github.com/lucinate-ai/lucinate/internal/tui"
	"github.com/lucinate-ai/lucinate/internal/version"
)

// promptAuthFix presents interactive options when the gateway rejects the
// stored device token. Returns true if a fix was applied and a retry should
// be attempted, false if the user chose to quit.
func promptAuthFix(c *client.Client) bool {
	fmt.Fprintln(os.Stderr, "The stored device token was rejected by the gateway.")
	fmt.Fprintln(os.Stderr, "Choose an option:")
	fmt.Fprintln(os.Stderr, "  1) Clear stored token and retry  (recommended)")
	fmt.Fprintln(os.Stderr, "  2) Reset full identity and retry")
	fmt.Fprintln(os.Stderr, "  3) Quit")
	fmt.Fprint(os.Stderr, "\nChoice [1-3]: ")

	scanner := bufio.NewScanner(os.Stdin)
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

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := c.Connect(ctx); err != nil {
		if !strings.Contains(err.Error(), "gateway token mismatch") {
			fmt.Fprintf(os.Stderr, "connection error: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "connection error: %v\n\n", err)
		if !promptAuthFix(c) {
			os.Exit(1)
		}
		ctx2, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel2()
		if err := c.Connect(ctx2); err != nil {
			fmt.Fprintf(os.Stderr, "connection error: %v\n", err)
			os.Exit(1)
		}
	}

	app := tui.NewApp(c)
	p := tea.NewProgram(app)

	// Pump gateway events into the bubbletea program from a dedicated goroutine.
	// This is more reliable than a cmd-chain that must be re-issued after each event.
	go func() {
		for ev := range c.Events() {
			p.Send(tui.GatewayEventMsg(ev))
		}
	}()

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
