# Agent picker

The agent picker (`selectModel` in `internal/tui/select.go`) is shown after a connection is established — either directly on startup when the connection is unambiguous, or after the user picks one from the connections picker (see [connections.md](connections.md)). It loads the list of configured agents and either presents a selection UI or auto-selects when only one agent exists. In managed mode the picker also surfaces a **Connections** action (key `c`) so the user can switch connection without first entering chat, and a **Config** action (key `,`, gated on the same managed-mode flag) that emits `showConfigMsg{}` to open the preferences screen.

Agents come from the active backend (`backend.Backend.ListAgents`). For OpenClaw, that's the gateway's agent list. For OpenAI-compatible connections, agents are local — see [connections.md](connections.md#openai-agent-storage). For Hermes connections the list is one synthetic entry (the connected profile is the agent — see [backend_hermes.md](backend_hermes.md#one-profile-one-agent)) and the create-agent affordance is hidden via `Capabilities.AgentManagement`.

## Loading agents

`loadAgents()` calls `client.ListAgents()` and returns an `agentsLoadedMsg`. Each agent is an `agentItem` wrapping a `protocol.AgentSummary`. The list is displayed using a Bubble Tea list component with a custom delegate that shows the agent name (falling back to ID if no name is set).

## Filtering

The picker enables the Bubble Tea list's built-in filtering with the same fuzzy matcher (`fuzzyFilter`) as the model picker (`internal/tui/models.go`). Press `/` to open the filter, type to narrow, and `esc` to clear it. `agentItem.FilterValue()` concatenates the agent's name and ID, so a query matches either — typing part of a display name or part of the raw agent ID both hit the same agent.

Unlike the model picker, the agent picker starts in plain list mode rather than dropping straight into filtering: the single-letter action shortcuts (`n` new, `d` delete, `c` connections) must stay reachable, so filtering is opt-in via `/`.

- **Keystroke routing.** While the filter input is focused (`list.FilterState() == list.Filtering`), `handleKey` forwards every key except `enter` to the list so characters that collide with action shortcuts (e.g. `n`) type into the query instead of firing the action. Outside filtering, the normal action dispatch applies.
- **Enter selects from the filter.** `enter` picks the highlighted agent from any filter state, so the user can type-to-narrow and pick in one keystroke rather than first applying the filter (the bubbles default while typing). When the filter matches nothing, `enter` falls through to the list.
- **Input focus.** `selectModel.filtering()` reports whether the filter is focused; `app.go`'s `computeWantsInput()` consults it (alongside the create-agent form) so the app-level `q`-to-quit shortcut and the embedder input-focus signal treat a typed `q` as filter text rather than a quit request.

## Auto-selection

If exactly one agent is returned, it is selected automatically without user interaction and a session is created immediately. The same auto-select fires after creating a new agent — the picker bypasses the list and proceeds straight to chat.

`lucinate chat --agent <name>` is a third auto-pick driver: `selectModel.autoPickName` runs an ID-then-case-insensitive-name match on the first `agentsLoadedMsg` and selects the matching agent. It runs **before** the single-agent and post-create branches, so a `--agent` mismatch in a single-agent connection surfaces as an error banner on the picker rather than silently picking the only available agent. The override is one-shot: cleared on consume so a later `agentsLoadedMsg` (e.g. after a user-driven create) doesn't re-fire it. See [chat-launch.md](chat-launch.md) for the full override-consumption story.

## Selecting an agent

Pressing Enter on a highlighted agent calls `client.CreateSession(agentID, key)`. On success, `sessionCreatedMsg` carries the new session key and the app transitions to the chat view (`newChatModel(...)`). See [sessions.md](sessions.md) for the session lifecycle from this point.

The `/agent <name>` slash command bypasses the picker entirely and reaches the same `sessionCreatedMsg` path — see [commands.md](commands.md#agent). From the chat input, `/agent ` followed by Tab autocompletes against the cached agent list.

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

## Deleting an agent

Pressing `d` on a highlighted agent enters `subStateConfirmDelete`. The substate is gated by the same `Capabilities.AgentManagement` flag as create — Hermes connections never expose it because profiles are server-managed.

The view is deliberately loud: a red header with the agent's name, a bullet list of what's about to be removed (metadata, transcript, and on OpenClaw bindings), the local backup path, a `Delete files | Keep files` toggle, and a textinput labelled with the agent's display name.

- **Type-to-confirm.** `confirm-delete` is only emitted from `Actions()` when the typed name matches the agent's display name (case-insensitive, whitespace-trimmed) and no request is in flight. That presence-toggle is the disable mechanism for native-platform embedders — the `Action` struct has no `Enabled` flag.
- **Keep files toggle.** `tab` flips `m.keepFiles`, which becomes `!DeleteFiles` on the `backend.DeleteAgentParams` sent to the backend. It defaults to **keep files** (`keepFiles=true` → `DeleteFiles=false`), so a mistaken confirmation preserves the agent's file content — the user has to toggle off to destroy it. The view's description line switches with the toggle so the user can read what the current mode will do before pressing enter.
- **Pending state is snapshotted** (`pendingDeleteID`, `pendingDeleteName`) at substate entry from the passed `agentItem`, never re-read from `list.SelectedItem()` afterwards. A list re-render mid-flight cannot resolve the destructive cmd to the wrong agent.
- **Esc** triggers `cancel-delete` and clears all pending state. **Enter** without a matching name is a no-op.
- **Plain `d` is not bound** inside the substate because it's a printable character the user might type as part of the agent name.

`agentDeletedMsg` carries the result. On success the picker clears pending state and reloads via `loadAgents()`. On error `pendingDeleteName` is preserved so the user can retry without retyping; `deleteErr` surfaces inline. Keystrokes are ignored while `m.deleting` is true — the network call has already left.

The destructive vs preserve interpretation is per-backend:

- **OpenClaw** — `Backend.DeleteAgent` forwards to `Client.DeleteAgent(ctx, agentID, deleteFiles)`, which sends `protocol.AgentsDeleteParams{AgentID, DeleteFiles: &flag}` to the gateway. The pointer is always set explicitly — the gateway's implicit "preserve files" default never applies.
- **OpenAI-compatible** — when `DeleteFiles=true` the agent directory is wiped via `AgentStore.Delete` (`os.RemoveAll`); when false it's moved to `<root>/.archive/<id>-<unixts>/` via `AgentStore.Archive` so IDENTITY.md, SOUL.md, and history.jsonl are recoverable on disk. See [backend_openai.md](backend_openai.md#agent-storage).
- **Hermes** — `DeleteAgent` returns a clear error pointing at `hermes profile delete`. The UI gate (AgentManagement=false) means the user shouldn't reach it; the reject is defensive.
