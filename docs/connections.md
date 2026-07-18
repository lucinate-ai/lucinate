# Connections and backends — lessons and rationale

The behavioural contract for connections and backends lives in
[`openspec/specs/connections/spec.md`](../openspec/specs/connections/spec.md) — the connection
types, the picker and form, the startup decision tree, the connection lifecycle, capability
negotiation, auth-recovery modals, and secrets storage are all captured there as requirements
and scenarios. This file keeps the hard-won lessons, pitfalls, and design rationale behind that
contract: the "why it works this odd way" that the spec's requirements don't dwell on.

## Sibling backends, not a base class and a subclass

The OpenAI and Hermes backends both speak HTTP+SSE+JSON over Bearer-token auth and share the
request builder, SSE scanner, and event emitter in `internal/backend/httpcommon` — but they're
sibling implementations, not a base class and a subclass. The shared plumbing is a convenience,
not an inheritance hierarchy, and treating it as one leads you astray when their request/response
shapes diverge (`/v1/chat/completions` vs `/v1/responses` server-state chaining).

## Why the factory switch is enough to reach a new backend everywhere

`DefaultBackendFactory` (`app/factory.go`) has two non-TUI consumers, and both are wired so that
landing a new connection type in the factory's switch is all it takes to reach them. `app.Send`
(`lucinate send`) calls the factory directly for one-shot dispatch, so a new type is automatically
reachable from the scripted CLI mode without further wiring. `app.Chat` (`lucinate chat`) defers
all connect / list / create work to the TUI and just packs overrides into `RunOptions`, so a new
type is reachable there as soon as it works in the regular TUI. Worth knowing before you go
hunting for extra plumbing to add.

## Auto-add doesn't persist until a successful connect

In the startup decision tree, auto-add from `OPENCLAW_GATEWAY_URL` / `LUCINATE_OPENAI_BASE_URL`
mutates the in-memory store but does **not** persist it until a successful connect. This is
deliberate: a typo in the env URL would otherwise accumulate ghost entries in
`~/.lucinate/connections.json` on every launch.

## The driver owns Close

In managed mode the driver, not the TUI, owns closing the backend once it's published. Picking a
different connection mid-session publishes `nil` to the driver, which closes the active backend
before binding to the next pick. TUI code never closes the backend itself. Keeping Close in one
place avoids two paths racing to tear down the same connection.

## Auth-modal dispatch is on `authNeed`, not interface assertion

The connecting view (`internal/tui/connecting.go`) dispatches modal submissions on
`connectingModel.authNeed` rather than on a Go interface assertion. The trap: the OpenClaw wrapper
implements **both** `DeviceTokenAuth` and `APIKeyAuth`, so a naive type-switch would always pick
the first arm and store the wrong kind of credential. See `connecting.go` `case "enter":` for the
dispatch.

## Hermes leaves agent management off on purpose

`Capabilities.AgentManagement` gates both the "new agent" and "delete agent" affordances. Hermes
leaves it false because profiles are configured server-side via `hermes profile create` on the
host — there's nothing for the picker to create or delete. As defence in depth `Backend.CreateAgent`
and `Backend.DeleteAgent` both reject if ever reached, so a stray call can't half-create a profile
the picker was never meant to touch.

## Secrets are on disk for now

API keys live at `~/.lucinate/secrets/secrets.json` (mode 0600). The `secretAwareOpenAIBackend`
and `secretAwareHermesBackend` shims in `app/factory.go` wrap their concrete backends so
`StoreAPIKey` writes through to that file during the auth-modal resolution path, letting the next
launch reuse the key without re-prompting. The shim exists so the concrete backends stay unaware
of on-disk storage — the write-through is bolted on at the factory rather than baked into each
backend.

A future enhancement is to back this with the OS keychain (Keychain on macOS, libsecret on Linux,
Credential Manager on Windows) and fall back to the JSON file when no keychain is available — kept
on disk for now to avoid platform-specific dependencies on first run.

## Switching presets clears the localhost prefill

The Ollama and Hermes presets pre-fill a localhost Base URL. Switching to either preset and back
clears the prefill so the user isn't stranded with the wrong localhost URL sitting in a gateway
field. Without the clear, a half-configured form would carry a stale value that looks plausible
but points at the wrong backend.

## Ollama isn't distinguishable after save

Ollama is an opinionated OpenAI preset — it persists type `openai`, not a distinct type. The
consequence is that an Ollama-created connection renders as "OpenAI-compatible" when edited,
because there's nothing on disk that marks it as having come from the Ollama preset. Not a bug,
just a limit of treating it as a preset rather than its own persisted type.
