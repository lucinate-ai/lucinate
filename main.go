package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/lucinate-ai/lucinate/app"
	"github.com/lucinate-ai/lucinate/internal/version"
)

func main() {
	fs := flag.NewFlagSet("lucinate", flag.ExitOnError)
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.BoolVar(showVersion, "v", false, "print version and exit")
	_ = fs.Parse(os.Args[1:])

	if *showVersion {
		fmt.Printf("lucinate %s\n", version.Version)
		return
	}

	entry := app.ResolveEntryConnection()

	if err := app.Run(context.Background(), app.RunOptions{
		Store:          &entry.Store,
		Initial:        entry.Connection,
		BackendFactory: app.DefaultBackendFactory,
		OnConnectionsChanged: func(c app.Connections) {
			if err := app.SaveConnections(c); err != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to save connections: %v\n", err)
			}
		},
	}); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
