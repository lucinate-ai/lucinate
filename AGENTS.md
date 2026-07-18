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

See [`openspec/specs/connections/spec.md`](openspec/specs/connections/spec.md) for the full picture (capability negotiation, auth recovery, secrets storage, OpenAI agent storage layout), and [docs/connections.md](docs/connections.md) for the rationale behind it.

### Key dependency

`github.com/a3tai/openclaw-go` is a **local replace** (`../openclaw-go`) — the OpenClaw Go SDK must be checked out as a sibling directory.

## Specs and developer docs

This project uses [OpenSpec](https://github.com/Fission-AI/OpenSpec) for spec-driven
development. Three places hold the project's knowledge and each has its own lane — keep them
there:

- **`openspec/specs/<domain>/spec.md`** — the behavioural contract (requirements + scenarios):
  the source of truth for *what* each subsystem does. `openspec list --specs` lists them.
- **`docs/<domain>.md`** — the lessons, pitfalls, and design rationale (the *why*), each pointing
  at its sibling spec. See [docs/README.md](docs/README.md) for the doc↔spec index.
- **`openspec/config.yaml`** `context` — project-wide conventions (tech stack, the keyboard-key
  vocabulary, the native-platform naming rule). This is the **canonical** home for conventions
  and is injected into every OpenSpec proposal; update it here rather than restating rules
  elsewhere, so they can't drift.

Reach for the spec for "what should happen"; the doc for "why, and what to watch out for".

### Making a change

For any non-trivial change to behaviour, work through OpenSpec rather than editing a spec by
hand — the delta gets reviewed before it lands in the spec:

1. `/opsx:propose <kebab-id>` (optionally `/opsx:explore` first to think it through) scaffolds
   `openspec/changes/<id>/` with `proposal.md`, `design.md`, `tasks.md`, and delta specs under
   `specs/<domain>/spec.md` using `## ADDED` / `## MODIFIED` / `## REMOVED Requirements`.
2. Implement against `tasks.md` (`/opsx:apply`), keeping code, tests, and the delta in step.
3. `/opsx:archive` merges the delta into `openspec/specs/` and moves the change to
   `openspec/changes/archive/`.

The `/opsx:*` slash commands and `openspec-*` skills are installed under `.claude/`. Useful CLI:
`openspec list [--specs]`, `openspec show <item>`, `openspec validate --specs`,
`openspec archive <id>`. OpenSpec is brownfield-first: write specs for what you are changing —
don't back-fill specs for untouched code.

Small, self-contained fixes (a typo, a one-line behaviour tweak with its test) may update the
spec directly — and the doc if the reasoning changed — without the full change ceremony where it
adds no value.

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
- Anything requiring a real backend → guard with a build tag so `go test ./...` stays hermetic. The OpenClaw suite uses `//go:build integration` (`queue_integration_test.go`); the OpenAI suite uses `//go:build integration_openai` (`internal/backend/openai/integration_test.go`). Both have matching `make test-integration-{openclaw,openai}-{setup,,teardown}` targets (the OpenClaw setup splits further into `-ollama-setup` / `-bedrock-setup`) — see `test/integration/README.md`.

Run `make test` before committing (and `openspec validate --specs` if you touched any spec).
Pushes trigger CI; a failing test blocks review.

## Keeping docs in sync

When you add or change commands, key bindings, event handlers, or user-visible behaviour:

- **Behaviour changed?** Update the affected `openspec/specs/<domain>/spec.md` — through an
  OpenSpec change for anything non-trivial (see [Making a change](#making-a-change)).
- **Reasoning changed?** Update the matching `docs/<domain>.md` — a new pitfall, gotcha, or
  design trade-off worth recording. Leave it untouched for pure behaviour changes.
- **A convention changed?** Update `openspec/config.yaml`'s `context` (the canonical home), not a
  copy of the rule scattered elsewhere.

