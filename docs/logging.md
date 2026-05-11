# Logging

`internal/logging` configures the process-wide `slog.Default` logger from three environment variables and one runtime hint (`Options.TUI`). Every other package logs through `log/slog` against the default logger; nothing in the codebase calls `log.Print*` directly.

The user-facing summary lives in the [README → Environment variables](../README.md#environment-variables). This doc covers the design, the destination-resolution rule, and the TUI-safety constraints that drove the defaults.

## Why a side file by default

The TUI owns the terminal. Anything written to stdout or stderr while a frame is being rendered will corrupt it — the cursor lands inside half-drawn ANSI sequences and the next repaint inherits the garbage. The pre-slog code worked around this by hardcoding a debug file (`/tmp/lucinate-events.log`) inside an `init()` hook in `internal/tui/logging.go`; the file was opened on every start regardless of whether anyone wanted to read it, and there was no concept of severity.

`internal/logging` keeps the side-file default for TUI invocations but gates everything behind a level and a configurable destination, so the same `slog.Warn` call can:

- land in `/tmp/lucinate-events.log` during a TUI session,
- stream to stderr during `lucinate send` so the operator sees it inline,
- or land in a JSON file the user pointed at, for piping into `jq`.

Below the configured level, calls are dropped by the slog handler before they reach the writer.

The handler is plain `slog.NewTextHandler` / `slog.NewJSONHandler` — no custom formatter, no third-party dependency.

## Destination resolution

`openDestination` (`internal/logging/logging.go`) implements the rule:

| `LUCINATE_LOG_FILE` set | `Options.TUI` | Destination |
|---|---|---|
| yes | either | the named file, opened `O_TRUNC` (current session only) |
| no  | true   | `/tmp/lucinate-events.log`, opened `O_TRUNC` |
| no  | false  | `os.Stderr` |

Truncate-on-start matches the pre-slog behaviour and keeps the file scoped to the current session — handy when you're tailing it while reproducing a bug. If you want to keep history across runs, point `LUCINATE_LOG_FILE` at a path you'll archive yourself.

`Options.TUI` is set by `cli.Run` based on `isTUIInvocation(args)`: empty args (bare `lucinate`) and `lucinate chat` are TUI; everything else (`send`, `help`, `--version`, unknown subcommands) is non-TUI. Subcommand-specific routing lives in `cli.Run` deliberately — moving it into `app.Run` would mean every embedder has to re-implement the same TUI / non-TUI distinction.

## Levels

Standard `log/slog` levels: `debug`, `info`, `warn` (the default), `error`. Parsing is case-insensitive and tolerant of `warning` for warn. Anything unrecognised falls back to `warn` silently — we don't want a typo in a user's shell rc to fail the launch.

The default of `warn` means a bare `lucinate` run produces no log noise at all unless something genuinely worth flagging happens. That preserves the previous "silent unless you opted into the debug file" behaviour while still giving the operator a knob to turn up.

`internal/tui/logging.go` keeps a thin `logEvent(format, args...)` wrapper around `slog.Debug` so the ~35 existing TUI lifecycle call sites didn't have to migrate to structured attrs in lockstep. New code should call `slog.Debug` (or `slog.Info`/`Warn`/`Error`) directly with `key, value` attrs instead.

## Re-init and tests

`Init` is idempotent: a second call closes the previously opened file (if any) and installs a fresh handler. The package keeps the open file handle in `currentFile` so a re-init doesn't leak a fd. Tests use `closeForTest` to flush and close without re-init (which would re-truncate the file mid-test).

Outside tests, nothing currently re-inits — `cli.Run` calls `Init` once at the top of the dispatch and the rest of the process inherits `slog.Default`.

## Where the warnings used to go

Two pre-existing `log.Printf("warning: ...")` calls in `internal/client/client.go` (device-token persistence and `IDENTITY.md` seeding) wrote straight to `os.Stderr` via the stdlib default logger. With the TUI mounted, that meant any failure during connect or agent creation would garble the screen. They are now `slog.Warn`, so they obey the destination rule above and stay out of the rendered frame unless `LUCINATE_LOG_FILE` says otherwise.
