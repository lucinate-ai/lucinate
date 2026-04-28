package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/lucinate-ai/lucinate/app"
	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/config"
	"github.com/lucinate-ai/lucinate/internal/version"
)

// clientFactory builds an unconnected gateway client for a stored
// connection. The TUI calls Connect itself so it can route auth
// errors into modal recovery flows.
func clientFactory(conn *config.Connection) (*client.Client, error) {
	if conn == nil {
		return nil, fmt.Errorf("connection is nil")
	}
	cfg, err := config.FromConnection(conn)
	if err != nil {
		return nil, err
	}
	return client.New(cfg)
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

	entry := config.ResolveEntryConnection()

	if err := app.Run(context.Background(), app.RunOptions{
		Store:         &entry.Store,
		Initial:       entry.Connection,
		ClientFactory: clientFactory,
		OnConnectionsChanged: func(c config.Connections) {
			if err := config.SaveConnections(c); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to save connections: %v\n", err)
			}
		},
	}); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
