// Package cli implements the lucinate command-line entry point: argument
// parsing, subcommand dispatch (`send`, `chat`, `help`), and the bare
// flag set (`--version`) that falls through to the interactive TUI.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/lucinate-ai/lucinate/app"
	"github.com/lucinate-ai/lucinate/internal/version"
)

// errUsage is returned by a subcommand runner when the user asked for
// help via `-h` / `--help`. Treated as a clean exit by Run so the
// usage block the flag set already printed is not followed by a
// redundant "lucinate: flag: help requested" error line.
var errUsage = errors.New("usage")

// Run parses args and dispatches to the appropriate flow: the `help`
// command, the `send` / `chat` subcommands, or the bare TUI launch.
// It returns the process exit code; callers typically wrap it in
// os.Exit. stdout/stderr receive diagnostic output (help blocks,
// version, error lines); the TUI itself talks directly to the
// terminal regardless of these writers.
func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	// Top-level help: routed before subcommand dispatch so `help`
	// doesn't fall through to flag parsing and silently launch the TUI.
	if len(args) > 0 {
		switch args[0] {
		case "help", "-h", "-help", "--help":
			if err := runHelp(args[1:], stdout); err != nil {
				fmt.Fprintf(stderr, "lucinate: %v\n\n", err)
				printTopUsage(stderr)
				return 1
			}
			return 0
		}
	}

	// Subcommand dispatch. Subcommands are detected by the first
	// non-flag argument so the legacy `lucinate -version` invocation
	// keeps working.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "send":
			return finish(runSend(ctx, args[1:], stdout), stderr)
		case "chat":
			return finish(runChat(ctx, args[1:]), stderr)
		}
		// Unknown subcommand: fall through to flag parsing so a
		// mistyped subcommand surfaces a clear flag-package error
		// rather than silently launching the TUI.
	}

	fs := flag.NewFlagSet("lucinate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { printTopUsage(fs.Output()) }
	var showVersion bool
	fs.BoolVar(&showVersion, "version", false, "print version and exit")
	fs.BoolVar(&showVersion, "v", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		// flag pkg already wrote the error and usage block.
		return 2
	}

	if showVersion {
		fmt.Fprintf(stdout, "lucinate %s\n", version.Version)
		return 0
	}

	entry := app.ResolveEntryConnection()
	if err := app.Run(ctx, app.RunOptions{
		Store:          &entry.Store,
		Initial:        entry.Connection,
		BackendFactory: app.DefaultBackendFactory,
		OnConnectionsChanged: func(c app.Connections) {
			if err := app.SaveConnections(c); err != nil {
				fmt.Fprintf(stderr, "warning: failed to save connections: %v\n", err)
			}
		},
	}); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

// finish maps a subcommand runner's error to an exit code, treating
// errUsage as a clean exit (the runner already printed its usage).
func finish(err error, stderr io.Writer) int {
	if errors.Is(err, errUsage) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "lucinate: %v\n", err)
		return 1
	}
	return 0
}
