package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lucinate-ai/lucinate/app"
	"github.com/lucinate-ai/lucinate/internal/config"
)

// runAsk implements `lucinate ask`, a thin alias for `lucinate send`
// (see runSend in send.go) that pre-fills the connection / agent /
// session / detach flags from the user's saved Ask defaults. Those
// defaults are edited in the TUI under /settings ▸ "Ask command
// defaults"; an empty default leaves the flag unset so the user can
// supply it on the command line. Any flag passed explicitly overrides
// the saved default.
//
// KEEP IN SYNC with runSend: message handling and the dispatch into
// app.Send are intentionally identical — the only difference is that
// `ask` seeds its flag defaults from config.AskDefaults rather than
// requiring them every time.
func runAsk(ctx context.Context, args []string, stdout io.Writer) error {
	defaults := config.LoadPreferences().Ask
	var (
		connection, agent, session string
		detach                     bool
	)
	fs := newAskFlagSet(&connection, &agent, &session, &detach, defaults)
	fs.Usage = func() { printAskUsage(fs.Output()) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errUsage
		}
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return errors.New("ask: missing message text")
	}
	if strings.TrimSpace(connection) == "" {
		return errors.New("ask: no connection configured — set one in the TUI under /settings ▸ Ask command defaults, or pass --connection")
	}
	if strings.TrimSpace(agent) == "" {
		return errors.New("ask: no agent configured — set one in the TUI under /settings ▸ Ask command defaults, or pass --agent")
	}
	message := strings.Join(rest, " ")
	out := stdout
	if out == nil {
		out = os.Stdout
	}
	return app.Send(ctx, app.SendOptions{
		Connection:     connection,
		Agent:          agent,
		Session:        session,
		Message:        message,
		Detach:         detach,
		Out:            out,
		BackendFactory: app.DefaultBackendFactory,
	})
}

// newAskFlagSet mirrors newSendFlagSet but seeds each flag's default
// value from the saved Ask defaults, so an omitted flag falls back to
// the configured value instead of the empty string.
func newAskFlagSet(connection, agent, session *string, detach *bool, d config.AskDefaults) *flag.FlagSet {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	fs.StringVar(connection, "connection", d.Connection, "saved connection name or ID (defaults to the configured Ask connection)")
	fs.StringVar(connection, "c", d.Connection, "short for --connection")
	fs.StringVar(agent, "agent", d.Agent, "agent name or ID within the connection (defaults to the configured Ask agent)")
	fs.StringVar(agent, "a", d.Agent, "short for --agent")
	fs.StringVar(session, "session", d.Session, "session key (defaults to the configured Ask session)")
	fs.StringVar(session, "s", d.Session, "short for --session")
	fs.BoolVar(detach, "detach", d.Detach, "dispatch the message and exit without waiting for a reply")
	fs.BoolVar(detach, "d", d.Detach, "short for --detach")
	return fs
}

// printAskUsage writes the `lucinate ask` usage block.
func printAskUsage(out io.Writer) {
	fs := newAskFlagSet(new(string), new(string), new(string), new(bool), config.AskDefaults{})
	fs.SetOutput(out)
	fmt.Fprintln(out, "Usage: lucinate ask [(--connection|-c) <name>] [(--agent|-a) <name>] [(--session|-s) <key>] [--detach|-d] <message...>")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Sends a single chat message, just like `lucinate send`, but pre-fills the")
	fmt.Fprintln(out, "connection, agent, session, and detach options from the saved Ask defaults")
	fmt.Fprintln(out, "(edit them in the TUI under /settings ▸ Ask command defaults). Any flag passed")
	fmt.Fprintln(out, "explicitly overrides the saved default.")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Flags:")
	fs.PrintDefaults()
}
