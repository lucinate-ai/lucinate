# Connections and Backends Specification

## Purpose

A connection is a saved target lucinate can connect to (URL + type + auth identity), persisted to `~/.lucinate/connections.json` and managed through the connections picker. Three backend types ship today — OpenClaw, OpenAI-compatible, and Hermes — all implementing a common `backend.Backend` interface so the chat, sessions and commands views remain backend-agnostic. This spec covers the connection types, the connections picker and form, the startup decision tree, the connection lifecycle, capability negotiation, auth-recovery modals, and secrets storage.

## Requirements

### Requirement: Connections picker as connection management surface

The system SHALL manage the list of saved connections through the connections picker (`viewConnections`), which persists the list to `~/.lucinate/connections.json`. Each connection is a saved target (URL + type + auth identity). The picker SHALL be accessible as the entry view on first run, via `/connections` mid-session, or via the **Connections** action on the agent picker. The picker SHALL also expose a **Settings** action (key `s`) that opens the settings screen without leaving the pre-chat flow.

#### Scenario: Reaching the picker mid-session

- **WHEN** the user runs `/connections` during a session
- **THEN** the connections picker opens
- **AND** the same picker is reachable as the first-run entry view and via the **Connections** action on the agent picker

#### Scenario: Opening settings from the picker

- **GIVEN** the connections picker is showing
- **WHEN** the user presses `s`
- **THEN** the settings screen opens without leaving the pre-chat flow

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

### Requirement: Adding a new backend type

The `AllConnectionTypes` enum (`internal/config/connections.go`) SHALL drive the picker form's type radio. Adding a fourth backend SHALL require: implementing `backend.Backend`, extending the enum, dispatching in `DefaultBackendFactory` (`app/factory.go`), adjusting the form's type-conditional rendering, and writing a `backend_<name>.md` doc for it.

`DefaultBackendFactory` has two non-TUI consumers:

- `app.Send` (`lucinate send`) calls it directly to build a backend for one-shot dispatch — a new connection type that lands in the factory's switch is automatically reachable from the scripted CLI mode without further wiring (see the `one-shot` spec).
- `app.Chat` (`lucinate chat`) defers all connect / list / create work to the TUI and just packs `Connection` / `Agent` / `Session` / `Message` overrides into `RunOptions`, so a new connection type is reachable there as soon as it works in the regular TUI (see the `chat-launch` spec).

#### Scenario: Wiring a fourth backend

- **WHEN** a fourth backend type is added
- **THEN** it implements `backend.Backend`, extends `AllConnectionTypes`, dispatches in `DefaultBackendFactory`, adjusts the form's type-conditional rendering, and ships a `backend_<name>.md` doc

#### Scenario: New type reachable from one-shot and chat modes

- **GIVEN** a new connection type has landed in the `DefaultBackendFactory` switch
- **THEN** `app.Send` (`lucinate send`) can build it for one-shot dispatch without further wiring
- **AND** `app.Chat` (`lucinate chat`) can reach it as soon as it works in the regular TUI

### Requirement: Startup entry-connection decision tree

`config.ResolveEntryConnection()` (`internal/config/startup.go`) SHALL decide what the TUI's entry view is, in the following order:

1. If `OPENCLAW_GATEWAY_URL` is set, find or auto-add a matching OpenClaw connection.
2. Else if `LUCINATE_OPENAI_BASE_URL` is set, find or auto-add a matching OpenAI connection (with `LUCINATE_OPENAI_DEFAULT_MODEL` if provided).
3. Else if a saved `defaultId` resolves to a known entry → use it (last-used = default).
4. Else if exactly one connection is stored → auto-pick it.
5. Else → open the connections picker.

Auto-add (steps 1–2) SHALL mutate the in-memory store but SHALL NOT persist it until a successful connect, so a typo in the env URL doesn't accumulate ghost entries.

#### Scenario: Gateway env var wins

- **GIVEN** `OPENCLAW_GATEWAY_URL` is set
- **WHEN** the TUI resolves its entry connection
- **THEN** it finds or auto-adds a matching OpenClaw connection

#### Scenario: Fall through to a single stored connection

- **GIVEN** no relevant env var is set and no saved `defaultId` resolves
- **AND** exactly one connection is stored
- **WHEN** the entry connection is resolved
- **THEN** that single connection is auto-picked

#### Scenario: No match opens the picker

- **GIVEN** no env var, no resolvable `defaultId`, and not exactly one stored connection
- **WHEN** the entry connection is resolved
- **THEN** the connections picker opens

#### Scenario: Typo env URL leaves no ghost entry

- **GIVEN** an auto-added connection from a mistyped env URL
- **WHEN** the connect fails
- **THEN** the in-memory auto-add is not persisted to `~/.lucinate/connections.json`

