# Slash Commands Specification

## Purpose

Slash commands are local TUI commands that begin with `/`. They are intercepted by
`handleSlashCommand()` in `internal/tui/commands.go` before any message is sent to the
gateway, so they run entirely on the client and never reach the backend when handled.
This spec covers command dispatch, the full set of built-in commands and their individual
behaviours, tab completion, the yes/no confirmation pattern, and the routine-active
navigation gate.

## Requirements

### Requirement: Slash command interception and dispatch

The system SHALL intercept input that begins with `/` via `handleSlashCommand()` in
`internal/tui/commands.go` before any message is sent to the gateway. The function SHALL
return `(handled bool, cmd tea.Cmd)` — when `handled` is true the input SHALL be consumed
locally and never forwarded to the backend.

Input that starts with `/` and contains no spaces SHALL be matched case-insensitively
against a `switch` statement of built-in commands. Commands that accept an argument (e.g.
`/agent foo`, `/model sonnet`, `/think high`) SHALL be matched by prefix check after the
switch.

Slash input that is not a built-in SHALL be checked against the loaded skill names: if the
first token (`/foo` from `/foo bar`) matches a skill, `handleSlashCommand` SHALL return
`(false, nil)` and let the regular send path expand it via `expandSkillReferences` (see the
`skills` spec). If it matches neither a built-in nor a skill, an error system message SHALL
be shown.

#### Scenario: Built-in command consumed locally
- **WHEN** the user submits a built-in slash command such as `/clear`
- **THEN** `handleSlashCommand()` returns `handled = true` and the input is consumed locally
- **AND** it is never forwarded to the gateway

#### Scenario: Command with an argument matched by prefix
- **WHEN** the user submits `/model sonnet`
- **THEN** the case-insensitive switch does not match the whole token, so the argument-bearing prefix check matches `/model ` and handles it

#### Scenario: Slash input matching a loaded skill
- **GIVEN** a loaded skill named `foo`
- **WHEN** the user submits `/foo bar`
- **THEN** `handleSlashCommand` returns `(false, nil)`
- **AND** the regular send path expands it via `expandSkillReferences`

#### Scenario: Slash input matching neither built-in nor skill
- **WHEN** the user submits a slash token that matches no built-in and no skill
- **THEN** an error system message is shown

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
| `/model` | Report the model in use for the current session |
| `/model <name>` | Switch model |
| `/models` | Open the model picker (filter as you type) |
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

### Requirement: /agent switches to or returns from a named agent

`handleAgentCommand()` SHALL cover both `/agent` shapes. With no argument it SHALL emit
`goBackMsg{}`, returning to the agent picker just like `/agents`. With a name it SHALL call
`client.ListAgents()`, fuzzy-match case-insensitively against agent names and IDs (exact
match first, then substring), then call `client.CreateSession(agentID, "main")` and emit the
same `sessionCreatedMsg` the picker selection path uses — so the chat view rebuild is
identical to picking from the list. Lookup failures (no match, or backend error) SHALL be
rendered inline as a system message via `agentSwitchFailedMsg` rather than bouncing the user
back to the picker.

#### Scenario: Bare /agent returns to the picker
- **WHEN** the user submits `/agent` with no argument
- **THEN** `goBackMsg{}` is emitted and the agent picker is shown, identical to `/agents`

#### Scenario: /agent with a name switches agent in place
- **WHEN** the user submits `/agent <name>`
- **THEN** `client.ListAgents()` is called, the name is fuzzy-matched (exact first, then substring) against agent names and IDs, `client.CreateSession(agentID, "main")` runs, and `sessionCreatedMsg` rebuilds the chat view identically to a picker selection

#### Scenario: /agent lookup fails
- **WHEN** `/agent <name>` finds no match or the backend errors
- **THEN** `agentSwitchFailedMsg` renders the failure inline as a system message
- **AND** the user is not bounced back to the picker

### Requirement: /model reports and switches the session model

`handleModelCommand()` SHALL report the current model for the active session when called
with no argument (falling back to `gateway default` when `m.modelID` is empty), pointing the
user at `/models` and `/model <name>` to change it. With a name it SHALL call
`client.ModelsList()` to retrieve available models from the gateway, fuzzy-match against
model IDs and names (exact match first, then substring), then call
`client.SessionPatchModel(sessionKey, modelRef)` and update `m.modelID` in the header.
`/models` (plural) SHALL open the picker via `showModelPickerMsg`.

