# AGENTS.md

This file provides guidance to AI coding agents when working with code in this repository.

## Build & Development Commands

```bash
make build            # Build binary (version from git tags)
make build-prod       # Production build (stripped/trimmed)
make test             # Run all tests
make coverage         # Run tests with coverage report
make coverage-html    # Generate HTML coverage report
make fmt              # Format code
make run args="..."   # Run with arguments
make install          # Install binary globally
```

Run a single test: `go test ./internal/tui/ -run TestExtractContent`

## Architecture

lucinate is a TUI chat client for the OpenClaw gateway, built with bubbletea.

### Packages

- **`internal/config`** — Loads `OPENCLAW_GATEWAY_URL` from env/`.env` file. Auto-derives WebSocket URL from HTTP URL (`https` → `wss`).
- **`internal/client`** — Wraps the `openclaw-go` gateway SDK. Manages WebSocket connection, device identity (`~/.openclaw-go/identity/`), and bridges gateway events to a buffered channel for the TUI event loop.
- **`internal/tui`** — Bubbletea TUI with two views: agent selection (`selectModel`) and chat (`chatModel`). Chat supports streaming responses with markdown rendering via glamour.

### Flow

`main.go` loads config → creates gateway client → launches bubbletea program → user selects agent → chat view opens WebSocket session → messages sent/received via gateway SDK events.

### Key dependency

`github.com/a3tai/openclaw-go` is a **local replace** (`../openclaw-go`) — the OpenClaw Go SDK must be checked out as a sibling directory.

## Developer docs

The `docs/` directory contains maintainer-level documentation for the main subsystems:

- [authentication.md](docs/authentication.md) — device pairing flow, identity storage, gateway connection
- [agents.md](docs/agents.md) — agent picker, auto-selection, agent creation
- [sessions.md](docs/sessions.md) — session lifecycle, session browser, compact/reset, message queueing
- [commands.md](docs/commands.md) — slash command dispatch, all built-in commands, tab completion, confirmation pattern
- [shell-execution.md](docs/shell-execution.md) — `!` local and `!!` remote exec, two-phase approval
- [skills.md](docs/skills.md) — skill file format, discovery, catalog injection, activation
- [chat-ux.md](docs/chat-ux.md) — input bindings, streaming animation, thinking levels, header bar, history depth
- [message-rendering.md](docs/message-rendering.md) — message roles, `System:` prefix convention, history cleanup, markdown rendering

## Testing requirements

Add or update tests whenever you change behaviour. Focus on core functionality — tests should capture behaviour a user or caller actually depends on, not exist for coverage's sake.

**Write a test when you:**
- add or change a command, event handler, key binding, or slash command
- change rendering output users see (prefixes, help bar, queued/pending state, streaming cursor, error styling)
- change control flow in `chatModel`/`selectModel` (queueing, draining, state transitions, view switches)
- fix a bug — add a regression test that fails without the fix

**Don't add a test for:**
- trivial getters/setters, style constants, or pure wiring
- behaviour already covered by an existing test
- implementation details that would lock in a specific refactor

**Pick the right level:**
- Pure logic (formatters, wrapping, validation, slash parsing) → plain unit tests against the function.
- Model state transitions → drive `Update` directly and assert on the returned model (see `commands_test.go`, `select_test.go`).
- Rendered output → use `teatest/v2` against a model adapter (see `rendering_test.go`). Assert on ANSI-stripped bytes via a single `teatest.WaitFor` — repeated `WaitFor` calls drain `tm.Output()`.
- Anything requiring the real gateway → guard with `//go:build integration` (see `queue_integration_test.go`) so `go test ./...` stays hermetic.

Run `make test` before committing. Pushes trigger CI; a failing test blocks review.

