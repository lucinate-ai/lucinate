// Package app runs the lucinate Bubble Tea program with pluggable I/O.
//
// The CLI entry point in main.go is a thin wrapper around Run; embedders
// that need to host the program with their own input source or output sink
// (for example, tests or alternative front-ends) construct a *client.Client,
// connect it, and then either call Run for a one-shot blocking invocation
// or build a *Program directly when they need to send window-size updates
// or request a quit from another goroutine.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/lucinate-ai/lucinate/internal/client"
	"github.com/lucinate-ai/lucinate/internal/tui"
)

// RunOptions configures a Program.
type RunOptions struct {
	// Client is the already-connected gateway client whose events drive the
	// UI. Neither Run nor Program closes the client; lifecycle is the
	// caller's responsibility.
	Client *client.Client

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
}

// Program wraps a Bubble Tea program with the lucinate model and a
// gateway-events pump goroutine. It is safe to call Resize and Quit from
// goroutines other than the one running Run.
type Program struct {
	tp     *tea.Program
	client *client.Client
}

// New constructs a Program with the given options. It does not start the
// underlying Bubble Tea loop; call Run to block on it.
func New(opts RunOptions) (*Program, error) {
	if opts.Client == nil {
		return nil, errors.New("app: Client is required")
	}
	in := opts.Input
	if in == nil {
		in = os.Stdin
	}
	out := opts.Output
	if out == nil {
		out = os.Stdout
	}

	model := tui.NewApp(opts.Client, tui.AppOptions{
		HideInputArea:       opts.HideInputArea,
		DisableMouse:        opts.DisableMouse,
		OnInputFocusChanged: opts.OnInputFocusChanged,
	})
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
	tp := tea.NewProgram(model, teaOpts...)
	return &Program{tp: tp, client: opts.Client}, nil
}

// Run starts the Bubble Tea program and blocks until it exits or ctx is
// cancelled. The events-pump goroutine that bridges gateway events into the
// program is owned by Run for the duration of the call.
//
// Run is single-shot per Program; calling it more than once is a programming
// error and the second call's behaviour is undefined.
func (p *Program) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		events := p.client.Events()
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					return
				}
				p.tp.Send(tui.GatewayEventMsg(ev))
			case <-runCtx.Done():
				return
			}
		}
	}()

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
	<-pumpDone

	if err != nil {
		return fmt.Errorf("program: %w", err)
	}
	return nil
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
