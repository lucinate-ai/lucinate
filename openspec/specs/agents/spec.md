# Agent Picker Specification

## Purpose

The agent picker (`selectModel` in `internal/tui/select.go`) is shown after a connection is
established — either directly on startup when the connection is unambiguous, or after the user
picks one from the connections picker (see the `connections` spec). It loads the list of
configured agents from the active backend and either presents a selection UI or auto-selects
when only one agent exists. This spec covers where agents come from, how the list is loaded,
filtered, and navigated, the managed-mode and OpenClaw action affordances, auto-selection
drivers, and the create, select, and delete flows.

## Requirements

### Requirement: Picker entry and managed-mode actions

The agent picker SHALL be shown after a connection is established — either directly on startup
when the connection is unambiguous, or after the user picks one from the connections picker (see
the `connections` spec). In managed mode the picker SHALL surface a **Connections** action (key
`c`) so the user can switch connection without first entering chat, and a **Settings** action
(key `s`, gated on the same managed-mode flag) that emits `showConfigMsg{}` to open the settings
screen. On OpenClaw connections the picker SHALL surface a **View crons** action (key `k`, gated
on `backend.CronBackend`) that emits `showCronsMsg{filterAgentID: ""}` to open the cron browser
across all agents — the one place to reach crons before entering a chat. `k` SHALL be dropped
from the list's up-navigation to make room (`↑` still scrolls up); see the `key-conventions`
spec.

#### Scenario: Connections action in managed mode

- **GIVEN** the picker is running in managed mode
- **WHEN** the user presses `c`
- **THEN** the connections picker is opened so the user can switch connection without first entering chat

#### Scenario: Settings action in managed mode

- **GIVEN** the picker is running in managed mode
- **WHEN** the user presses `s`
- **THEN** `showConfigMsg{}` is emitted to open the settings screen

#### Scenario: View crons action on an OpenClaw connection

- **GIVEN** an OpenClaw connection whose backend implements `backend.CronBackend`
- **WHEN** the user presses `k`
- **THEN** `showCronsMsg{filterAgentID: ""}` is emitted to open the cron browser across all agents
- **AND** `k` is dropped from the list's up-navigation while `↑` still scrolls up

### Requirement: Agent source per backend

Agents SHALL come from the active backend (`backend.Backend.ListAgents`). For OpenClaw, that is
the gateway's agent list. For OpenAI-compatible connections, agents are local — see the
`connections` spec (OpenAI agent storage). For Hermes connections the list SHALL be one synthetic
entry (the connected profile is the agent — see the `backend-hermes` spec, one-profile-one-agent)
and the create-agent affordance SHALL be hidden via `Capabilities.AgentManagement`.

#### Scenario: OpenClaw agents come from the gateway

- **GIVEN** an OpenClaw connection
- **WHEN** the picker lists agents
- **THEN** the agents are the gateway's agent list via `backend.Backend.ListAgents`

#### Scenario: Hermes shows a single synthetic agent

- **GIVEN** a Hermes connection
- **WHEN** the picker lists agents
- **THEN** the list is one synthetic entry for the connected profile
- **AND** the create-agent affordance is hidden via `Capabilities.AgentManagement`

### Requirement: Loading agents

The picker SHALL load agents via `loadAgents()`, which calls `client.ListAgents()` and returns an
`agentsLoadedMsg`. Each agent SHALL be an `agentItem` wrapping a `protocol.AgentSummary`. The
list SHALL be displayed using a Bubble Tea list component with a custom delegate that shows the
agent name, falling back to ID if no name is set.

#### Scenario: Agents loaded into the list

- **WHEN** `loadAgents()` runs
- **THEN** `client.ListAgents()` is called and an `agentsLoadedMsg` is returned
- **AND** each agent is an `agentItem` wrapping a `protocol.AgentSummary` shown by name, falling back to ID when no name is set

### Requirement: Fuzzy filtering with opt-in activation

The picker SHALL enable the Bubble Tea list's built-in filtering with the same fuzzy matcher
(`fuzzyFilter`) as the model picker (`internal/tui/models.go`). Pressing `/` SHALL open the
filter, typing SHALL narrow it, and `esc` SHALL clear it. `agentItem.FilterValue()` SHALL
concatenate the agent's name and ID so a query matches either — typing part of a display name or
part of the raw agent ID both hit the same agent. Unlike the model picker, the agent picker SHALL
start in plain list mode rather than dropping straight into filtering: the single-letter action
shortcuts (`n` new, `d` delete, `c` connections) must stay reachable, so filtering is opt-in via
`/`.

The following behaviours SHALL hold:

- **Keystroke routing.** While the filter input is focused (`list.FilterState() == list.Filtering`),
  `handleKey` SHALL forward every key except `enter` to the list so characters that collide with
  action shortcuts (e.g. `n`) type into the query instead of firing the action. Outside filtering,
  the normal action dispatch applies.
