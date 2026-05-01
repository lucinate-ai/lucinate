# Connections and backends

A connection is a saved target lucinate can connect to (URL + type + auth identity). The list is persisted to `~/.lucinate/connections.json` and managed through the connections picker (`viewConnections`) — accessible as the entry view on first run, via `/connections` mid-session, or via the **Connections** action on the agent picker.

## Connection types

Three backend types ship today; all implement `backend.Backend` (`internal/backend/backend.go`) so the chat / sessions / commands views are backend-agnostic. The OpenAI and Hermes backends both speak HTTP+SSE+JSON over Bearer-token auth and share the request builder, SSE scanner, and event emitter in `internal/backend/httpcommon` — but they're sibling implementations, not a base class and a subclass. Backend-specific behaviour is documented in dedicated files:

- [backend_openclaw.md](backend_openclaw.md) — full capability surface, device-token auth, server-side agents
- [backend_openai.md](backend_openai.md) — `/v1/chat/completions` streaming, on-disk agents (IDENTITY.md + SOUL.md), API-key auth
- [backend_hermes.md](backend_hermes.md) — `/v1/responses` server-state chaining, one synthetic agent per connection, API-key auth

| Type      | URL shape                                                   | Auth                                   | Agent storage                                                                  |
|-----------|-------------------------------------------------------------|----------------------------------------|--------------------------------------------------------------------------------|
| OpenClaw  | `https://`/`http://`/`wss://`/`ws://` (WS endpoint derived) | Ed25519 device pairing                 | Server-side on the gateway                                                     |
| OpenAI    | `http(s)://host/v1`                                         | Optional `Authorization: Bearer <key>` | Local under `~/.lucinate/agents/<conn-id>/<agent-id>/`                         |
| Hermes    | `http(s)://host:8642/v1` (default loopback)                 | `Authorization: Bearer <API_SERVER_KEY>` | Server-side in the Hermes profile; only `last_response_id` + prompts log local |

`AllConnectionTypes` (`internal/config/connections.go`) drives the picker form's type radio. Adding a fourth backend means: implementing `backend.Backend`, extending the enum, dispatching in `DefaultBackendFactory` (`app/factory.go`), adjusting the form's type-conditional rendering, and writing a `backend_<name>.md` doc for it.

## Startup decision tree

`config.ResolveEntryConnection()` (`internal/config/startup.go`) decides what the TUI's entry view is:

1. If `OPENCLAW_GATEWAY_URL` is set, find or auto-add a matching OpenClaw connection.
2. Else if `LUCINATE_OPENAI_BASE_URL` is set, find or auto-add a matching OpenAI connection (with `LUCINATE_OPENAI_DEFAULT_MODEL` if provided).
3. Else if a saved `defaultId` resolves to a known entry → use it (last-used = default).
4. Else if exactly one connection is stored → auto-pick it.
5. Else → open the connections picker.

Auto-add (steps 1–2) mutates the in-memory store but does **not** persist it until a successful connect, so a typo in the env URL doesn't accumulate ghost entries.

## Connection lifecycle

The TUI owns the lifecycle in managed mode (`AppOptions.Store != nil`):

- A successful connect calls `OnBackendChanged(backend)` so the app driver in `app/app.go` rewires the events pump and supervisor onto the new backend.
- Picking a different connection mid-session (`/connections`, the picker action, or any other `showConnectionsMsg`) publishes `nil` to the driver, which closes the active backend before binding to whatever the next pick produces.
- The driver owns Close — TUI code never closes the backend itself once it's published.

Legacy mode (`AppOptions.Backend != nil`, no `Store`) is for native-platform embedders that manage the connection elsewhere; the connections picker and `/connections` are unavailable, and Close is the caller's responsibility.

## Capability negotiation

