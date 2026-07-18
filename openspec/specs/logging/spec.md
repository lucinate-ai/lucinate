# Logging Specification

## Purpose

Configure the process-wide `slog.Default` logger for lucinate from three environment
variables and one runtime hint (`Options.TUI`). The `internal/logging` package owns this
configuration; every other package logs through `log/slog` against the default logger and
nothing in the codebase calls `log.Print*` directly. This spec covers the logging design,
the destination-resolution rule, level parsing, and the TUI-safety constraints that drove
the defaults. The user-facing summary lives in the README's environment-variables section.

## Requirements

### Requirement: slog-based logging through the default logger

The `internal/logging` package SHALL configure the process-wide `slog.Default` logger, and
every other package SHALL log through `log/slog` against that default logger. Nothing in the
codebase SHALL call `log.Print*` directly. The handler SHALL be a plain
`slog.NewTextHandler` or `slog.NewJSONHandler` — there is no custom formatter and no
third-party logging dependency. TUI lifecycle call sites in
`internal/tui/{events,sessions,chat,commands}.go` SHALL use `slog.Debug` directly with
`key, value` attributes. Earlier revisions kept a `logEvent(format, args...)` shim around
printf-style calls; that shim is gone and new code SHOULD follow the same attribute pattern.

#### Scenario: Package logs via the default logger

- **WHEN** any package emits a log line
- **THEN** it calls a `log/slog` function against `slog.Default`
- **AND** it does not call `log.Print*` directly

#### Scenario: TUI call sites use structured attributes

- **WHEN** a TUI lifecycle call site logs an event
- **THEN** it calls `slog.Debug` directly with `key, value` attributes
- **AND** does not route through a printf-style `logEvent` shim

### Requirement: Side-file default protects the TUI terminal

Because the TUI owns the terminal, anything written to stdout or stderr while a frame is
being rendered will corrupt it — the cursor lands inside half-drawn ANSI sequences and the
next repaint inherits the garbage. For TUI invocations the system SHALL default log output to
a side file rather than the terminal. This preserves the pre-slog behaviour, which hardcoded a
debug file (`/tmp/lucinate-events.log`) inside an `init()` hook in `internal/tui/logging.go`;
that old file was opened on every start regardless of whether anyone wanted to read it and had
no concept of severity. `internal/logging` keeps the side-file default for TUI invocations but
gates everything behind a level and a configurable destination, so the same `slog.Warn` call
can land in `<os-tempdir>/lucinate-events.log` during a TUI session (resolved via
`os.TempDir()` so the path is sensible on every platform), stream to stderr during
`lucinate send` so the operator sees it inline, or land in a JSON file the user pointed at for
piping into `jq`. Calls below the configured level SHALL be dropped by the slog handler before
they reach the writer.

#### Scenario: TUI session logs to a side file, not the terminal

- **GIVEN** a TUI invocation with no `LUCINATE_LOG_FILE` set
- **WHEN** a log call at or above the configured level fires
- **THEN** it is written to `<os.TempDir()>/lucinate-events.log`
- **AND** nothing is written to stdout or stderr that could corrupt the rendered frame

#### Scenario: Sub-level calls are dropped

- **WHEN** a log call is below the configured level
- **THEN** the slog handler drops it before it reaches the writer

### Requirement: Destination resolution rule

The `openDestination` function (`internal/logging/logging.go`) SHALL resolve the log
destination from `LUCINATE_LOG_FILE` and `Options.TUI` per this rule:

| `LUCINATE_LOG_FILE` set | `Options.TUI` | Destination |
|---|---|---|
| yes | either | the named file, opened `O_TRUNC` (current session only) |
| no  | true   | `DefaultTUIFile()` (`<os.TempDir()>/lucinate-events.log`), opened `O_TRUNC` |
| no  | false  | `os.Stderr` |

Truncate-on-start matches the pre-slog behaviour and keeps the file scoped to the current
session — handy when tailing it while reproducing a bug. To keep history across runs, the user
SHALL point `LUCINATE_LOG_FILE` at a path they will archive themselves.

#### Scenario: Explicit log file wins regardless of mode

- **GIVEN** `LUCINATE_LOG_FILE` is set
- **WHEN** the destination is resolved
- **THEN** the named file is used, opened `O_TRUNC` for the current session only
- **AND** the choice is independent of `Options.TUI`

#### Scenario: TUI default file

- **GIVEN** `LUCINATE_LOG_FILE` is unset AND `Options.TUI` is true
- **WHEN** the destination is resolved
- **THEN** it is `DefaultTUIFile()` (`<os.TempDir()>/lucinate-events.log`), opened `O_TRUNC`

#### Scenario: Non-TUI defaults to stderr

