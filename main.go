package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/config"
	"github.com/lucinate-ai/lucinate/internal/tui"
	"github.com/lucinate-ai/lucinate/internal/version"
)

func main() {
	fs := flag.NewFlagSet("lucinate", flag.ExitOnError)
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.BoolVar(showVersion, "v", false, "print version and exit")
	historyLimit := fs.Int("history-limit", 0, "number of messages to load per session (overrides preference)")
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
		fmt.Fprintf(os.Stderr, "connection error: %v\n", err)
		os.Exit(1)
	}

	var opts []tui.AppOption
	if *historyLimit > 0 {
		opts = append(opts, tui.WithHistoryLimit(*historyLimit))
	}
	app := tui.NewApp(c, opts...)
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
