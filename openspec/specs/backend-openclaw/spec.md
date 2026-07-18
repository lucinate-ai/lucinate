# OpenClaw Backend Specification

## Purpose

The OpenClaw backend (`internal/backend/openclaw`) is a thin adapter over `*client.Client` from the existing OpenClaw SDK. Every TUI call site that used to hold a `*client.Client` now holds a `backend.Backend`; the OpenClaw concrete type is recovered via type assertion at the few sites that still need gateway-only affordances. This spec covers the backend's declared capabilities, its connect and auth pass-throughs, the agent and session model, skill-catalogue injection, the pass-through method surface, and the cross-backend status payload. See the `connections` spec for the cross-backend connection lifecycle and the `authentication` spec for device pairing.

## Requirements

### Requirement: Full capability surface

`Backend.Capabilities()` SHALL report the full surface — every optional sub-interface SHALL be implemented. The capabilities and their use sites are:

| Capability        | Use site                                  |
|-------------------|-------------------------------------------|
| `GatewayStatus`   | `/status`                                 |
| `RemoteExec`      | `!!` — see the `shell-execution` spec     |
| `SessionCompact`  | `/compact`                                |
| `Thinking`        | `/think`                                  |
| `SessionUsage`    | `/stats`                                  |
| `Cron`            | `/crons` — see the `crons` spec           |
| `AuthRecovery`    | `AuthRecoveryDeviceToken` — see the connect-and-auth requirement |
| `AgentWorkspace`  | Workspace field on the create-agent form  |

#### Scenario: Every optional sub-interface is implemented