Both switch paths — the `/model <name>` command and the `/models` picker — SHALL patch with
the **qualified `<provider>/<id>` reference** produced by `qualifiedModelRef()`
(`internal/tui/models.go`), not the bare id. `models.list` reports a provider-local id (e.g.
`deepseek/deepseek-v4-pro`) alongside a separate `provider` field (e.g. `openrouter`), but
`sessions.patch` — like the agent's configured `model.primary` — validates against the
fully-qualified form (`openrouter/deepseek/deepseek-v4-pro`). Sending the bare id makes the
gateway reject the switch with `INVALID_REQUEST: model not allowed: <id>`. Backends that
leave `provider` empty (openai, hermes) SHALL keep the bare id unchanged.

#### Scenario: Bare /model reports the current model
- **WHEN** the user submits `/model` with no argument
- **THEN** the current session model is reported (or `gateway default` when `m.modelID` is empty) and the user is pointed at `/models` and `/model <name>`

#### Scenario: /model switches with a qualified reference
- **WHEN** the user submits `/model <name>`
- **THEN** `client.ModelsList()` is called, the name is fuzzy-matched (exact first, then substring) against model IDs and names, and `client.SessionPatchModel` is called with the qualified `<provider>/<id>` reference from `qualifiedModelRef()`
- **AND** `m.modelID` in the header is updated

#### Scenario: Provider-empty backend keeps the bare id
- **GIVEN** an openai or hermes backend that leaves `provider` empty
- **WHEN** the model reference is qualified
- **THEN** the bare id is kept unchanged

### Requirement: /cron resolves and runs a named cron job

`handleCronCommand()` SHALL be gated on `backend.CronBackend` (OpenClaw only); on other
connections it SHALL report "not available". It SHALL require a name argument — bare `/cron`
SHALL error and point at `/crons` (the plural browser is matched by the switch first, so a
`/cron ` prefix never swallows it).

Because names live on the gateway, resolution SHALL be asynchronous: the handler SHALL return
a command that calls `CronsList` and dispatches a `cronResolveMsg` carrying the query and the
matched jobs. `matchCronJobs()` SHALL be tiered — exact (case-insensitive) `Name`, then exact
`ID`, then substring on either — and SHALL return every job in the winning tier because cron
job names are **not unique**. `chatModel.Update` SHALL turn the result into one of: an error
row (list failed or no match), an ambiguity row listing each candidate's name, `id`, and
schedule (re-run `/cron <id>` — IDs are unique — to disambiguate), or, for a single match, a
`pendingConfirmation` (see the confirmation pattern requirement). The confirm prompt SHALL
show the schedule and next run and end in `(y/n)`; on `y` the action SHALL call
`CronRun(id, force=true)` — run now regardless of schedule, matching the crons browser's
"Run now" — and report the outcome via `chatCronRanMsg` → `replacePendingSystem`.

#### Scenario: Bare /cron errors
- **WHEN** the user submits `/cron` with no argument
- **THEN** it errors and points at `/crons`

#### Scenario: Ambiguous cron name
- **GIVEN** multiple cron jobs match the query in the winning tier
- **WHEN** `matchCronJobs()` returns more than one job
- **THEN** an ambiguity row lists each candidate's name, `id`, and schedule, advising the user to re-run `/cron <id>` (IDs are unique) to disambiguate

#### Scenario: Single cron match confirmed and run
- **GIVEN** exactly one cron job matches
- **WHEN** the user answers `y` to the `(y/n)` confirmation showing the schedule and next run
- **THEN** `CronRun(id, force=true)` runs the job now regardless of schedule and reports the outcome via `chatCronRanMsg` → `replacePendingSystem`

### Requirement: /stats renders a usage and cost table

Stats SHALL be loaded asynchronously via `client.SessionUsage()` on chat init and after each
message exchange. `/stats` SHALL format `m.stats` through `formatStatsTable()` in
`internal/tui/render.go`, which produces a text table of input/output/cache tokens and cost
breakdown.

#### Scenario: /stats formats loaded usage
- **GIVEN** `m.stats` has been populated via `client.SessionUsage()`
- **WHEN** the user submits `/stats`
- **THEN** `formatStatsTable()` renders a text table of input/output/cache tokens and cost breakdown

### Requirement: /header sets a per-agent chat header colour

`handleHeaderCommand()` SHALL parse the argument, run it through
`config.NormalizeHexColor()` (accepts `#RRGGBB`, `#RGB`, with or without the leading `#`),
then write the canonical `#RRGGBB` value via `prefs.SetHeaderColor(agentID, hex)`, persist
with `config.SavePreferences()`, and emit `prefsUpdatedMsg` so `AppModel.prefs` and
`chatModel.prefs` stay in sync. The chat view's `View()` SHALL call
`m.prefs.HeaderColorFor(m.agentID)` each render and override `headerStyle.Background()` (and
the update-available warn-badge background, which sits inside the header) when a value is set
for the current agent. Bare `/header` SHALL report the current agent's value; `/header reset`
(also `default`, `off`) SHALL clear it.

