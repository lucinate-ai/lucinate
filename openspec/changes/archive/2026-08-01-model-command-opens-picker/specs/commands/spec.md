## RENAMED Requirements

- FROM: `### Requirement: /model reports and switches the session model`
- TO: `### Requirement: /model opens the picker and switches the session model`

## MODIFIED Requirements

### Requirement: /model opens the picker and switches the session model

`handleModelCommand()` SHALL open the model picker when called with no argument, behaving
identically to `/models` — the same `showModelPickerMsg` carrying the active session key and
the current model reference. Bare `/model` SHALL NOT emit a textual report of the model in
use; the picker is the answer to "which model am I on?" (see the `chat-ux` spec — the picker
marks the model in use). `/models` (plural) SHALL remain as an alias for the same behaviour.

With a name it SHALL call `client.ModelsList()` to retrieve available models from the gateway,
fuzzy-match against model IDs and names (exact match first, then substring), then call
`client.SessionPatchModel(sessionKey, modelRef)` and update `m.modelID` in the header.

Both switch paths — the `/model <name>` command and the picker — SHALL patch with
the **qualified `<provider>/<id>` reference** produced by `qualifiedModelRef()`
(`internal/tui/models.go`), not the bare id. `models.list` reports a provider-local id (e.g.
`deepseek/deepseek-v4-pro`) alongside a separate `provider` field (e.g. `openrouter`), but
`sessions.patch` — like the agent's configured `model.primary` — validates against the
fully-qualified form (`openrouter/deepseek/deepseek-v4-pro`). Sending the bare id makes the
gateway reject the switch with `INVALID_REQUEST: model not allowed: <id>`. Backends that
leave `provider` empty (openai, hermes) SHALL keep the bare id unchanged.

#### Scenario: Bare /model opens the picker
- **WHEN** the user submits `/model` with no argument
- **THEN** the model picker opens, seeded with the active session key and the current model reference
- **AND** no `Model: <id>` system message is appended to the chat

#### Scenario: /model and /models are equivalent with no argument
- **WHEN** the user submits `/model` and, separately, `/models`
- **THEN** both open the model picker with the same session key and current model reference

#### Scenario: Trailing whitespace still counts as no argument
- **WHEN** the user submits `/model` followed only by spaces
- **THEN** the picker opens, exactly as for bare `/model`

#### Scenario: /model switches with a qualified reference
- **WHEN** the user submits `/model <name>`
- **THEN** `client.ModelsList()` is called, the name is fuzzy-matched (exact first, then substring) against model IDs and names, and `client.SessionPatchModel` is called with the qualified `<provider>/<id>` reference from `qualifiedModelRef()`
- **AND** `m.modelID` in the header is updated

#### Scenario: Provider-empty backend keeps the bare id
- **GIVEN** an openai or hermes backend that leaves `provider` empty
- **WHEN** the model reference is qualified
- **THEN** the bare id is kept unchanged

### Requirement: Built-in slash command catalogue

The system SHALL provide the following built-in slash commands. Backend-only commands
(marked **OpenClaw only**) SHALL render a "not available on this connection" system message
on connections that do not support them (see the `connections` spec — capability negotiation).

| Command | What it does |
|---|---|
| `/agent` | Return to the agent picker (alias for `/agents`) |
| `/agent <name>` | Switch to a named agent without going through the picker |
| `/agents` | Return to the agent picker by emitting `goBackMsg{}` |
| `/cancel` | Cancel the in-progress response (also triggered by Escape) — see the `chat-ux` spec |
| `/clear` | Wipe `m.messages` from the local display (does not affect gateway history) |
| `/compact` | Compact the session context — see the `sessions` spec |
| `/config` | Backward-compatible alias for `/settings` |
| `/connections` | Open the connections picker mid-session, tearing down the active backend — see the `connections` spec |
| `/crons` | Open the cron browser filtered to the current agent — see the `crons` spec — **OpenClaw only** |
| `/crons all` | Open the cron browser unfiltered (jobs across all agents) — **OpenClaw only** |
| `/cron <name>` | Resolve a named cron job and run it immediately after a y/n confirmation — **OpenClaw only** |
| `/exit`, `/quit` | Exit via `tea.Quit` |
| `/export` | Write the current session's canonical history to a transcript file |
| `/export all` | Same as `/export` |
| `/export routine` | Convert the session's user prompts into routine steps and open the form prepopulated |
| `/header` | Show the chat header background colour for the current agent |
| `/header <hex>` | Set the chat header background for the current agent to a hex colour (e.g. `#4FC3F7`, `#F0C`); persisted per agent across runs |
| `/header reset` | Restore the default header colour for the current agent (also accepts `default` or `off`) |
| `/help`, `/commands` | Print static help text; appends skill count if any are loaded |
| `/model` | Open the model picker (filter as you type) |
| `/model <name>` | Switch model |
| `/models` | Alias for `/model` — opens the model picker |
| `/mouse` | Report the current mouse-capture state |
| `/mouse on` | Enable mouse capture (the default): wheel scrolls history, click-drag selects and copies — see the `chat-ux` spec |
| `/mouse off` | Disable capture, handing click-drag back to the terminal's native selection (wheel scrolling stops) |
| `/mouse toggle` | Flip the capture state |
| `/record` | Show whether transcript capture is on, and where it's writing |
| `/record on` | Begin streaming canonical conversation messages to a transcript file |
| `/record off` | Stop the active recording and report the file path |
| `/reset` | Delete the session and start fresh — see the `sessions` spec |
| `/routine <name>` | Activate a stored routine in the current session — see the `routines` spec |
| `/routines` | Open the routines manager (list/view/edit/delete) — see the `routines` spec |
| `/sessions` | Open the session browser — see the `sessions` spec |
| `/settings` | Open the settings view by emitting `showConfigMsg{}` (alias: `/config`) |
| `/skills` | List discovered skills — see the `skills` spec |
| `/stats` | Show a token usage and cost table for the current session — **OpenClaw only** |
| `/status` | Show backend status — common header (type, endpoint, auth, default model) plus backend-specific blocks: OpenClaw gateway health / versions / agents / channels, OpenAI agent count + current `history.jsonl` stats, Hermes thread state |
| `/think` | Show the current thinking level — **OpenClaw only** |
| `/think <level>` | Set the thinking level — see the `chat-ux` spec — **OpenClaw only** |

#### Scenario: Backend-only command on an unsupported connection
- **GIVEN** a connection that does not support cron jobs
- **WHEN** the user submits `/crons`, `/cron <name>`, `/stats`, or `/think`
- **THEN** a "not available on this connection" system message is rendered

#### Scenario: Alias resolves to canonical command
- **WHEN** the user submits `/config`
- **THEN** it behaves identically to `/settings`, emitting `showConfigMsg{}`
