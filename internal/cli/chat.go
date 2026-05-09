package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/lucinate-ai/lucinate/app"
)

// runChat parses the `lucinate chat` flag set and dispatches into
// app.Chat. Unlike `send`, every flag is optional and so is the
// positional message — `lucinate chat` with no args is equivalent
// to bare `lucinate`. As with `send`, the flag set stops at the
// first positional argument so message text containing dashes is
// taken verbatim; use `--` to disambiguate a leading dash.
func runChat(ctx context.Context, args []string) error {
	var connection, agent, session string
	fs := newChatFlagSet(&connection, &agent, &session)
	fs.Usage = func() { printChatUsage(fs.Output()) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errUsage
		}
		return err
	}
	rest := fs.Args()
	message := strings.Join(rest, " ")
	return app.Chat(ctx, app.ChatOptions{
		Connection:     connection,
		Agent:          agent,
		Session:        session,
		Message:        message,
		BackendFactory: app.DefaultBackendFactory,
	})
}

func newChatFlagSet(connection, agent, session *string) *flag.FlagSet {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	fs.StringVar(connection, "connection", "", "saved connection name or ID (defaults to the auto-pick)")
	fs.StringVar(connection, "c", "", "short for --connection")
	fs.StringVar(agent, "agent", "", "agent name or ID to auto-select after connecting")
	fs.StringVar(agent, "a", "", "short for --agent")
	fs.StringVar(session, "session", "", "session key to open (defaults to the agent's main session)")
	fs.StringVar(session, "s", "", "short for --session")
	return fs
}

// printChatUsage writes the `lucinate chat` usage block.
func printChatUsage(out io.Writer) {
	var connection, agent, session string
	fs := newChatFlagSet(&connection, &agent, &session)
	fs.SetOutput(out)
	fmt.Fprintln(out, "Usage: lucinate chat [(--connection|-c) <name>] [(--agent|-a) <name>] [(--session|-s) <key>] [<message...>]")
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