The colour SHALL be scoped per agent — switching to another agent shows that agent's own
colour (or the built-in default). Per-agent overrides SHALL be stored under a nested `agents`
sub-object in `config.json`, keyed by agent ID, so future per-agent settings can sit
alongside `headerColor` without growing the file horizontally. When the active agent has no
ID (e.g. an unbound chat), `/header` SHALL report an error rather than falling back to a
global setting.

#### Scenario: Setting a header colour
- **WHEN** the user submits `/header #4FC3F7`
- **THEN** the value is normalised via `config.NormalizeHexColor()`, written via `prefs.SetHeaderColor(agentID, hex)`, persisted with `config.SavePreferences()`, and `prefsUpdatedMsg` keeps the models in sync

#### Scenario: Per-agent scoping
- **GIVEN** a header colour set for agent A
- **WHEN** the user switches to agent B
- **THEN** agent B shows its own colour or the built-in default, and agent A's stored colour is untouched

#### Scenario: Reset clears the override
- **WHEN** the user submits `/header reset` (or `default` or `off`)
- **THEN** the current agent's header colour override is cleared

#### Scenario: Header on an agent with no ID
- **GIVEN** an unbound chat where the active agent has no ID
- **WHEN** the user submits `/header`
- **THEN** an error is reported rather than falling back to a global setting

### Requirement: /record and /export persist canonical chat history

Both `/record` and `/export` SHALL persist the session's canonical chat history to a Markdown
file under `<dataDir>/transcripts/`. `/record on` SHALL stream new turns as they finalise;
`/export [all]` SHALL dump the current state in one shot; `/export routine` SHALL skip the
file write and open the routines manager prefilled with the session's user prompts. The
canonical-history tap, dedup signature, file layout, and lifecycle are documented in the
`export-and-recording` spec.

#### Scenario: Recording streams finalised turns
- **WHEN** the user submits `/record on`
- **THEN** new turns are streamed to a Markdown transcript under `<dataDir>/transcripts/` as they finalise
- **AND** `/record off` stops the active recording and reports the file path

#### Scenario: Export dumps current state
- **WHEN** the user submits `/export` or `/export all`
- **THEN** the current canonical history is written in one shot to a Markdown file under `<dataDir>/transcripts/`

#### Scenario: Export routine skips the file write
- **WHEN** the user submits `/export routine`
- **THEN** no transcript file is written and the routines manager opens prefilled with the session's user prompts

### Requirement: Tab completion menu

A live menu (rendered between the conversation viewport and the input) SHALL appear as soon
as the cursor sits at the end of a completable token with at least one matching candidate.
Three sources SHALL feed the same menu and the same Tab/Shift+Tab semantics:

- **Slash commands and skills** — `matchingSlashCommands(prefix)` (`completion.go`) returns
  built-ins from the static `slashCommands` slice in their curated order, followed by skill
  names. Source detection: `findSlashTokenAt(value, cursorByte)`.
- **Agent names** — the argument of `/agent <name>`. `matchingAgentNames(prefix)` returns
  every loaded agent whose lowercased form has the prefix as a prefix, preserving each
  agent's original casing. Source detection: `findAgentArgAt(value, cursorByte)`, which
  treats the entire tail of the line after `/agent ` as the token (so names with spaces
  complete in one shot).
- **Cron names** — the argument of `/cron <name>`. `matchingCronNames(prefix)` mirrors the
  agent source over `m.cronNames`, which `loadCronNames()` populates on init from `CronsList`
  (a no-op on non-OpenClaw connections). Source detection: `findCronArgAt(value, cursorByte)`
  (line begins with `/cron ` — the trailing space excludes `/crons`). The snapshot may go
  slightly stale mid-session; `/cron <name>` always re-lists to resolve the actual run.

`completionAtCursor()` SHALL resolve the active source — slash commands take priority — and
return a `completionContext{start, cursorByte, prefix, candidates}`. The Tab handler SHALL
dispatch a single `handleCompletionTab(ctx)` over this context, applying bash-style
menu-complete semantics:

- One match → full completion in place.
- Multiple matches with a longest common prefix beyond what the user typed → the input is
  extended up to that LCP. `longestCommonPrefixFold(strs)` computes a case-insensitive LCP
  using the first candidate's casing — agent names like `Main` and `Mail` collapse to `Mai`,
  slash candidates (already lowercase) behave identically to the byte-wise variant.