`backend.Backend.Capabilities()` reports a `Capabilities` struct (`internal/backend/backend.go`); the TUI type-asserts against optional sub-interfaces (`StatusBackend`, `ExecBackend`, `CompactBackend`, `ThinkingBackend`, `UsageBackend`, `CronBackend`, `DeviceTokenAuth`, `APIKeyAuth`) at the relevant call sites. OpenClaw implements all of them. OpenAI and Hermes implement only `APIKeyAuth` — backend-only commands (`/status`, `/compact`, `/think`, `/stats`, `/crons`, `!!`) render a "not available on this connection" system message instead of erroring.

The `Capabilities.AgentManagement` flag gates both the picker's "new agent" affordance and its "delete agent" affordance. OpenClaw and OpenAI opt in (the user creates and deletes agents via the picker). Hermes leaves it false because profiles are configured server-side via `hermes profile create` on the host, so both buttons are hidden on Hermes connections; `Backend.DeleteAgent` and `Backend.CreateAgent` both reject defensively if reached.

## Auth-recovery modals

`Connect` errors are routed into modal sub-states by the connecting view (`internal/tui/connecting.go`):

- `gateway token mismatch` → device-token modal: clear / reset identity / cancel. `ClearToken` and `ResetIdentity` come from `DeviceTokenAuth`.
- `gateway token missing` → device-token text prompt; submission stores via `DeviceTokenAuth.StoreToken`.
- `api key required` (HTTP 401/403 from any `/v1` request) → API-key text prompt; submission stores via `APIKeyAuth.StoreAPIKey`.

The submission flow dispatches on `connectingModel.authNeed` rather than on Go interface assertion: the OpenClaw wrapper implements both `DeviceTokenAuth` and `APIKeyAuth`, so a naive type-switch would always pick the first arm. See `connecting.go` `case "enter":` for the dispatch.

## Secrets storage

API keys live at `~/.lucinate/secrets/secrets.json` (mode 0600), keyed by connection ID. `config.GetAPIKey(connID)` and `config.SetAPIKey(connID, key)` are the public surface. `LUCINATE_OPENAI_API_KEY` falls back when no per-connection key is stored.

The `secretAwareBackend` shim in `main.go` wraps `*openai.Backend` so `StoreAPIKey` writes through to disk during the auth-modal resolution path; the next launch reuses the key without re-prompting.

A future enhancement is to back this with the OS keychain (Keychain on macOS, libsecret on Linux, Credential Manager on Windows) and fall back to the JSON file when no keychain is available — kept on disk for now to avoid platform-specific dependencies on first run.

## Connections form

`internal/tui/connections.go` implements the picker. The form has a fixed type radio plus type-conditional fields:

| Preset   | Persisted Type | Fields                                                     |
|----------|----------------|------------------------------------------------------------|
| OpenClaw | `openclaw`     | Type, Name, Gateway URL                                    |
| OpenAI   | `openai`       | Type, Name, Base URL, Default model (optional)             |
| Ollama   | `openai`       | Type, Name, Base URL, Default model (optional)             |
| Hermes   | `hermes`       | Type, Name, Base URL, Profile name                         |

The picker offers four presets but only three persisted types — Ollama is an opinionated OpenAI preset that pre-fills `Name = ollama` and `Base URL = http://localhost:11434/v1`. Hermes is its own type with a pre-filled `Base URL = http://127.0.0.1:8642/v1` and a "Profile name" model field. Switching to either preset and back clears the prefill so the user isn't stranded with the wrong localhost URL in a gateway field.

The type radio renders vertically (one preset per line) so the list reflows cleanly on narrow terminals. ↑/↓ cycles the presets when the radio is focused; Tab moves between fields. Edit forms drop the radio entirely (type is immutable post-create) and start focus on the name field. Edited connections show the persisted type as a dimmed read-only label — Ollama-created connections render as "OpenAI-compatible" on edit because the Ollama preset isn't distinguishable post-save.

Delete confirmation is exposed as a sub-state with confirm/cancel pairs in `Actions()`, so native-platform embedders render them as buttons rather than relying on inline `y/n` keys.