### Requirement: `chat --connection` short-circuits the decision tree

`lucinate chat --connection <name>` SHALL short-circuit the startup decision tree: the named connection SHALL be resolved against the store directly (ID first, then case-insensitive name), and a miss SHALL be a hard error rather than falling through to the env-var / default-id path. With `--connection` unset, `chat` SHALL run the same `ResolveEntryConnection` the bare invocation does. See the `chat-launch` spec for the override plumbing.

#### Scenario: Named connection resolved directly

- **GIVEN** `lucinate chat --connection <name>` is invoked
- **WHEN** the connection is resolved
- **THEN** it is matched against the store by ID first, then by case-insensitive name

#### Scenario: Unknown named connection is a hard error

- **GIVEN** `--connection <name>` names a connection that is not in the store
- **WHEN** resolution runs
- **THEN** it fails as a hard error rather than falling through to the env-var / default-id path

#### Scenario: No override uses the normal tree

- **GIVEN** `--connection` is unset
- **WHEN** `chat` starts
- **THEN** it runs the same `ResolveEntryConnection` as the bare invocation

### Requirement: Connection lifecycle in managed mode

The TUI SHALL own the connection lifecycle in managed mode (`AppOptions.Store != nil`):

- A successful connect SHALL call `OnBackendChanged(backend)` so the app driver in `app/app.go` rewires the events pump and supervisor onto the new backend.
- Picking a different connection mid-session (`/connections`, the picker action, or any other `showConnectionsMsg`) SHALL publish `nil` to the driver, which closes the active backend before binding to whatever the next pick produces.
- The driver SHALL own Close — TUI code SHALL NOT close the backend itself once it's published.

#### Scenario: Successful connect rewires the driver

- **WHEN** a connect succeeds in managed mode
- **THEN** `OnBackendChanged(backend)` is called and the app driver rewires the events pump and supervisor onto the new backend

#### Scenario: Switching connection closes the old backend

- **GIVEN** an active backend in managed mode
- **WHEN** the user picks a different connection (via `/connections`, the picker action, or any `showConnectionsMsg`)
- **THEN** `nil` is published to the driver, which closes the active backend before binding to the next pick

### Requirement: Legacy mode for native-platform embedders

Legacy mode (`AppOptions.Backend != nil`, no `Store`) SHALL be for native-platform embedders that manage the connection elsewhere. In legacy mode the connections picker and `/connections` SHALL be unavailable, and Close SHALL be the caller's responsibility.

#### Scenario: Legacy mode hides connection management

- **GIVEN** the app is started with `AppOptions.Backend != nil` and no `Store`
- **THEN** the connections picker and `/connections` are unavailable
- **AND** closing the backend is the caller's responsibility

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

### Requirement: `/status` works on every backend

`/status` SHALL work on every backend, with each one populating only the sub-blocks of `BackendStatus` that apply: OpenClaw fills the gateway-health block, OpenAI fills agent-count and `history.jsonl` stats, Hermes fills the lastResponseID thread block. See the per-backend "Status payload" sections in the `backend-openclaw`, `backend-openai`, and `backend-hermes` specs.

#### Scenario: Each backend fills only its applicable status blocks

- **WHEN** `/status` runs on a connection
- **THEN** OpenClaw fills the gateway-health block, OpenAI fills agent-count and `history.jsonl` stats, and Hermes fills the lastResponseID thread block

### Requirement: Agent-management capability gates picker affordances

The `Capabilities.AgentManagement` flag SHALL gate both the picker's "new agent" affordance and its "delete agent" affordance. OpenClaw and OpenAI SHALL opt in (the user creates and deletes agents via the picker). Hermes SHALL leave it false because profiles are configured server-side via `hermes profile create` on the host, so both buttons SHALL be hidden on Hermes connections; `Backend.DeleteAgent` and `Backend.CreateAgent` SHALL both reject defensively if reached.

#### Scenario: Hermes hides agent management

- **GIVEN** a Hermes connection with `Capabilities.AgentManagement` false
- **THEN** the picker's "new agent" and "delete agent" buttons are hidden
- **AND** `Backend.DeleteAgent` and `Backend.CreateAgent` reject defensively if reached

#### Scenario: OpenClaw and OpenAI expose agent management

- **GIVEN** an OpenClaw or OpenAI connection
- **THEN** the "new agent" and "delete agent" affordances are shown so the user creates and deletes agents via the picker

### Requirement: Auth-recovery modals routed by the connecting view

`Connect` errors SHALL be routed into modal sub-states by the connecting view (`internal/tui/connecting.go`):

