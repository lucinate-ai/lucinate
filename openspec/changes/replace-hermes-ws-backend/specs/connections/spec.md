## MODIFIED Requirements

### Requirement: Three backend types over a common interface

The system SHALL ship three backend types — OpenClaw, OpenAI-compatible, and Hermes — all implementing `backend.Backend` (`internal/backend/backend.go`) so the chat / sessions / commands views are backend-agnostic. The OpenAI backend speaks HTTP+SSE+JSON over Bearer-token auth using the request builder, SSE scanner, and event emitter in `internal/backend/httpcommon`. OpenClaw and Hermes both speak a JSON message protocol over WebSocket instead — OpenClaw via the openclaw-go gateway SDK, Hermes via the `tui_gateway` JSON-RPC protocol — and SHALL NOT depend on `httpcommon`. The three backends are sibling implementations, not a base class and subclasses. Backend-specific behaviour is documented in dedicated specs:

- the `backend-openclaw` spec — full capability surface, device-token auth, server-side agents
- the `backend-openai` spec — `/v1/chat/completions` streaming, on-disk agents (IDENTITY.md + SOUL.md), API-key auth
- the `backend-hermes` spec — `tui_gateway` JSON-RPC over WebSocket (`hermes dashboard`, `/api/ws`), one synthetic agent per connection, server-side sessions, gateway-token auth

The three types differ as follows:

| Type      | URL shape                                                   | Auth                                   | Agent storage                                                                  |
|-----------|-------------------------------------------------------------|----------------------------------------|--------------------------------------------------------------------------------|
| OpenClaw  | `https://`/`http://`/`wss://`/`ws://` (WS endpoint derived) | Ed25519 device pairing                 | Server-side on the gateway                                                     |
| OpenAI    | `http(s)://host/v1`                                         | Optional `Authorization: Bearer <key>` | Local under `~/.lucinate/agents/<conn-id>/<agent-id>/`                         |
| Hermes    | `http(s)://host:9119` (`hermes dashboard` gateway, WS derived) | Gateway session token                 | Server-side in the Hermes profile; no local state                              |

#### Scenario: Views stay backend-agnostic

- **GIVEN** any of the three backend types is the active connection
- **WHEN** the chat, sessions or commands views operate
- **THEN** they call through the common `backend.Backend` interface without type-specific branching

#### Scenario: OpenAI uses shared HTTP plumbing; OpenClaw and Hermes use WebSocket

- **GIVEN** the OpenAI backend speaks HTTP+SSE+JSON over Bearer-token auth
- **THEN** it uses the request builder, SSE scanner and event emitter in `internal/backend/httpcommon`
- **AND** the OpenClaw and Hermes backends speak a JSON message protocol over WebSocket and do not depend on `httpcommon`
- **AND** all three remain sibling implementations rather than a base class and subclasses

### Requirement: Capability negotiation via optional sub-interfaces

`backend.Backend.Capabilities()` SHALL report a `Capabilities` struct (`internal/backend/backend.go`); the TUI SHALL type-assert against optional sub-interfaces (`StatusBackend`, `ExecBackend`, `CompactBackend`, `ThinkingBackend`, `UsageBackend`, `CronBackend`, `DeviceTokenAuth`, `APIKeyAuth`) at the relevant call sites. OpenClaw implements all of them. OpenAI implements `APIKeyAuth`, `CompactBackend` (the latter via a local summarisation pass — see the `backend-openai` spec), and `StatusBackend`. Hermes implements `APIKeyAuth` (gateway token), `StatusBackend`, `UsageBackend`, and `CompactBackend`. Backend-only commands the active connection doesn't support (`/think`, `/crons`, `!!` on OpenAI/Hermes; `/stats` on OpenAI) SHALL render a "not available on this connection" system message instead of erroring.

#### Scenario: Unsupported command shows a system message

- **GIVEN** the active connection does not support a backend-only command (e.g. `/think` on OpenAI, `/crons` on Hermes)
- **WHEN** the user runs it
- **THEN** a "not available on this connection" system message is shown instead of an error

#### Scenario: OpenClaw implements every sub-interface

- **GIVEN** an OpenClaw connection
- **THEN** it implements all of `StatusBackend`, `ExecBackend`, `CompactBackend`, `ThinkingBackend`, `UsageBackend`, `CronBackend`, `DeviceTokenAuth` and `APIKeyAuth`

#### Scenario: Hermes supports usage and compact

- **GIVEN** a Hermes connection
- **WHEN** the user runs `/stats` or `/compact`
- **THEN** the command succeeds because Hermes implements `UsageBackend` and `CompactBackend`, rather than showing "not available on this connection"

### Requirement: Connections form presets and persisted types

`internal/tui/connections.go` SHALL implement the picker. The form SHALL have a fixed type radio plus type-conditional fields. It SHALL offer four presets that map to three persisted types:

| Preset   | Persisted Type | Fields                                                     |
|----------|----------------|------------------------------------------------------------|
| OpenClaw | `openclaw`     | Type, Name, Gateway URL                                    |
| OpenAI   | `openai`       | Type, Name, Base URL, Default model (optional)             |
| Ollama   | `openai`       | Type, Name, Base URL, Default model (optional)             |
| Hermes   | `hermes`       | Type, Name, Base URL                                       |

Ollama SHALL be an opinionated OpenAI preset that pre-fills `Name = ollama` and `Base URL = http://localhost:11434/v1`. Hermes SHALL be its own type with a pre-filled `Base URL = http://localhost:9119` (the `hermes dashboard` gateway); it SHALL NOT carry a `/v1` path hint or a model field, because the Hermes profile pins its model server-side and the gateway token is supplied through the auth modal rather than a form field. Switching to any preset and back SHALL clear the prefill so the user isn't stranded with the wrong localhost URL in a gateway field.

#### Scenario: Ollama preset pre-fills OpenAI-typed defaults

- **WHEN** the user selects the Ollama preset
- **THEN** the form pre-fills `Name = ollama` and `Base URL = http://localhost:11434/v1` and persists type `openai`

#### Scenario: Hermes preset pre-fills the gateway URL without a model field

- **WHEN** the user selects the Hermes preset
- **THEN** the form pre-fills `Base URL = http://localhost:9119` with no `/v1` hint and no model field, and persists type `hermes`

#### Scenario: Switching presets clears stale prefill

- **GIVEN** a preset has pre-filled a localhost Base URL
- **WHEN** the user switches to another preset and back
- **THEN** the prefill is cleared so no wrong localhost URL is stranded in a gateway field
