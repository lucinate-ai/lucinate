# Agent picker

The agent picker (`selectModel` in `internal/tui/select.go`) is shown after a connection is established — either directly on startup when the connection is unambiguous, or after the user picks one from the connections picker (see [connections.md](connections.md)). It loads the list of configured agents and either presents a selection UI or auto-selects when only one agent exists. In managed mode the picker also surfaces a **Connections** action (key `c`) so the user can switch connection without first entering chat.

Agents come from the active backend (`backend.Backend.ListAgents`). For OpenClaw, that's the gateway's agent list. For OpenAI-compatible connections, agents are local — see [connections.md](connections.md#openai-agent-storage). For Hermes connections the list is one synthetic entry (the connected profile is the agent — see [backend_hermes.md](backend_hermes.md#one-profile-one-agent)) and the create-agent affordance is hidden via `Capabilities.AgentManagement`.

## Loading agents

`loadAgents()` calls `client.ListAgents()` and returns an `agentsLoadedMsg`. Each agent is an `agentItem` wrapping a `protocol.AgentSummary`. The list is displayed using a Bubble Tea list component with a custom delegate that shows the agent name (falling back to ID if no name is set).

## Auto-selection

If exactly one agent is returned, it is selected automatically without user interaction and a session is created immediately. The same auto-select fires after creating a new agent — the picker bypasses the list and proceeds straight to chat.

## Selecting an agent

Pressing Enter on a highlighted agent calls `client.CreateSession(agentID, key)`. On success, `sessionCreatedMsg` carries the new session key and the app transitions to the chat view (`newChatModel(...)`). See [sessions.md](sessions.md) for the session lifecycle from this point.

## Creating an agent

Pressing `n` in the picker switches to a creation form (`subStateCreate`). The form's shape is driven by the active backend's `Capabilities.AgentWorkspace` flag (see `backend.Capabilities`).

**OpenClaw** (workspace-aware):

- **Name** — must start with a lowercase letter and contain only alphanumeric characters and hyphens. Validated on submit.
- **Workspace** — a filesystem path that is auto-suggested but editable.

On submit, `Backend.CreateAgent` is called with both fields. The gateway creates the agent and seeds an `IDENTITY.md` file in the workspace.

**OpenAI-compatible** (local-agent backends):

- **Name** — same validation rules as above.
- The workspace field is hidden. On submit, the backend seeds `IDENTITY.md` and `SOUL.md` with defaults under `~/.lucinate/agents/<connection>/<agent>/`. Users edit those files on disk to customise the agent's identity and behaviour — see [connections.md](connections.md#openai-agent-storage).

On success the agent list is reloaded and the new agent is auto-selected (see above). On failure the form stays open and the error is shown so the user can correct and retry.
