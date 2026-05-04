// Package app runs the lucinate Bubble Tea program with pluggable I/O.
//
// The CLI entry point in main.go is a thin wrapper around Run; embedders
// that need to host the program with their own input source or output sink
// (for example, tests or alternative front-ends) either:
//
//   - construct a *client.Client themselves, connect it, and pass it via
//     RunOptions.Client (legacy single-connection mode used by native
//     platform embedders); or
//   - pass a *config.Connections store plus a ClientFactory and let the
//     TUI own the connection lifecycle, including the connections picker
//     and auth-recovery modals (used by the CLI).
//
// In both modes Run is a one-shot blocking invocation; embedders that
// need Resize or Quit from another goroutine build a *Program directly.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/lucinate-ai/lucinate/internal/backend"
	openclawBackend "github.com/lucinate-ai/lucinate/internal/backend/openclaw"
	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/config"
	"github.com/lucinate-ai/lucinate/internal/tui"
)

// BackendFactory builds an unconnected backend.Backend for the given
// connection. The TUI calls Connect on the returned backend itself so
// it can route auth errors (token-mismatch, token-missing, 401) into
// modal recovery flows. Embedders pass a factory whose implementation
// chooses the right concrete backend (OpenClaw, OpenAI-compat, ...)
// based on Connection.Type.
type BackendFactory = tui.BackendFactory

