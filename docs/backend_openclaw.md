# OpenClaw backend

The OpenClaw backend (`internal/backend/openclaw`) is a thin adapter over `*client.Client` from the existing OpenClaw SDK. Every TUI call site that used to hold a `*client.Client` now holds a `backend.Backend`; the OpenClaw concrete type is recovered via type assertion at the few sites that still need gateway-only affordances.

See [connections.md](connections.md) for the cross-backend connection lifecycle and [authentication.md](authentication.md) for device pairing.

## Capabilities

`Backend.Capabilities()` reports the full surface — every optional sub-interface is implemented:

| Capability        | Use site                                  |
|-------------------|-------------------------------------------|
| `GatewayStatus`   | `/status`                                 |
| `RemoteExec`      | `!!` — see [shell-execution.md](shell-execution.md) |
| `SessionCompact`  | `/compact`                                |
| `Thinking`        | `/think`                                  |
| `SessionUsage`    | `/stats`                                  |
| `Cron`            | `/crons` — see [crons.md](crons.md)       |
| `AuthRecovery`    | `AuthRecoveryDeviceToken` — see below     |
| `AgentWorkspace`  | Workspace field on the create-agent form  |

## Connect and auth

`Connect`, `Close`, `Events`, and `Supervise` are pass-throughs to the underlying client. The TUI's connecting view routes auth failures into modal sub-states:

- `gateway token mismatch` → device-token modal: clear / reset identity / cancel. `ClearToken` and `ResetIdentity` come from the `DeviceTokenAuth` sub-interface.
- `gateway token missing` → device-token text prompt; submission stores via `StoreToken`.

Tokens and the Ed25519 device identity live under `~/.lucinate/identity/<endpoint>/`, isolated per gateway endpoint — see [authentication.md](authentication.md) for the full pairing flow.

## Agent and session model

Agents are owned by the gateway. `ListAgents` and `CreateAgent` (with name + workspace) call straight through. Sessions are created server-side and identified by the gateway-issued session key.

The gateway seeds an `IDENTITY.md` file in the agent's workspace on creation; lucinate does not author it.

## Skill catalog injection

The chat layer passes the active skill catalog through `ChatSendParams.Skills`. The backend prepends a `System:`-prefixed block — `Available agent skills (activate with /skill-name): …` — to the first turn of each session via `takePendingCatalog(sessionKey, skills)`. After the first turn, `catalogSent[sessionKey] = true` and subsequent sends omit the block.

The check-and-mark is mutex-guarded so two concurrent sends on the same session can't both emit the catalog. Every line of the block is prefixed with `System:` so the gateway's prompt assembler can identify it as a session-level system block (retained server-side across turns) and so `stripSystemLines` on the client side hides it from the visible transcript on history refresh.

## Pass-through methods

`SessionsList`, `CreateSession`, `SessionDelete`, `ChatSend`, `ChatAbort`, `ChatHistory`, `ModelsList`, `SessionPatchModel`, and the capability-specific methods (`GatewayHealth`, `ExecRequest`, `ExecResolve`, `SessionCompact`, `SessionPatchThinking`, `SessionUsage`, `CronsList`, `CronRuns`, `CronAdd`, `CronUpdate`, `CronUpdateRaw`, `CronRemove`, `CronRun`) all forward to the underlying client unchanged. The adapter exists to satisfy the `backend.Backend` interface, not to add behaviour.
