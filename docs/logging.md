# Logging — lessons and rationale

The behavioural contract for logging lives in
[`openspec/specs/logging/spec.md`](../openspec/specs/logging/spec.md) — the destination-resolution
rule, level parsing, the environment variables, and the TUI-safety constraints are all captured
there as requirements and scenarios. This file keeps the hard-won lessons, pitfalls, and design
rationale behind that configuration: the "why it works this odd way" that the spec's requirements
don't dwell on.

## Why a side file by default

The TUI owns the terminal. Anything written to stdout or stderr while a frame is being rendered
will corrupt it — the cursor lands inside half-drawn ANSI sequences and the next repaint inherits
the garbage. The pre-slog code worked around this by hardcoding a debug file
(`/tmp/lucinate-events.log`) inside an `init()` hook in `internal/tui/logging.go`; the file was
opened on every start regardless of whether anyone wanted to read it, and there was no concept of
severity. `internal/logging` keeps the side-file default for TUI invocations but gates everything
behind a level and a configurable destination.

## Why truncate on start

Truncate-on-start matches the pre-slog behaviour and keeps the file scoped to the current session —
handy when you're tailing it while reproducing a bug. If you want to keep history across runs, point
`LUCINATE_LOG_FILE` at a path you'll archive yourself.

## Why the default level is `warn`

The default of `warn` means a bare `lucinate` run produces no log noise at all unless something
genuinely worth flagging happens. That preserves the previous "silent unless you opted into the
debug file" behaviour while still giving the operator a knob to turn up.

## Why unrecognised levels fall back silently

Anything unrecognised falls back to `warn` silently — we don't want a typo in a user's shell rc to
fail the launch.

## Why TUI detection lives in `cli.Run`

`Options.TUI` is set by `cli.Run` based on `isTUIInvocation(args)`. That subcommand-specific routing
lives in `cli.Run` deliberately — moving it into `app.Run` would mean every embedder has to
re-implement the same TUI / non-TUI distinction.

## The `closeForTest` re-truncation gotcha

`Init` is idempotent: a second call closes the previously opened file and installs a fresh handler,
so a re-init doesn't leak the fd tracked in `currentFile`. But re-init also re-truncates the file.
Tests therefore use `closeForTest` to flush and close without re-init — a re-init mid-test would wipe
the file you're asserting against.