- `gateway token mismatch` → device-token modal: clear / reset identity / cancel. `ClearToken` and `ResetIdentity` come from `DeviceTokenAuth`.
- `gateway token missing` → device-token text prompt; submission stores via `DeviceTokenAuth.StoreToken`.
- `api key required` (HTTP 401/403 from any `/v1` request) → API-key text prompt; submission stores via `APIKeyAuth.StoreAPIKey`.

The submission flow SHALL dispatch on `connectingModel.authNeed` rather than on Go interface assertion: the OpenClaw wrapper implements both `DeviceTokenAuth` and `APIKeyAuth`, so a naive type-switch would always pick the first arm. See `connecting.go` `case "enter":` for the dispatch.

#### Scenario: Token mismatch opens the device-token modal

- **GIVEN** `Connect` fails with `gateway token mismatch`
- **WHEN** the connecting view routes it
- **THEN** the device-token modal offers clear / reset identity / cancel, with `ClearToken` and `ResetIdentity` from `DeviceTokenAuth`

#### Scenario: API key required prompts and stores the key

- **GIVEN** a `/v1` request returns HTTP 401/403 (`api key required`)
- **WHEN** the connecting view routes it
- **THEN** an API-key text prompt is shown and submission stores the key via `APIKeyAuth.StoreAPIKey`

#### Scenario: Dispatch avoids the ambiguous type-switch

- **GIVEN** the OpenClaw wrapper implements both `DeviceTokenAuth` and `APIKeyAuth`
- **WHEN** a modal submission is handled
- **THEN** it dispatches on `connectingModel.authNeed` rather than a Go interface assertion, so the correct arm is chosen

### Requirement: Secrets storage for API keys

API keys SHALL live at `~/.lucinate/secrets/secrets.json` (mode 0600), keyed by connection ID. `config.GetAPIKey(connID)` and `config.SetAPIKey(connID, key)` SHALL be the public surface. `LUCINATE_OPENAI_API_KEY` SHALL fall back when no per-connection key is stored.

The `secretAwareOpenAIBackend` and `secretAwareHermesBackend` shims in `app/factory.go` SHALL wrap their respective concrete backends so `StoreAPIKey` writes through to `~/.lucinate/secrets/secrets.json` during the auth-modal resolution path; the next launch SHALL reuse the key without re-prompting.

A future enhancement is to back this with the OS keychain (Keychain on macOS, libsecret on Linux, Credential Manager on Windows) and fall back to the JSON file when no keychain is available — kept on disk for now to avoid platform-specific dependencies on first run.

#### Scenario: API key written with restricted permissions

- **WHEN** an API key is stored
- **THEN** it is written to `~/.lucinate/secrets/secrets.json` (mode 0600), keyed by connection ID

#### Scenario: Env fallback when no per-connection key

- **GIVEN** no per-connection API key is stored
- **WHEN** an OpenAI-compatible connection needs a key
- **THEN** `LUCINATE_OPENAI_API_KEY` is used as a fallback

#### Scenario: Stored key reused on next launch

- **GIVEN** an API key stored through the `secretAwareOpenAIBackend` or `secretAwareHermesBackend` shim during auth-modal resolution
- **WHEN** the app launches again
- **THEN** the key is reused without re-prompting

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

### Requirement: Type radio, focus behaviour, and immutable type on edit

The type radio SHALL render vertically (one preset per line) so the list reflows cleanly on narrow terminals. ↑/↓ SHALL cycle the presets when the radio is focused; Tab SHALL move between fields. Edit forms SHALL drop the radio entirely (type is immutable post-create) and SHALL start focus on the name field. Edited connections SHALL show the persisted type as a dimmed read-only label — Ollama-created connections SHALL render as "OpenAI-compatible" on edit because the Ollama preset isn't distinguishable post-save.

#### Scenario: Navigating the create form

- **GIVEN** the create form with the type radio focused
- **WHEN** the user presses ↑/↓
- **THEN** the presets cycle, and Tab moves focus between fields

#### Scenario: Editing an existing connection

- **GIVEN** an existing connection is edited
- **THEN** the radio is dropped, type is immutable, focus starts on the name field, and the persisted type shows as a dimmed read-only label

#### Scenario: Ollama connection labelled on edit

- **GIVEN** a connection created from the Ollama preset (persisted type `openai`)
- **WHEN** it is edited
- **THEN** it renders as "OpenAI-compatible" because the Ollama preset isn't distinguishable post-save

### Requirement: Delete confirmation as a sub-state

Delete confirmation SHALL be exposed as a sub-state with confirm/cancel pairs in `Actions()`, so native-platform embedders render them as buttons rather than relying on inline `y/n` keys.

#### Scenario: Delete confirmation exposed for embedders

- **WHEN** the user initiates deleting a connection
- **THEN** the confirm/cancel choices are exposed as a sub-state via `Actions()`
- **AND** native-platform embedders render them as buttons rather than inline `y/n` keys
