package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// ChatOptions configures a `lucinate chat` invocation.
//
// Chat is the programmatic entry point behind the `chat` subcommand:
// it launches the same TUI the bare `lucinate` invocation does, but
// with optional overrides that drive the connection / agent / session
// pickers past themselves and (when Message is set) auto-submit a
// first turn. Every override is optional — an unset field falls back
// to the same default the TUI uses interactively (single-connection
// auto-pick, single-agent auto-pick, the agent's main session).
type ChatOptions struct {
	// Connection identifies the saved connection to start with,
	// matched first by ID and then case-insensitively by Name. When
	// empty Chat falls back to ResolveEntryConnection() — env vars,
	// stored DefaultID, single-entry auto-pick, and finally the
	// picker. An explicit value that doesn't match any stored
	// connection is a hard error so a typo can't silently strand
	// the user on a stranger's default.
	Connection string

	// Agent, when non-empty, instructs the TUI's agent picker to
	// auto-select the named agent (matched ID-first, then
	// case-insensitive Name) the first time its list loads. A miss
	// surfaces as an error banner on the picker rather than silently
	// falling through to the single-agent convenience auto-pick or
	// the first-in-list. Empty leaves the picker behaving normally.
	Agent string

	// Session, when non-empty, overrides the session key the TUI
	// passes to the backend's CreateSession call on the first agent
	// selection. The backend is responsible for resolving the key
	// into a wire-level session identifier (creating a new one when
	// necessary). Empty preserves the existing default ("main", or
	// the connection's MainKey for its default agent).
	Session string

	// Message, when non-empty, is queued as the first user turn in
	// the chat view and submitted automatically once the session's
	// history has loaded. Equivalent to typing the same text into
	// the textarea and pressing Enter — including any leading slash,
	// which the TUI routes to a command handler. Empty leaves the
	// textarea idle so the user lands at a fresh chat ready to type.
	Message string

	// BackendFactory builds the backend for the resolved connection.
	// Defaults to DefaultBackendFactory so callers get the same auth
	// resolution and per-connection secrets store the rest of the
	// CLI uses. Tests substitute a fake here.
	BackendFactory BackendFactory

	// ConnectionsStore, when non-nil, is the connection set Chat
	// resolves Connection against. Defaults to LoadConnections() so
	// the CLI's standard on-disk store is used.
	ConnectionsStore *Connections
}

// Chat is the programmatic entry point for `lucinate chat`.
//
// Chat is a thin pre-flight stage: it resolves Connection (or falls
// back to ResolveEntryConnection's defaults), packs the agent /
// session / message overrides into RunOptions, and hands off to Run.
// The TUI consumes each override at the existing transition that
// would otherwise have prompted, so all the auth-recovery, single-
// connection / single-agent auto-pick, and `/connections` machinery
// keeps working — Chat is just driving past pickers it can answer
// for the user.
func Chat(ctx context.Context, opts ChatOptions) error {
	runOpts, err := resolveChatRunOptions(opts)
	if err != nil {
		return err
	}
	return Run(ctx, runOpts)
}

// resolveChatRunOptions does the resolution Chat needs without
// actually starting the TUI, so it can be exercised in unit tests.
// Returns RunOptions ready to pass to Run.
func resolveChatRunOptions(opts ChatOptions) (RunOptions, error) {
	factory := opts.BackendFactory
	if factory == nil {
		factory = DefaultBackendFactory
	}

	var store Connections
	if opts.ConnectionsStore != nil {
		store = *opts.ConnectionsStore
	} else {
		store = LoadConnections()
	}

	var initial *Connection
	if strings.TrimSpace(opts.Connection) != "" {
		conn := resolveSendConnection(&store, opts.Connection)
		if conn == nil {
			if len(store.Connections) == 0 {
				return RunOptions{}, fmt.Errorf("chat: no saved connections — run `lucinate` once to create one, then retry with --connection")
			}
			return RunOptions{}, fmt.Errorf("chat: connection %q not found", opts.Connection)
		}
		initial = conn
	} else {
		// No explicit --connection: use the same decision tree the
		// bare `lucinate` invocation uses. ShowPicker means we leave
		// Initial nil and let the TUI render the connections picker.
		entry := config.ResolveEntryConnection()
		// Replace the local store with the (possibly auto-mutated)
		// one ResolveEntryConnection produced — env-var auto-add
		// needs to be visible to the TUI's picker and persistence.
		store = entry.Store
		if !entry.ShowPicker {
			initial = entry.Connection
		}
	}

	storeCopy := store
	return RunOptions{
		Store:          &storeCopy,
		Initial:        initial,
		InitialAgent:   strings.TrimSpace(opts.Agent),
		InitialSession: strings.TrimSpace(opts.Session),
		InitialMessage: opts.Message,
		BackendFactory: factory,
		OnConnectionsChanged: func(c Connections) {
			// Best-effort persistence, mirroring main.go's bare-CLI
			// callback. An error here is non-fatal; the user simply
			// sees no last-used hint on the next launch.
			_ = SaveConnections(c)
		},
	}, nil
}