- Multiple matches at the LCP → enter cycle mode. The first Tab snapshots the candidate list
  into `m.completion.cycleCandidates`, sets `cycleIndex = 0`, and replaces the input with the
  first candidate. Subsequent Tab presses advance the index modulo the snapshot length;
  Shift+Tab decrements with the same wraparound. The snapshot persists across presses because
  Tab returns early before `refreshCompletionMenu` runs.

Any non-Tab keystroke SHALL route through `refreshCompletionMenu`, which clears `cycling` and
recomputes `candidates` from the current textarea contents via `completionAtCursor()`. The
menu SHALL auto-hide when no source applies (whitespace breaks a slash token, cursor leaves
end-of-line in the agent-arg context, the input is cleared, or the message is sent —
`Reset()` calls in the Enter handler explicitly invoke `refreshCompletionMenu` so the menu
doesn't outlive the input).

The curated order in `slashCommands` (e.g. `/agents` before `/agent`, `/model` before
`/models`) now only breaks ties for the inline ghost-hint and the legacy
`completeSlashCommand` callers — Tab uses LCP, so the order no longer steers it.
`setTextareaToValueWithCursor` SHALL perform the in-place replacement and reposition the
cursor at the end of the inserted text.

#### Scenario: Single candidate completes in place
- **GIVEN** the cursor is at the end of a completable token with exactly one matching candidate
- **WHEN** the user presses Tab
- **THEN** the token is fully completed in place

#### Scenario: Multiple candidates extend to the LCP
- **GIVEN** multiple matches with a longest common prefix beyond what the user typed
- **WHEN** the user presses Tab
- **THEN** the input is extended up to the case-insensitive LCP computed by `longestCommonPrefixFold`

#### Scenario: Cycling at the LCP
- **GIVEN** multiple matches already at the LCP
- **WHEN** the user presses Tab repeatedly
- **THEN** the candidate list is snapshotted and each Tab advances the index modulo the snapshot length, with Shift+Tab decrementing under the same wraparound

#### Scenario: Menu auto-hides when no source applies
- **WHEN** whitespace breaks a slash token, the cursor leaves end-of-line in the agent-arg context, the input is cleared, or the message is sent
- **THEN** `refreshCompletionMenu` recomputes candidates and the menu hides

### Requirement: Inline ghost-hint fallback

`slashCommandHint` and `agentNameHint` SHALL drive a single-line greyed-out hint in the help
bar as a fallback for short terminals where the menu cannot render. With the menu visible,
the help line SHALL switch to `Tab: extend · Shift+Tab: back · N matches`.

#### Scenario: Ghost hint on a short terminal
- **GIVEN** a terminal too short for the completion menu to render
- **WHEN** a completable token is typed
- **THEN** `slashCommandHint` / `agentNameHint` render a single-line greyed-out hint in the help bar

#### Scenario: Help line with menu visible
- **WHEN** the completion menu is visible
- **THEN** the help line shows `Tab: extend · Shift+Tab: back · N matches`

### Requirement: Completion menu layout

`chatModel.baseViewportHeight` SHALL record the viewport height with the menu hidden;
`applyLayout()` SHALL shrink the viewport by `menuRowsToRender()` whenever menu state changes,
so the conversation pane reflows cleanly. The menu SHALL suppress itself entirely when the
baseline cannot leave at least `completionMenuViewportFloor` rows for the conversation — Tab
still does LCP extension on the underlying state. Candidate counts above
`completionMenuMaxRows` SHALL collapse the tail into a `+N more` line.

#### Scenario: Viewport reflows around the menu
- **WHEN** menu state changes
- **THEN** `applyLayout()` shrinks the viewport by `menuRowsToRender()` so the conversation pane reflows cleanly

#### Scenario: Menu suppressed when the conversation would be starved
- **GIVEN** the baseline cannot leave at least `completionMenuViewportFloor` rows for the conversation
- **WHEN** completion is triggered
- **THEN** the menu suppresses itself entirely while Tab still performs LCP extension on the underlying state

#### Scenario: Long candidate list collapses
- **GIVEN** more candidates than `completionMenuMaxRows`
- **WHEN** the menu renders
- **THEN** the tail collapses into a `+N more` line

### Requirement: Agent name completion source

The chat model SHALL fetch the agent list once on init via `loadAgentNames()` and store
display names in `m.agentNames`; completion SHALL silently degrade to a no-op when the list
has not loaded yet or the backend errored (`matchingAgentNames` returns nil, so
`completionAtCursor` reports an empty candidate list and the menu stays hidden).
`findAgentArgAt` SHALL recognise the argument context only when the cursor sits at
end-of-line and the line begins with `/agent ` (single space). An empty prefix SHALL match
every agent — Tab on `/agent ` opens the menu listing the full roster, with the LCP/cycle
flow taking over from there.

#### Scenario: Agent list not loaded
- **GIVEN** the agent list has not loaded or the backend errored
- **WHEN** the user attempts agent-name completion
- **THEN** `matchingAgentNames` returns nil, the candidate list is empty, and the menu stays hidden

#### Scenario: Empty prefix lists the full roster
- **WHEN** the user presses Tab on `/agent ` (single trailing space, cursor at end-of-line)
- **THEN** the menu lists every loaded agent and the LCP/cycle flow takes over

### Requirement: Yes/no confirmation pattern

Commands that need a yes/no (`/compact`, `/reset`, `/cron <name>`) SHALL use a two-step
confirmation. On first invocation a `pendingConfirmation` struct SHALL be stored on the model
containing the prompt string, an optional `runningStatus` line, and an action closure. The
prompt SHALL be appended to the chat scrollback as a system row tagged `confirmPrompt: true`.
On the next Enter keypress, if the input is `y` or `yes` the closure SHALL be executed;
anything else SHALL cancel — and either way `removeConfirmPrompt()` SHALL drop the tagged
prompt row (a brief `Confirmed.`/`Cancelled.` notification reports which) so the `(y/n)`
question does not linger once answered. This prevents accidental data loss.

When `runningStatus` is set, the confirmation handler SHALL also append a pending system row
(`pending: true`) carrying that status text to the chat scrollback. The renderer SHALL
animate the same braille spinner used for in-flight assistant turns next to the row, and
`hasStreamingMessage` SHALL keep `spinnerTickCmd` firing until the action returns. The result
handler (`sessionCompactedMsg`, `sessionClearedMsg`, `chatCronRanMsg`) SHALL call
`replacePendingSystem` to swap the placeholder for the outcome line in place — no stale
"Compacting session…" stuck above the result.

#### Scenario: Confirmation accepted
- **GIVEN** a `pendingConfirmation` is showing its `confirmPrompt`-tagged row
- **WHEN** the user's next Enter input is `y` or `yes`
- **THEN** the action closure executes, `removeConfirmPrompt()` drops the prompt row, and a brief `Confirmed.` notification is shown

#### Scenario: Confirmation cancelled
- **WHEN** the user's next Enter input is anything other than `y`/`yes`
- **THEN** the action is not executed, `removeConfirmPrompt()` drops the prompt row, and a brief `Cancelled.` notification is shown

#### Scenario: Running status spinner
- **GIVEN** a confirmation with `runningStatus` set is accepted
- **WHEN** the action runs
- **THEN** a `pending: true` system row shows the braille spinner until the result handler calls `replacePendingSystem` to swap in the outcome line

### Requirement: Routine-active navigation gate

Slash commands that strand or replace the chat model — `/agents`, `/agent <name>`,
`/sessions`, `/crons`, `/crons all`, `/connections`, `/routine <name>`, `/routines` — SHALL
route through `gateNavigation()` (`internal/tui/routines_chat.go`) when a routine is active. A
`pendingNavConfirm` SHALL be set, the prompt SHALL be rendered as a notification, and the
Enter handler SHALL resolve it: `y` cancels any in-flight turn, ends the routine cleanly
(closing the log file), and dispatches the navigation; `n` or Esc dismisses the prompt and
the routine continues. The state SHALL be independent of the generic `pendingConfirmation` so
the two flows do not compete. See the `routines` spec (slash commands and gating) for the full
rationale.

#### Scenario: Navigation gated while a routine is active
- **GIVEN** a routine is active
- **WHEN** the user submits a chat-replacing command such as `/agents` or `/connections`
- **THEN** `gateNavigation()` sets a `pendingNavConfirm` and renders the prompt as a notification

#### Scenario: Confirming navigation ends the routine
- **GIVEN** a `pendingNavConfirm` is showing
- **WHEN** the user answers `y`
- **THEN** any in-flight turn is cancelled, the routine ends cleanly (closing the log file), and the navigation is dispatched

#### Scenario: Declining navigation continues the routine
- **GIVEN** a `pendingNavConfirm` is showing
- **WHEN** the user answers `n` or presses Esc
- **THEN** the prompt is dismissed and the routine continues