- **Enter selects from the filter.** `enter` SHALL pick the highlighted agent from any filter
  state, so the user can type-to-narrow and pick in one keystroke rather than first applying the
  filter (the bubbles default while typing). When the filter matches nothing, `enter` SHALL fall
  through to the list.
- **Input focus.** `selectModel.filtering()` SHALL report whether the filter is focused;
  `app.go`'s `computeWantsInput()` consults it (alongside the create-agent form) so the app-level
  `q`-to-quit shortcut and the embedder input-focus signal treat a typed `q` as filter text rather
  than a quit request.
- **Reset on re-entry.** The picker reuses one `selectModel` across navigation, so leaving for
  another screen (config, connections, chat) and coming back would otherwise restore a stale
  narrowed view. `AppModel.Update` SHALL call `selectModel.resetFilter()` on every transition
  *into* `viewSelect`, clearing the query so the list reopens showing every agent. It SHALL be a
  no-op when no filter was active.

#### Scenario: Filter matches name or ID

- **GIVEN** the filter is open
- **WHEN** the user types part of a display name or part of the raw agent ID
- **THEN** `agentItem.FilterValue()` concatenates name and ID so both queries hit the same agent

#### Scenario: Filtering is opt-in

- **GIVEN** the agent picker has just opened
- **WHEN** no key has been pressed
- **THEN** it is in plain list mode with the `n`, `d`, and `c` action shortcuts reachable, and filtering starts only after `/`

#### Scenario: Colliding key typed while filtering

- **GIVEN** the filter input is focused (`list.FilterState() == list.Filtering`)
- **WHEN** the user presses a key such as `n`
- **THEN** `handleKey` forwards it to the list so it types into the query instead of firing the action

#### Scenario: Enter picks from an active filter

- **GIVEN** the filter is active and narrows to at least one match
- **WHEN** the user presses `enter`
- **THEN** the highlighted agent is picked in one keystroke
- **AND** when the filter matches nothing, `enter` falls through to the list

#### Scenario: Typed q treated as filter text

- **GIVEN** the filter input is focused
- **WHEN** the user types `q`
- **THEN** `computeWantsInput()` consults `selectModel.filtering()` so `q` is filter text, not a quit request

#### Scenario: Filter reset on re-entry

- **GIVEN** a narrowed filter was left active and the user navigated away to config, connections, or chat
- **WHEN** the app transitions back into `viewSelect`
- **THEN** `AppModel.Update` calls `selectModel.resetFilter()` so the list reopens showing every agent
- **AND** it is a no-op when no filter was active

### Requirement: Auto-selection of a single agent and after create

If exactly one agent is returned, the picker SHALL select it automatically without user
interaction and create a session immediately. The same auto-select SHALL fire after creating a
new agent — the picker bypasses the list and proceeds straight to chat.

#### Scenario: Single agent auto-selected

- **GIVEN** exactly one agent is returned
- **WHEN** the list loads
- **THEN** that agent is selected automatically without user interaction and a session is created immediately

#### Scenario: Auto-select after create

- **GIVEN** a new agent was just created
- **WHEN** the picker reloads
- **THEN** it bypasses the list and proceeds straight to chat with the new agent

### Requirement: Command-line agent auto-pick

`lucinate chat --agent <name>` SHALL be a third auto-pick driver: `selectModel.autoPickName`
runs an ID-then-case-insensitive-name match on the first `agentsLoadedMsg` and selects the
matching agent. It SHALL run **before** the single-agent and post-create branches, so a `--agent`
mismatch in a single-agent connection surfaces as an error banner on the picker rather than
silently picking the only available agent. The override SHALL be one-shot: cleared on consume so
a later `agentsLoadedMsg` (e.g. after a user-driven create) does not re-fire it. See the
`chat-launch` spec for the full override-consumption story.

#### Scenario: --agent selects a matching agent

- **GIVEN** `lucinate chat --agent <name>` was invoked
- **WHEN** the first `agentsLoadedMsg` arrives
- **THEN** `selectModel.autoPickName` runs an ID-then-case-insensitive-name match and selects the matching agent, before the single-agent and post-create branches

#### Scenario: --agent mismatch in a single-agent connection

- **GIVEN** a single-agent connection and a `--agent` name that matches no agent
- **WHEN** the picker resolves the auto-pick
- **THEN** an error banner is shown on the picker rather than silently picking the only available agent

#### Scenario: Override is one-shot

- **GIVEN** the `--agent` override was consumed on the first `agentsLoadedMsg`
- **WHEN** a later `agentsLoadedMsg` arrives (e.g. after a user-driven create)
- **THEN** the override does not re-fire because it was cleared on consume