// RunOptions configures a Program.
type RunOptions struct {
	// Backend is an already-connected backend whose events drive
	// the UI. Setting Backend puts the program in legacy
	// single-connection mode: the connections picker and /connections
	// command are unavailable, and the lifecycle is the caller's
	// responsibility (Run does not Close it).
	//
	// Mutually exclusive with Store. Embedders driving the program
	// from a platform-native shell that already manages connections
	// elsewhere (native platform embedders) keep using this.
	Backend backend.Backend

	// Client is the previous name for Backend, kept as a typed
	// alias-style field so existing native platform embedders
	// continue to compile. New code should set Backend directly.
	// When both are set, Backend takes precedence.
	Client *client.Client

	// Store is the connections persistence used by the TUI when it
	// owns the connection lifecycle. Setting Store enables the
	// connections picker (entry view + /connections command) and the
	// auth-recovery modals; the program closes the active backend
	// when it exits. Mutually exclusive with Backend / Client.
	Store *config.Connections

	// Initial, when set alongside Store, is the connection the TUI
	// will attempt to use first. A nil Initial drops the user into the
	// connections picker as the entry view.
	Initial *config.Connection

	// InitialAgent, when non-empty, instructs the TUI to auto-select
	// the named agent (matched ID-first, then case-insensitive Name)
	// the first time the agent picker loads its list. A miss surfaces
	// as an error banner on the picker rather than silently falling
	// through. Used by `lucinate chat --agent <name>` to drive past
	// the picker without user interaction. The override is one-shot:
	// it is consumed on the first transition that would otherwise
	// have prompted, and cleared on auth-cancel / connect-failure /
	// `/connections` so a subsequent connection's picker is not
	// spuriously filtered against a name from a different scope.
	InitialAgent string

	// InitialSession, when non-empty, overrides the session key the
	// TUI passes to CreateSession on the first agent selection — the
	// `--session <key>` knob from `lucinate chat`. An empty value
	// preserves the existing default ("main", or the connection's
	// MainKey for its default agent). One-shot, cleared in the same
	// places as InitialAgent.
	InitialSession string

	// InitialMessage, when non-empty, is queued as the first user
	// turn in the chat view and submitted automatically once the
	// session's history has loaded. The `lucinate chat <text>`
	// auto-submit. Empty leaves the textarea idle. One-shot, cleared
	// in the same places as InitialAgent.
	InitialMessage string

	// BackendFactory builds a fresh, unconnected backend.Backend for
	// a connection. Required when Store is set.
	BackendFactory BackendFactory

	// OnConnectionsChanged, when set, is invoked whenever the TUI
	// adds, edits, deletes, or marks a connection as used. The CLI
	// wires this to SaveConnections so a successful connect persists
	// to disk. Embedders that own persistence elsewhere can mirror it
	// here.
	OnConnectionsChanged func(config.Connections)

	// Input is the source of user input bytes. If nil, os.Stdin is used.
	Input io.Reader

	// Output is the destination for rendered frames. If nil, os.Stdout is
	// used.
	Output io.Writer

	// InitialCols and InitialRows seed the program with a window-size
	// message before its first render. Embedders that drive a fixed-size
	// virtual terminal (e.g. an in-process renderer) should set these so
	// the first paint already fits the visible grid; otherwise Bubble Tea
	// renders against its default size and reflows on the first
	// post-layout WindowSizeMsg, which can leave stale characters on
	// screen until the next full repaint.
	InitialCols int
	InitialRows int

	// ColorProfile, when non-zero, overrides Bubble Tea's automatic
	// colour-profile detection. Bubble Tea inspects Output to decide
	// what palette Lipgloss is allowed to emit; when Output is not a
	// real TTY (an in-process virtual terminal driven by an embedder,
	// say) the auto-detected profile is NoTTY, which strips every SGR
	// sequence and produces a monochrome render. Embedders whose
	// terminal supports colour should set this to the appropriate
	// profile (typically colorprofile.TrueColor). The CLI leaves it
	// zero so its existing detection still applies.
	ColorProfile colorprofile.Profile

	// HideInputArea suppresses the chat view's textarea so the embedder
	// can supply its own input surface (for example, a platform-native
	// text field whose typed bytes are written into Input). The
	// underlying textarea model is still updated by the incoming byte
	// stream so command parsing, slash-command autocomplete, history,
	// and Enter-to-send behave exactly as in the CLI; only the textarea
	// view and its border are skipped, and the help line below
	// continues to surface slash-command hints. The CLI never needs
	// this; embedders without a separate input surface should leave it
	// false.
	HideInputArea bool

	// HideActionHints suppresses the inline help line each view renders
	// listing its action keys ("  n: new agent · r: retry"). Embedders
	// that surface the same actions through OnActionsChanged as native
	// controls (buttons, toolbar items) want this true so the hint text
	// isn't doubled up on screen. The CLI, whose only action surface is
	// the inline hint, leaves it false.
	HideActionHints bool

	// DisableExitKeys suppresses every quit shortcut the program would
	// otherwise honour: ctrl+c at the app level, and the bubbles list's
	// default `q` / `esc` / `ctrl+c` Quit bindings on each picker view.
	// Embedders running inside a host that doesn't allow programmatic
	// process termination — a native-platform shell whose OS forbids
	// quitting, where `tea.Quit` would only stop the TUI loop while
	// the host view stays mounted — set this true so the rendered
	// "q quit" footer text and the bound shortcut both disappear
	// together. The CLI, whose user reaches the program through a
	// terminal that can't otherwise dismiss it, leaves this false.
	DisableExitKeys bool

	// DisableMouse stops the program from emitting the
	// alt-screen mouse-tracking enable sequence. Embedders driving the
	// program through a virtual terminal whose host wants to handle
	// pan/swipe gestures natively (translating them into PgUp/PgDown
	// keystrokes for example) should set this so the host's gesture
	// recogniser doesn't capture pans into mouse motion events that the
	// program then ignores. The CLI relies on mouse tracking for
	// selection and should leave it false.
	DisableMouse bool

	// OnInputFocusChanged, if non-nil, is invoked whenever the active
	// view's preferred input mode changes. wantsInput is true when the
	// active view has a focused free-form text input (the chat
	// textarea, the new-agent form fields) and false when only
	// navigation keys are expected (the agent list, the session
	// browser, the config view). The callback fires once during start-up
	// with the initial state so the embedder need not assume a default,
	// and again on every subsequent transition.
	//
	// Embedders on platforms with an on-screen keyboard use this to
	// surface it only when the program actually wants typing, instead
	// of pinning it permanently and losing screen real estate. The
	// callback runs from a tea.Cmd goroutine — embedders that touch UI
	// on a main thread should trampoline accordingly. The CLI leaves
	// it nil.
	OnInputFocusChanged func(wantsInput bool)

	// OnActionsChanged, if non-nil, is invoked whenever the active
	// view's set of exposed Actions changes. The active view is the
	// authoritative source of its discoverable, view-level commands
	// (e.g. "new agent", "back", "retry"); the desktop TUI renders
	// these as inline help and dispatches the bound key, while
	// embedders typically render them as buttons whose tap calls back
	// in via Program.TriggerAction.
	//
	// The callback fires once at start-up with the initial list and
	// again on every transition. It runs from a tea.Cmd goroutine, so
	// embedders that touch UI on a main thread should trampoline
	// accordingly. The CLI leaves it nil — its inline help is the
	// surface that matters there.
	OnActionsChanged func(actions []Action)

	// OnFocusedFieldChanged, if non-nil, is invoked whenever the
	// active view's focused text-input changes — Tab/Shift-Tab inside
	// a multi-field form, or entry into a view that lands focus on a
	// different field. The string is the new field's current value
	// at the moment of transition (empty for a fresh field; pre-fill
	// in Edit mode), so embedders driving an external input surface
	// (a native-platform host's text field, say) can hydrate it to
	// match the TUI state without inventing their own per-field
	// bookkeeping.
	//
	// The callback does not fire on every keystroke — only on field
	// transitions, dedup'd by field identity — so embedders are safe
	// to use it as a source of truth without rate-limiting. It runs
	// from a tea.Cmd goroutine, so embedders that touch UI on a main
	// thread should trampoline accordingly. The CLI leaves it nil.
	OnFocusedFieldChanged func(value string)
}

