package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/lucinate-ai/lucinate/app"
)

// runSend parses the `lucinate send` flag set and dispatches into
// app.Send. The flag set deliberately stops at the first positional
// argument so the message body — which may contain text that looks
// like flags — is taken verbatim from the remaining args. Use `--`
// before a message that starts with a dash, the standard Unix escape.
//
// KEEP IN SYNC with runAsk (ask.go): `ask` is an alias for `send` that
// pre-fills these same flags from the saved Ask defaults, so any change
// to the flag set, message handling, or app.Send dispatch here should be
// mirrored there (and any new send field should become customisable in
// config.AskDefaults, except per-invocation bounds like --timeout).
func runSend(ctx context.Context, args []string, stdout io.Writer) error {
	var (
		connection, agent, session string
		detach                     bool
		timeout                    time.Duration
	)
	fs := newSendFlagSet(&connection, &agent, &session, &detach, &timeout)
	fs.Usage = func() { printSendUsage(fs.Output()) }
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errUsage
		}
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return errors.New("send: missing message text")
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
		Timeout:        timeout,
		Out:            out,
		BackendFactory: app.DefaultBackendFactory,
	})
}

// defaultSendTimeout bounds a one-shot reply wait. The streaming backends
// also fail a run that goes quiet (see the OpenAI/Hermes idle guards); this
// is the outer bound for the whole call, so a wedged process still exits.
const defaultSendTimeout = 10 * time.Minute

func newSendFlagSet(connection, agent, session *string, detach *bool, timeout *time.Duration) *flag.FlagSet {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	fs.StringVar(connection, "connection", "", "saved connection name or ID (required)")
	fs.StringVar(connection, "c", "", "short for --connection")
	fs.StringVar(agent, "agent", "", "agent name or ID within the connection (required)")
	fs.StringVar(agent, "a", "", "short for --agent")
	fs.StringVar(session, "session", "", "session key (defaults to the agent's main session)")
	fs.StringVar(session, "s", "", "short for --session")
	fs.BoolVar(detach, "detach", false, "dispatch the message and exit without waiting for a reply")
	fs.BoolVar(detach, "d", false, "short for --detach")
	fs.DurationVar(timeout, "timeout", defaultSendTimeout, "give up waiting for a reply after this long (0 waits forever)")
	return fs
}

// printSendUsage writes the `lucinate send` usage block.
func printSendUsage(out io.Writer) {
	var (
		connection, agent, session string
		detach                     bool
		timeout                    time.Duration
	)
	fs := newSendFlagSet(&connection, &agent, &session, &detach, &timeout)
	fs.SetOutput(out)
	fmt.Fprintln(out, "Usage: lucinate send (--connection|-c) <name> (--agent|-a) <name> [(--session|-s) <key>] [--detach|-d] [--timeout <duration>] <message...>")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Sends a single chat message through a stored connection and prints the")
	fmt.Fprintln(out, "assistant's first complete reply on stdout. With --detach the call returns")
	fmt.Fprintln(out, "as soon as the gateway has accepted the turn. --timeout (default 10m, 0")
	fmt.Fprintln(out, "waits forever) bounds how long the reply is awaited.")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Flags:")
	fs.PrintDefaults()
}
