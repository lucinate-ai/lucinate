package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/lucinate-ai/lucinate/app"
	"github.com/lucinate-ai/lucinate/internal/version"
)

// errSendUsage is returned by runSend when the user asked for help via
// `-h` / `--help`. Treated as a clean exit by main so the usage block
// the flag set already printed is not followed by a redundant
// "lucinate: flag: help requested" error line.
var errSendUsage = errors.New("usage")

// errChatUsage mirrors errSendUsage for the `chat` subcommand.
var errChatUsage = errors.New("usage")

func main() {
	args := os.Args[1:]

	// Subcommand dispatch. The "send" subcommand is the one-shot CLI
	// entry that bypasses the TUI and routes a single message into a
	// stored connection / agent / session, optionally waiting for the
	// first complete reply. Subcommands are detected by the first
	// non-flag argument so the legacy `lucinate -version` invocation
	// keeps working.
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "send":
			err := runSend(args[1:])
			if errors.Is(err, errSendUsage) {
				return
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "lucinate: %v\n", err)
				os.Exit(1)
			}
			return
		case "chat":
			err := runChat(args[1:])
			if errors.Is(err, errChatUsage) {
				return
			}
			if err != nil {
				fmt.Fprintf(os.Stderr, "lucinate: %v\n", err)
				os.Exit(1)
			}
			return
		}
		// Unknown subcommand: fall through to flag parsing so a
		// mistyped subcommand surfaces a clear flag-package error
		// rather than silently launching the TUI.
	}

	fs := flag.NewFlagSet("lucinate", flag.ExitOnError)
	showVersion := fs.Bool("version", false, "print version and exit")
	fs.BoolVar(showVersion, "v", false, "print version and exit")
	_ = fs.Parse(args)

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

// runSend parses the `lucinate send` flag set and dispatches into
// app.Send. The flag set deliberately stops at the first positional
// argument so the message body — which may contain text that looks
// like flags — is taken verbatim from the remaining args. Use `--`
// before a message that starts with a dash, the standard Unix escape.
func runSend(args []string) error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	var (
		connection string
		agent      string
		session    string
		detach     bool
	)
	fs.StringVar(&connection, "connection", "", "saved connection name or ID (required)")
	fs.StringVar(&agent, "agent", "", "agent name or ID within the connection (required)")
	fs.StringVar(&session, "session", "", "session key (defaults to the agent's main session)")
	fs.BoolVar(&detach, "detach", false, "dispatch the message and exit without waiting for a reply")
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintln(out, "Usage: lucinate send --connection <name> --agent <name> [--session <key>] [--detach] <message...>")
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Sends a single chat message through a stored connection and prints the")
		fmt.Fprintln(out, "assistant's first complete reply on stdout. With --detach the call returns")
		fmt.Fprintln(out, "as soon as the gateway has accepted the turn.")
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errSendUsage
		}
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return errors.New("send: missing message text")
	}
	message := strings.Join(rest, " ")
	return app.Send(context.Background(), app.SendOptions{
		Connection:     connection,
		Agent:          agent,
		Session:        session,
		Message:        message,
		Detach:         detach,
		Out:            os.Stdout,
		BackendFactory: app.DefaultBackendFactory,
	})
}

// runChat parses the `lucinate chat` flag set and dispatches into
// app.Chat. Unlike `send`, every flag is optional and so is the
// positional message — `lucinate chat` with no args is equivalent
// to bare `lucinate`. As with `send`, the flag set stops at the
// first positional argument so message text containing dashes is
// taken verbatim; use `--` to disambiguate a leading dash.
func runChat(args []string) error {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	var (
		connection string
		agent      string
		session    string
	)
	fs.StringVar(&connection, "connection", "", "saved connection name or ID (defaults to the auto-pick)")
	fs.StringVar(&agent, "agent", "", "agent name or ID to auto-select after connecting")
	fs.StringVar(&session, "session", "", "session key to open (defaults to the agent's main session)")
	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintln(out, "Usage: lucinate chat [--connection <name>] [--agent <name>] [--session <key>] [<message...>]")
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Launches the TUI pre-navigated to the named connection / agent / session,")
		fmt.Fprintln(out, "optionally auto-submitting the supplied message as the first turn. Any")
		fmt.Fprintln(out, "unset flag falls back to the same default the bare `lucinate` invocation")
		fmt.Fprintln(out, "uses (single-connection auto-pick, single-agent auto-pick, agent's main")
		fmt.Fprintln(out, "session). Unlike `send`, this stays in the TUI for follow-up interaction.")
		fmt.Fprintln(out, "")
		fmt.Fprintln(out, "Flags:")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errChatUsage
		}
		return err
	}
	rest := fs.Args()
	message := strings.Join(rest, " ")
	return app.Chat(context.Background(), app.ChatOptions{
		Connection:     connection,
		Agent:          agent,
		Session:        session,
		Message:        message,
		BackendFactory: app.DefaultBackendFactory,
	})
}