// Program wraps a Bubble Tea program with the lucinate model and the
// gateway-events / supervisor goroutines whose lifetimes the
// connection driver controls. It is safe to call Resize, Quit, and
// TriggerAction from goroutines other than the one running Run.
type Program struct {
	tp        *tea.Program
	mode      programMode
	backend   backend.Backend      // legacy mode only
	backendCh chan backend.Backend // managed mode: TUI publishes the active backend here
	store     *config.Connections
}

type programMode int

const (
	modeLegacy programMode = iota // pre-connected Backend; lifecycle owned by caller
	modeManaged                   // Store + factory; lifecycle owned by the program
)

// resolveLegacyBackend reconciles the deprecated Client field with the
// new Backend field. New code sets Backend; existing native platform
// embedders pass *client.Client and we wrap it transparently. Both
// fields nil → legacy mode is unset (caller must use Store).
func resolveLegacyBackend(opts RunOptions) backend.Backend {
	if opts.Backend != nil {
		return opts.Backend
	}
	if opts.Client != nil {
		return openclawBackend.New(opts.Client)
	}
	return nil
}

// New constructs a Program with the given options. It does not start the
// underlying Bubble Tea loop; call Run to block on it.
func New(opts RunOptions) (*Program, error) {
	legacyBackend := resolveLegacyBackend(opts)
	if legacyBackend == nil && opts.Store == nil {
		return nil, errors.New("app: either Client/Backend or Store is required")
	}
	if legacyBackend != nil && opts.Store != nil {
		return nil, errors.New("app: Client/Backend and Store are mutually exclusive")
	}
	if opts.Store != nil && opts.BackendFactory == nil {
		return nil, errors.New("app: BackendFactory is required when Store is set")
	}

	in := opts.Input
	if in == nil {
		in = os.Stdin
	}
	out := opts.Output
	if out == nil {
		out = os.Stdout
	}

	mode := modeLegacy
	var backendCh chan backend.Backend
	if opts.Store != nil {
		mode = modeManaged
		// Buffered size 1 with drain-and-replace semantics in the
		// driver so a rapid sequence of backend switches collapses
		// to the most recent one rather than queueing.
		backendCh = make(chan backend.Backend, 1)
	}

	teaOpts := []tea.ProgramOption{
		tea.WithInput(in),
		tea.WithOutput(out),
	}
	if opts.InitialCols > 0 && opts.InitialRows > 0 {
		teaOpts = append(teaOpts, tea.WithWindowSize(opts.InitialCols, opts.InitialRows))
	}
	if opts.ColorProfile != 0 {
		teaOpts = append(teaOpts, tea.WithColorProfile(opts.ColorProfile))
	}

	tuiOpts := tui.AppOptions{
		HideInputArea:         opts.HideInputArea,
		HideActionHints:       opts.HideActionHints,
		DisableExitKeys:       opts.DisableExitKeys,
		DisableMouse:          opts.DisableMouse,
		OnInputFocusChanged:   opts.OnInputFocusChanged,
		OnActionsChanged:      opts.OnActionsChanged,
		OnFocusedFieldChanged: opts.OnFocusedFieldChanged,
		Store:                 opts.Store,
		Initial:               opts.Initial,
		InitialAgent:          opts.InitialAgent,
		InitialSession:        opts.InitialSession,
		InitialMessage:        opts.InitialMessage,
		BackendFactory:        opts.BackendFactory,
		OnConnectionsChanged:  opts.OnConnectionsChanged,
	}
	if mode == modeManaged {
		// Blocking send is fine: OnBackendChanged is invoked from a
		// tea.Cmd goroutine, and the driver drains promptly except
		// during tear-down of the previous backend (a brief wait).
		ch := backendCh
		tuiOpts.OnBackendChanged = func(b backend.Backend) {
			ch <- b
		}
	}

	model := tui.NewApp(legacyBackend, tuiOpts)
	tp := tea.NewProgram(model, teaOpts...)
	return &Program{
		tp:        tp,
		mode:      mode,
		backend:   legacyBackend,
		backendCh: backendCh,
		store:     opts.Store,
	}, nil
}

