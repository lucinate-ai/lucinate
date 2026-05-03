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

lucinate is a TUI chat client for backend agent runtimes, built with bubbletea. Two backend types ship: OpenClaw (gateway, WebSocket) and OpenAI-compatible (any `/v1/chat/completions` endpoint — Ollama, vLLM, LM Studio, OpenAI proper). The TUI talks to backends through a uniform `backend.Backend` interface so the chat / sessions / commands code paths are backend-agnostic.

### Packages

- **`internal/config`** — Persisted state: connections (`connections.json`), API-key secrets (`secrets/secrets.json`), preferences (`config.json`). `ResolveEntryConnection()` (`startup.go`) is the entry-view decision tree consulted by `main.go`.
- **`internal/backend`** — `Backend` interface plus optional sub-interfaces for capabilities not every backend exposes (`StatusBackend`, `ExecBackend`, `CompactBackend`, `ThinkingBackend`, `UsageBackend`, `DeviceTokenAuth`, `APIKeyAuth`).
  - **`internal/backend/openclaw`** — Adapter wrapping the OpenClaw gateway client (`internal/client`).
  - **`internal/backend/openai`** — `/v1/chat/completions` SSE translated into the gateway's protocol event shape; agents stored locally as IDENTITY.md + SOUL.md + history.jsonl under `~/.lucinate/agents/<conn-id>/<agent-id>/`.
- **`internal/client`** — Wraps the `openclaw-go` gateway SDK. Manages WebSocket connection, device identity (`~/.lucinate/identity/<endpoint>/`), and bridges gateway events to a buffered channel. A `Supervise` goroutine reconnects with exponential backoff if the WebSocket drops.
- **`internal/tui`** — Bubbletea TUI. Views: connections picker (`connectionsModel`), connecting/auth-modal (`connectingModel`), agent picker (`selectModel`), chat (`chatModel`), session browser (`sessionsModel`), config (`configModel`).

### Flow

`main.go` runs `ResolveEntryConnection()` → constructs `app.RunOptions` with a `BackendFactory` that dispatches by `Connection.Type` → launches bubbletea. The TUI owns the connection lifecycle in managed mode (Connect, auth modals, switch via `/connections`); the app driver in `app/app.go` rewires the events pump and supervisor whenever a new backend is published via `OnBackendChanged`.

See [docs/connections.md](docs/connections.md) for the full picture (capability negotiation, auth recovery, secrets storage, OpenAI agent storage layout).

### Key dependency

`github.com/a3tai/openclaw-go` is a **local replace** (`../openclaw-go`) — the OpenClaw Go SDK must be checked out as a sibling directory.

## Developer docs

The `docs/` directory contains maintainer-level documentation for the main subsystems:

- [connections.md](docs/connections.md) — connection types, picker, startup decision tree, capability negotiation, OpenAI agent storage, secrets
- [authentication.md](docs/authentication.md) — device pairing flow, identity storage, gateway connection
- [agents.md](docs/agents.md) — agent picker, auto-selection, agent creation
- [sessions.md](docs/sessions.md) — session lifecycle, session browser, compact/reset, message queueing
- [commands.md](docs/commands.md) — slash command dispatch, all built-in commands, tab completion, confirmation pattern
- [one-shot.md](docs/one-shot.md) — `lucinate send` lifecycle, default session rule, detach semantics, embedding `app.Send`
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
- Anything requiring a real backend → guard with a build tag so `go test ./...` stays hermetic. The OpenClaw suite uses `//go:build integration` (`queue_integration_test.go`); the OpenAI suite uses `//go:build integration_openai` (`internal/backend/openai/integration_test.go`). Both have matching `make test-integration{,-openai}-{setup,,teardown}` targets — see `test/integration/README.md`.

Run `make test` before committing. Pushes trigger CI; a failing test blocks review.

## Documentation

When you add or change commands, key bindings, event handlers, or user-visible behaviour, update the relevant file(s) in the `docs/` directory. The docs listed in [Developer docs](#developer-docs) are the canonical reference for each subsystem — keep them in sync with the code.