### Requirement: Selecting an agent

Pressing Enter on a highlighted agent SHALL call `client.CreateSession(agentID, key)`. On
success, `sessionCreatedMsg` SHALL carry the new session key and the app SHALL transition to the
chat view (`newChatModel(...)`). See the `sessions` spec for the session lifecycle from this
point. The `/agent <name>` slash command SHALL bypass the picker entirely and reach the same
`sessionCreatedMsg` path — see the `commands` spec (agent). From the chat input, `/agent `
followed by Tab SHALL autocomplete against the cached agent list.

#### Scenario: Enter creates a session

- **GIVEN** an agent is highlighted
- **WHEN** the user presses Enter
- **THEN** `client.CreateSession(agentID, key)` is called and, on success, `sessionCreatedMsg` carries the new session key and the app transitions to the chat view via `newChatModel(...)`

#### Scenario: /agent slash command bypasses the picker

- **WHEN** the user runs `/agent <name>`
- **THEN** the picker is bypassed entirely and the same `sessionCreatedMsg` path is reached

#### Scenario: Tab autocompletes agent names

- **GIVEN** the chat input contains `/agent ` followed by Tab
- **THEN** the name is autocompleted against the cached agent list

### Requirement: Creating an agent

Pressing `n` in the picker SHALL switch to a creation form (`subStateCreate`). The form's shape
SHALL be driven by the active backend's `Capabilities.AgentWorkspace` flag (see
`backend.Capabilities`).

**OpenClaw** (workspace-aware):

- **Name** — must start with a lowercase letter and contain only alphanumeric characters and
  hyphens. Validated on submit.
- **Workspace** — a filesystem path that is auto-suggested but editable.

On submit, `Backend.CreateAgent` SHALL be called with both fields. The gateway creates the agent
and seeds an `IDENTITY.md` file in the workspace.

**OpenAI-compatible** (local-agent backends):

- **Name** — same validation rules as above.
- The workspace field SHALL be hidden. On submit, the backend seeds `IDENTITY.md` and `SOUL.md`
  with defaults under `~/.lucinate/agents/<connection>/<agent>/`. Users edit those files on disk to
  customise the agent's identity and behaviour — see the `connections` spec (OpenAI agent storage).

On success the agent list SHALL be reloaded and the new agent SHALL be auto-selected (see the
auto-selection requirement). On failure the form SHALL stay open and the error SHALL be shown so
the user can correct and retry.

#### Scenario: OpenClaw create with workspace

- **GIVEN** an OpenClaw connection and the create form (`subStateCreate`)
- **WHEN** the user submits a valid name (starts with a lowercase letter, alphanumeric and hyphens only) and an auto-suggested but editable workspace path
- **THEN** `Backend.CreateAgent` is called with both fields and the gateway seeds an `IDENTITY.md` file in the workspace

#### Scenario: OpenAI-compatible create hides workspace

- **GIVEN** an OpenAI-compatible connection where `Capabilities.AgentWorkspace` is not set
- **WHEN** the create form is shown and a valid name submitted
- **THEN** the workspace field is hidden and the backend seeds `IDENTITY.md` and `SOUL.md` with defaults under `~/.lucinate/agents/<connection>/<agent>/`

#### Scenario: Create succeeds

- **WHEN** agent creation succeeds
- **THEN** the agent list is reloaded and the new agent is auto-selected

#### Scenario: Create fails

- **WHEN** agent creation fails
- **THEN** the form stays open and the error is shown so the user can correct and retry

### Requirement: Deleting an agent

Pressing `d` on a highlighted agent SHALL enter `subStateConfirmDelete`. The substate SHALL be
gated by the same `Capabilities.AgentManagement` flag as create — Hermes connections never expose
it because profiles are server-managed. The view SHALL be deliberately loud: a red header with
the agent's name, a bullet list of what's about to be removed (metadata, transcript, and on
OpenClaw bindings), the local backup path, a `Delete files | Keep files` toggle, and a textinput
labelled with the agent's display name.

The following behaviours SHALL hold:

- **Type-to-confirm.** `confirm-delete` SHALL only be emitted from `Actions()` when the typed name
  matches the agent's display name (case-insensitive, whitespace-trimmed) and no request is in
  flight. That presence-toggle is the disable mechanism for native-platform embedders — the
  `Action` struct has no `Enabled` flag.
- **Keep files toggle.** `tab` SHALL flip `m.keepFiles`, which becomes `!DeleteFiles` on the
  `backend.DeleteAgentParams` sent to the backend. It SHALL default to **keep files**
  (`keepFiles=true` → `DeleteFiles=false`), so a mistaken confirmation preserves the agent's file
  content — the user has to toggle off to destroy it. The view's description line SHALL switch with
  the toggle so the user can read what the current mode will do before pressing enter.