// Run starts the Bubble Tea program and blocks until it exits or ctx is
// cancelled. The events-pump goroutine that bridges gateway events into
// the program — and the connection supervisor that pushes reconnect
// state transitions — are owned by Run for the duration of the call.
//
// In legacy mode (Client set) the pump is bound once to that client and
// the caller closes it after Run returns. In managed mode (Store set)
// the pump rewires whenever the TUI publishes a new client through
// OnClientChanged, and Run closes whichever client is active when the
// program exits.
//
// Run is single-shot per Program; calling it more than once is a
// programming error and the second call's behaviour is undefined.
func (p *Program) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	driverDone := make(chan struct{})
	go p.runDriver(runCtx, driverDone)

	// Quit the program if the caller cancels the context.
	stopWatcher := make(chan struct{})
	go func() {
		select {
		case <-runCtx.Done():
			p.tp.Quit()
		case <-stopWatcher:
		}
	}()

	_, err := p.tp.Run()
	close(stopWatcher)
	cancel()
	<-driverDone

	if err != nil {
		return fmt.Errorf("program: %w", err)
	}
	return nil
}

// runDriver owns the per-backend pump+supervisor goroutines. In
// legacy mode it binds once to p.backend. In managed mode it watches
// backendCh and rebinds on every successful backend switch, closing
// the previous backend after its goroutines have drained.
func (p *Program) runDriver(runCtx context.Context, done chan<- struct{}) {
	defer close(done)

	type bound struct {
		b     backend.Backend
		stop  context.CancelFunc
		wg    *sync.WaitGroup
		owned bool // close on teardown (true in managed mode, false in legacy)
	}

	var current *bound

	tearDown := func(bnd *bound) {
		if bnd == nil {
			return
		}
		bnd.stop()
		bnd.wg.Wait()
		if bnd.owned && bnd.b != nil {
			_ = bnd.b.Close()
		}
	}

	bindBackend := func(b backend.Backend, owned bool) *bound {
		if b == nil {
			return nil
		}
		bctx, bcancel := context.WithCancel(runCtx)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			events := b.Events()
			for {
				select {
				case ev, ok := <-events:
					if !ok {
						return
					}
					p.tp.Send(tui.GatewayEventMsg(ev))
				case <-bctx.Done():
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			b.Supervise(bctx, func(s client.ConnState) {
				p.tp.Send(tui.ConnStateMsg{Status: s.Status, Attempt: s.Attempt, Err: s.Err})
			})
		}()
		return &bound{b: b, stop: bcancel, wg: &wg, owned: owned}
	}

	if p.mode == modeLegacy {
		current = bindBackend(p.backend, false)
		<-runCtx.Done()
		tearDown(current)
		return
	}

	for {
		select {
		case <-runCtx.Done():
			tearDown(current)
			return
		case b := <-p.backendCh:
			tearDown(current)
			current = bindBackend(b, true)
		}
	}
}

// Resize sends a window-size update to the running program. Safe to call
// from any goroutine. A no-op if the program has already exited.
func (p *Program) Resize(cols, rows int) {
	p.tp.Send(tea.WindowSizeMsg{Width: cols, Height: rows})
}

// Quit requests the program to exit cleanly. Safe to call from any
// goroutine. The corresponding Run call will return shortly afterwards.
func (p *Program) Quit() {
	p.tp.Quit()
}

// TriggerAction invokes the named action on the active view. Embedders
// pass the ID of one of the actions the program most recently published
// via OnActionsChanged. Safe to call from any goroutine; a no-op if the
// program has already exited or the active view does not recognise the
// ID (the latter typically means the embedder's UI is one transition
// behind the program — not an error).
func (p *Program) TriggerAction(id string) {
	p.tp.Send(tui.TriggerActionMsg{ID: id})
}

// Run is a convenience wrapper that constructs a Program and runs it to
// completion. Embedders that need Resize or Quit should use New + Program.Run
// instead.
func Run(ctx context.Context, opts RunOptions) error {
	p, err := New(opts)
	if err != nil {
		return err
	}
	return p.Run(ctx)
}