- **GIVEN** `LUCINATE_LOG_FILE` is unset AND `Options.TUI` is false
- **WHEN** the destination is resolved
- **THEN** it is `os.Stderr`

### Requirement: TUI-mode detection drives the destination

`Options.TUI` SHALL be set by `cli.Run` based on `isTUIInvocation(args)`: empty args (bare
`lucinate`) and `lucinate chat` are TUI; everything else (`send`, `help`, `--version`, unknown
subcommands) is non-TUI. This subcommand-specific routing SHALL live in `cli.Run` deliberately —
moving it into `app.Run` would mean every embedder has to re-implement the same TUI / non-TUI
distinction.

#### Scenario: Bare invocation and chat are TUI

- **WHEN** the process is launched as bare `lucinate` or `lucinate chat`
- **THEN** `isTUIInvocation(args)` is true and `Options.TUI` is set true

#### Scenario: Other subcommands are non-TUI

- **WHEN** the process is launched as `send`, `help`, `--version`, or an unknown subcommand
- **THEN** `isTUIInvocation(args)` is false and `Options.TUI` is set false

### Requirement: Log level parsing and default

The system SHALL support the standard `log/slog` levels `debug`, `info`, `warn`, and `error`,
with `warn` as the default. Level parsing SHALL be case-insensitive and SHALL tolerate
`warning` as an alias for `warn`. Anything unrecognised SHALL fall back to `warn` silently,
because a typo in a user's shell rc must not fail the launch. The `warn` default means a bare
`lucinate` run produces no log noise at all unless something genuinely worth flagging happens,
preserving the previous "silent unless you opted into the debug file" behaviour while still
giving the operator a knob to turn up.

#### Scenario: Case-insensitive parsing with warning alias

- **WHEN** the level is given as `WARN`, `Warning`, or `warning`
- **THEN** it resolves to the `warn` level

#### Scenario: Unrecognised level falls back silently

- **GIVEN** an unrecognised level string (e.g. a typo)
- **WHEN** the logger is initialised
- **THEN** it falls back to `warn` without failing the launch

#### Scenario: Default is silent for a bare run

- **GIVEN** no level override
- **WHEN** a bare `lucinate` runs and nothing warn-worthy happens
- **THEN** no log output is produced

### Requirement: Environment-variable configuration

The system SHALL configure logging from three environment variables and the `Options.TUI`
runtime hint:

| Variable | Effect |
|---|---|
| `LUCINATE_LOG_FILE` | Destination path; when set, log output goes to this file, opened `O_TRUNC` for the current session, regardless of TUI mode |
| `LUCINATE_LOG_LEVEL` | Minimum level (`debug`/`info`/`warn`/`error`, case-insensitive, `warning` accepted); unrecognised values fall back to `warn` |
| `LUCINATE_LOG_FORMAT` | Handler selection between the text handler (`slog.NewTextHandler`) and the JSON handler (`slog.NewJSONHandler`), the latter suitable for piping into `jq` |

#### Scenario: Log file variable overrides the destination

- **GIVEN** `LUCINATE_LOG_FILE` points at a path
- **WHEN** the logger initialises
- **THEN** output goes to that file, opened `O_TRUNC`, regardless of TUI mode

#### Scenario: JSON format for machine consumption

- **GIVEN** `LUCINATE_LOG_FORMAT` selects JSON
- **WHEN** the logger initialises
- **THEN** it installs `slog.NewJSONHandler` so records can be piped into `jq`

### Requirement: Idempotent re-init and test lifecycle

`Init` SHALL be idempotent: a second call SHALL close the previously opened file (if any) and
install a fresh handler. The package SHALL keep the open file handle in `currentFile` so a
re-init does not leak a file descriptor. Tests SHALL use `closeForTest` to flush and close
without re-init, since re-init would re-truncate the file mid-test. Outside tests nothing
currently re-inits — `cli.Run` calls `Init` once at the top of the dispatch and the rest of the
process inherits `slog.Default`.

#### Scenario: Second Init closes the prior file

- **GIVEN** `Init` has already opened a log file
- **WHEN** `Init` is called again
- **THEN** the previously opened file is closed and a fresh handler is installed
- **AND** no file descriptor is leaked because the handle is tracked in `currentFile`

#### Scenario: Tests close without re-truncating

- **WHEN** a test needs to flush and close the log
- **THEN** it calls `closeForTest` rather than re-initialising
- **AND** the file is not re-truncated mid-test

#### Scenario: Single init in normal operation

- **WHEN** the process runs outside tests
- **THEN** `cli.Run` calls `Init` once at the top of the dispatch
- **AND** the rest of the process inherits `slog.Default` without re-init
