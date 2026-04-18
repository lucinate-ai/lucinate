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

repclaw is a TUI chat client for the OpenClaw gateway, built with bubbletea.

### Packages

- **`internal/config`** — Loads `OPENCLAW_GATEWAY_URL` and `OPENCLAW_GATEWAY_TOKEN` from env/`.env` file. Auto-derives WebSocket URL from HTTP URL (`https` → `wss`).
- **`internal/client`** — Wraps the `openclaw-go` gateway SDK. Manages WebSocket connection, device identity (`~/.openclaw-go/identity/`), and bridges gateway events to a buffered channel for the TUI event loop.
- **`internal/tui`** — Bubbletea TUI with two views: agent selection (`selectModel`) and chat (`chatModel`). Chat supports streaming responses with markdown rendering via glamour.

### Flow

`main.go` loads config → creates gateway client → launches bubbletea program → user selects agent → chat view opens WebSocket session → messages sent/received via gateway SDK events.

### Key dependency

`github.com/a3tai/openclaw-go` is a **local replace** (`../openclaw-go`) — the OpenClaw Go SDK must be checked out as a sibling directory.