- **Pending state is snapshotted** (`pendingDeleteID`, `pendingDeleteName`) at substate entry from
  the passed `agentItem`, never re-read from `list.SelectedItem()` afterwards. A list re-render
  mid-flight cannot resolve the destructive cmd to the wrong agent.
- **Esc** SHALL trigger `cancel-delete` and clear all pending state. **Enter** without a matching
  name SHALL be a no-op.
- **Plain `d` is not bound** inside the substate because it's a printable character the user might
  type as part of the agent name.

`agentDeletedMsg` SHALL carry the result. On success the picker SHALL clear pending state and
reload via `loadAgents()`. On error `pendingDeleteName` SHALL be preserved so the user can retry
without retyping; `deleteErr` SHALL surface inline. Keystrokes SHALL be ignored while
`m.deleting` is true — the network call has already left.

The destructive vs preserve interpretation is per-backend:

- **OpenClaw** — `Backend.DeleteAgent` forwards to `Client.DeleteAgent(ctx, agentID, deleteFiles)`,
  which sends `protocol.AgentsDeleteParams{AgentID, DeleteFiles: &flag}` to the gateway. The
  pointer is always set explicitly — the gateway's implicit "preserve files" default never applies.
- **OpenAI-compatible** — when `DeleteFiles=true` the agent directory is wiped via
  `AgentStore.Delete` (`os.RemoveAll`); when false it's moved to `<root>/.archive/<id>-<unixts>/`
  via `AgentStore.Archive` so IDENTITY.md, SOUL.md, and history.jsonl are recoverable on disk. See
  the `backend-openai` spec (agent storage).
- **Hermes** — `DeleteAgent` returns a clear error pointing at `hermes profile delete`. The UI gate
  (AgentManagement=false) means the user shouldn't reach it; the reject is defensive.

#### Scenario: Confirm-delete gated on typed name

- **GIVEN** the `subStateConfirmDelete` view is showing
- **WHEN** the typed name matches the agent's display name (case-insensitive, whitespace-trimmed) and no request is in flight
- **THEN** `confirm-delete` is emitted from `Actions()`; otherwise the action is absent (the presence-toggle disable mechanism, since `Action` has no `Enabled` flag)

#### Scenario: Keep-files default preserves content

- **GIVEN** the confirm-delete view with its default toggle
- **WHEN** the user confirms without toggling
- **THEN** `keepFiles=true` → `DeleteFiles=false` so the agent's file content is preserved, and the user must press `tab` to toggle off to destroy it, with the description line switching to match

#### Scenario: Pending agent snapshotted against re-render

- **GIVEN** delete confirmation was entered from a passed `agentItem`
- **WHEN** the list re-renders mid-flight
- **THEN** `pendingDeleteID` and `pendingDeleteName` remain the snapshotted values, never re-read from `list.SelectedItem()`, so the destructive cmd cannot resolve to the wrong agent

#### Scenario: Cancel and no-op enter

- **WHEN** the user presses Esc
- **THEN** `cancel-delete` fires and all pending state is cleared
- **AND** pressing Enter without a matching name is a no-op, and plain `d` is not bound inside the substate

#### Scenario: Delete succeeds

- **WHEN** `agentDeletedMsg` reports success
- **THEN** the picker clears pending state and reloads via `loadAgents()`

#### Scenario: Delete fails

- **WHEN** `agentDeletedMsg` reports an error
- **THEN** `pendingDeleteName` is preserved for retry without retyping and `deleteErr` surfaces inline, with keystrokes ignored while `m.deleting` is true

#### Scenario: OpenClaw delete sets the files pointer explicitly

- **GIVEN** an OpenClaw connection
- **WHEN** an agent is deleted
- **THEN** `Backend.DeleteAgent` forwards to `Client.DeleteAgent(ctx, agentID, deleteFiles)` sending `protocol.AgentsDeleteParams{AgentID, DeleteFiles: &flag}`, with the pointer always set so the gateway's implicit preserve-files default never applies

#### Scenario: OpenAI-compatible archive vs wipe

- **GIVEN** an OpenAI-compatible connection
- **WHEN** an agent is deleted with `DeleteFiles=true`
- **THEN** the agent directory is wiped via `AgentStore.Delete` (`os.RemoveAll`)
- **AND** when `DeleteFiles=false` it is moved to `<root>/.archive/<id>-<unixts>/` via `AgentStore.Archive` so IDENTITY.md, SOUL.md, and history.jsonl are recoverable on disk

#### Scenario: Hermes delete is defensively rejected

- **GIVEN** a Hermes connection where the UI gate `AgentManagement=false` should keep the user away
- **WHEN** `DeleteAgent` is nonetheless reached
- **THEN** it returns a clear error pointing at `hermes profile delete`
