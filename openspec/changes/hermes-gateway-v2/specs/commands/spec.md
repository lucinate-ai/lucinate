## MODIFIED Requirements

### Requirement: Built-in slash command catalogue

The system SHALL provide the following built-in slash commands. Backend-only commands
(marked **OpenClaw only** or **Hermes only**) SHALL render a "not available on this connection"
system message on connections that do not support them (see the `connections` spec — capability
negotiation).

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
| `/models` | Alias for /model — opens the model picker |
| `/mouse` | Report the current mouse-capture state |
| `/mouse on` | Enable mouse capture (the default): wheel scrolls history, click-drag selects and copies — see the `chat-ux` spec |
| `/mouse off` | Disable capture, handing click-drag back to the terminal's native selection (wheel scrolling stops) |
| `/mouse toggle` | Flip the capture state |
| `/record` | Show whether transcript capture is on, and where it's writing |
| `/record on` | Begin streaming canonical conversation messages to a transcript file |
| `/record off` | Stop the active recording and report the file path |
| `/reset` | Delete the session and start fresh — see the `sessions` spec |
| `/rooms` | Open the rooms view — Hermes hosted group chats with Hermes profiles — see the `rooms` spec — **Hermes only** |
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

#### Scenario: /rooms on a non-Hermes connection
- **GIVEN** an OpenClaw or OpenAI-compatible connection
- **WHEN** the user submits `/rooms`
- **THEN** a "not available on this connection" system message names the Hermes requirement
- **AND** no gateway socket is dialled

#### Scenario: Alias resolves to canonical command
- **WHEN** the user submits `/config`
- **THEN** it behaves identically to `/settings`, emitting `showConfigMsg{}`