- **WHEN** `Backend.Capabilities()` is queried
- **THEN** it reports `GatewayStatus`, `RemoteExec`, `SessionCompact`, `Thinking`, `SessionUsage`, `Cron`, `AuthRecovery`, and `AgentWorkspace`
- **AND** each maps to its use site (`/status`, `!!`, `/compact`, `/think`, `/stats`, `/crons`, `AuthRecoveryDeviceToken`, and the create-agent form's workspace field)

### Requirement: Connect and auth pass-through with modal auth recovery

`Connect`, `Close`, `Events`, and `Supervise` SHALL be pass-throughs to the underlying client. The TUI's connecting view SHALL route auth failures into modal sub-states:

- `NOT_PAIRED` → pairing-required modal: instructions to approve the device on the gateway host, then Enter to retry.
- `gateway token mismatch` → device-token modal: clear / reset identity / cancel. `ClearToken` and `ResetIdentity` come from the `DeviceTokenAuth` sub-interface.
- `gateway token missing` → device-token text prompt; submission stores via `StoreToken`.

Tokens and the Ed25519 device identity SHALL live under `~/.lucinate/identity/<endpoint>/`, isolated per gateway endpoint. The first successful connect after a fresh approval SHALL re-dial transparently so the surviving connection authenticates with the issued token — see the `authentication` spec for the full pairing flow and the re-dial rationale.

#### Scenario: Auth failures route into modal sub-states

- **GIVEN** a connect attempt fails with a recognised auth error
- **WHEN** the connecting view handles it
- **THEN** `NOT_PAIRED` opens the pairing-required modal, `gateway token mismatch` opens the device-token modal (clear / reset identity / cancel via `DeviceTokenAuth`'s `ClearToken` and `ResetIdentity`), and `gateway token missing` opens a text prompt that stores the token via `StoreToken`

#### Scenario: Transparent re-dial after fresh approval

- **GIVEN** a device has just been approved on the gateway host
- **WHEN** the first successful connect completes
- **THEN** the backend re-dials transparently so the surviving connection authenticates with the issued token stored under `~/.lucinate/identity/<endpoint>/`

### Requirement: Agent and session model owned by the gateway

Agents SHALL be owned by the gateway. `ListAgents` and `CreateAgent` (with name + workspace) SHALL call straight through. Sessions SHALL be created server-side and identified by the gateway-issued session key. The gateway SHALL seed an `IDENTITY.md` file in the agent's workspace on creation; lucinate SHALL NOT author it.

`DeleteAgent` SHALL forward to `Client.DeleteAgent(ctx, agentID, deleteFiles)`, which sends `protocol.AgentsDeleteParams{AgentID, DeleteFiles: &flag}` over the wire. The `*bool` SHALL always be populated explicitly from the picker's keep-vs-delete-files toggle (see the `agents` spec, deleting-an-agent) — the gateway's implicit "preserve files" default MUST never apply. When `deleteFiles=false` the gateway SHALL drop bindings but leave the agent's workspace files in place; when true the workspace SHALL be wiped along with the bindings.

#### Scenario: Agents and sessions call through to the gateway

- **WHEN** `ListAgents` or `CreateAgent` (name + workspace) is invoked
- **THEN** it calls straight through to the gateway, which owns the agents
- **AND** sessions are created server-side and identified by the gateway-issued session key
- **AND** the gateway seeds `IDENTITY.md` in the agent's workspace, which lucinate does not author

#### Scenario: Delete agent always sends an explicit files flag

- **GIVEN** the picker's keep-vs-delete-files toggle
- **WHEN** `DeleteAgent` forwards to `Client.DeleteAgent(ctx, agentID, deleteFiles)`
- **THEN** it sends `protocol.AgentsDeleteParams{AgentID, DeleteFiles: &flag}` with the `*bool` populated explicitly, so the gateway's implicit "preserve files" default never applies

#### Scenario: Delete with files preserved versus wiped

- **WHEN** `deleteFiles=false`
- **THEN** the gateway drops bindings but leaves the agent's workspace files in place
- **AND** **WHEN** `deleteFiles=true`, the workspace is wiped along with the bindings

### Requirement: Skill catalogue injection on the first turn

The chat layer SHALL pass the active skill catalogue through `ChatSendParams.Skills`. The backend SHALL prepend a `System:`-prefixed block — `Available agent skills (activate with /skill-name): …` — to the first turn of each session via `takePendingCatalog(sessionKey, skills)`. After the first turn, `catalogSent[sessionKey] = true` and subsequent sends SHALL omit the block.

The check-and-mark SHALL be mutex-guarded so two concurrent sends on the same session cannot both emit the catalogue. Every line of the block SHALL be prefixed with `System:` so the gateway's prompt assembler can identify it as a session-level system block (retained server-side across turns) and so `stripSystemLines` on the client side hides it from the visible transcript on history refresh.

#### Scenario: Catalogue prepended once per session

- **GIVEN** a session's first turn with an active skill catalogue passed via `ChatSendParams.Skills`
- **WHEN** the send is processed
- **THEN** the backend prepends the `System:`-prefixed `Available agent skills (activate with /skill-name): …` block via `takePendingCatalog(sessionKey, skills)`
- **AND** sets `catalogSent[sessionKey] = true` so subsequent sends omit the block

#### Scenario: Concurrent sends cannot double-emit the catalogue

- **GIVEN** two concurrent sends on the same session
- **WHEN** the mutex-guarded check-and-mark runs
- **THEN** at most one send emits the catalogue block

#### Scenario: System-prefixed lines retained server-side and hidden client-side

- **GIVEN** every line of the catalogue block is prefixed with `System:`
- **WHEN** the gateway's prompt assembler processes it
- **THEN** it treats the block as a session-level system block retained server-side across turns
- **AND** `stripSystemLines` on the client side hides it from the visible transcript on history refresh

### Requirement: Pass-through method surface

`SessionsList`, `CreateSession`, `SessionDelete`, `ChatSend`, `ChatAbort`, `ChatHistory`, `ModelsList`, `SessionPatchModel`, and the capability-specific methods (`ExecRequest`, `ExecResolve`, `SessionCompact`, `SessionPatchThinking`, `SessionUsage`, `CronsList`, `CronRuns`, `CronAdd`, `CronUpdate`, `CronUpdateRaw`, `CronRemove`, `CronRun`) SHALL all forward to the underlying client unchanged. The adapter exists to satisfy the `backend.Backend` interface, not to add behaviour.

#### Scenario: Methods forward unchanged

- **WHEN** any of `SessionsList`, `CreateSession`, `SessionDelete`, `ChatSend`, `ChatAbort`, `ChatHistory`, `ModelsList`, `SessionPatchModel`, `ExecRequest`, `ExecResolve`, `SessionCompact`, `SessionPatchThinking`, `SessionUsage`, `CronsList`, `CronRuns`, `CronAdd`, `CronUpdate`, `CronUpdateRaw`, `CronRemove`, or `CronRun` is called
- **THEN** it forwards to the underlying client unchanged, adding no behaviour

### Requirement: Cross-backend status payload

`Status(ctx, agentID, sessionKey)` SHALL assemble the cross-backend `BackendStatus` from a single gateway `/health` round-trip plus accessors on `*client.Client`:

- Common header — `Type: "openclaw"`, the gateway WebSocket URL, and `"device token"` / `"anonymous"` derived from whether a token is loaded for this endpoint.
- `Gateway` block — the health snapshot (sessions, agents, channels), the gateway version and uptime from the connect handshake, and the negotiated protocol version bounded by `protocol.MinProtocolVersion` and `protocol.ProtocolVersion` (the same pair advertised in `ConnectParams.MinProtocol`/`MaxProtocol`, so the displayed range matches what the handshake actually negotiated against).

If the health fetch fails, `Status` SHALL still return a populated payload with `Gateway.Health = nil` and SHALL surface the error to the caller; the TUI SHALL render the body and the error as separate system messages rather than swallowing either.

`agentID` and `sessionKey` SHALL be ignored — OpenClaw's per-session state lives on the gateway and is already reflected in the health snapshot.

#### Scenario: Status assembled from a single health round-trip

- **WHEN** `Status(ctx, agentID, sessionKey)` is called
- **THEN** it assembles `BackendStatus` from one gateway `/health` round-trip plus `*client.Client` accessors
- **AND** the common header carries `Type: "openclaw"`, the gateway WebSocket URL, and `"device token"` or `"anonymous"` per whether a token is loaded for this endpoint
- **AND** the `Gateway` block carries the health snapshot (sessions, agents, channels), the gateway version and uptime from the connect handshake, and the protocol version bounded by `protocol.MinProtocolVersion` and `protocol.ProtocolVersion` (matching `ConnectParams.MinProtocol`/`MaxProtocol`)

#### Scenario: Health fetch failure still returns a payload

- **WHEN** the `/health` fetch fails
- **THEN** `Status` returns a populated payload with `Gateway.Health = nil` and surfaces the error to the caller
- **AND** the TUI renders the body and the error as separate system messages rather than swallowing either

#### Scenario: Agent and session identifiers are ignored

- **GIVEN** `agentID` and `sessionKey` arguments
- **WHEN** `Status` runs
- **THEN** both are ignored, because OpenClaw's per-session state lives on the gateway and is already reflected in the health snapshot
