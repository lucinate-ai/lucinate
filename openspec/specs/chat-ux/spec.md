# Chat UX Specification

## Purpose

Define the interactive chat surface of lucinate: how the input box interprets key bindings, how sent messages stream back with animated feedback, how the header bar reports agent, model, context usage, connection and update state, how the view regions are stacked and how ephemeral notifications, routine status, and tool activity are surfaced. It also covers message recall, extended-thinking levels, history depth and mid-turn resync, connect timeout, and the mouse-driven scrolling and selection model. This is a faithful reformat of the chat-ux maintainer doc.

## Requirements

### Requirement: Input key bindings

The chat input box SHALL interpret the following key bindings:

| Key | Action |
|---|---|
| Enter | Send message — or, on empty input with an active routine, advance the routine (see the `routines` spec) |
| Alt+Enter | Insert newline |
| Ctrl+W | Delete word backward |
| Up arrow (empty input) | Recall the last queued message for editing, or start a bash-style walk back through previously sent messages |
| Down arrow (while walking) | Walk forward again; at the newest entry, clear the input |
| Tab | Open slash menu, extend to longest common prefix, then cycle |
| Shift+Tab | While slash-menu is cycling: cycle backward through candidates. Otherwise, with an active routine: cycle the routine's mode (auto ↔ manual) |
| Page Up / Page Down | Scroll message history |
| Mouse wheel | Scroll message history — see the "Scrolling, selection, and the mouse" requirement |
| Mouse click-drag | Select transcript text; copies to the clipboard on release |
| Esc | Cancel in-progress response — or, with an active routine, end the routine and (if streaming) cancel the turn |

Alt+Enter SHALL be configured via `ta.KeyMap.InsertNewline.SetKeys("alt+enter")` in `chat.go`. Shift+Enter SHALL NOT be supported — `ReportAllKeysAsEscapeCodes` is disabled to preserve shifted punctuation input.

#### Scenario: Newline binding uses Alt+Enter, not Shift+Enter
- **GIVEN** the chat input box is focused
- **WHEN** the user presses Alt+Enter
- **THEN** a newline is inserted via `ta.KeyMap.InsertNewline.SetKeys("alt+enter")`
- **AND** Shift+Enter is not supported because `ReportAllKeysAsEscapeCodes` is disabled to preserve shifted punctuation input

#### Scenario: Esc cancels the in-progress response
- **GIVEN** a response is in progress
- **WHEN** the user presses Esc
- **THEN** the in-progress response is cancelled
- **AND** with an active routine, Esc instead ends the routine and (if streaming) cancels the turn

### Requirement: Message recall

When the textarea is empty and the user presses Up arrow, the system SHALL pop the last entry in `m.pendingMessages` and insert it into the textarea with the cursor at the end, so that a recently queued message can be edited and resent without retyping it.

With no queued messages, Up SHALL start a bash-style walk back through previously submitted user messages (`historyBrowseIndex` / `historyBrowseValue` in `chat.go`): repeated Up steps older, Down steps newer, and reaching the newest entry clears the input. The walk SHALL end as soon as the recalled text is edited — Up/Down then revert to ordinary cursor movement within multi-line content. Because mouse capture is on by default, the terminal never synthesises arrow keys from the wheel, so scrolling cannot accidentally trigger a walk (see the "Scrolling, selection, and the mouse" requirement).

#### Scenario: Recall the last queued message
- **GIVEN** the textarea is empty and `m.pendingMessages` has at least one entry
- **WHEN** the user presses Up arrow
- **THEN** the last entry in `m.pendingMessages` is popped and inserted into the textarea with the cursor at the end

#### Scenario: Bash-style walk through submitted messages
- **GIVEN** the textarea is empty and there are no queued messages
- **WHEN** the user presses Up arrow
- **THEN** a walk back through previously submitted user messages begins using `historyBrowseIndex` / `historyBrowseValue`
- **AND** repeated Up steps older, Down steps newer, and reaching the newest entry clears the input

#### Scenario: Editing ends the walk
- **GIVEN** the user is walking through previously submitted messages
- **WHEN** the recalled text is edited
- **THEN** the walk ends and Up/Down revert to ordinary cursor movement within multi-line content

### Requirement: Streaming animation

When a message is sent, the system SHALL immediately append an assistant message with `streaming: true` to the display so there is always visible feedback. A braille spinner (`⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`) SHALL animate at 120 ms intervals via `spinnerTickCmd()`; each frame increments `m.spinnerFrame` and re-renders the last message line.

As delta events arrive from the gateway, the message content SHALL be built up in place. When the final event arrives, `streaming` SHALL be set to false and the spinner SHALL stop. If the final event arrives before any delta (empty response), the placeholder SHALL be removed from the display entirely.

The same spinner SHALL also decorate pending system rows (`chatMessage.pending`) — the in-flight placeholders posted by `/compact` and `/reset` after confirmation. `hasStreamingMessage()` SHALL treat any pending system row as a reason to keep ticking, and the result handler SHALL swap the placeholder for the outcome via `replacePendingSystem` so the spinner is replaced in place rather than appended after. The confirmation-pattern wiring is captured in the `commands` spec, and the rendering side is captured in the `message-rendering` spec.

#### Scenario: Placeholder appears immediately on send
- **WHEN** a message is sent
- **THEN** an assistant message with `streaming: true` is appended immediately
- **AND** the braille spinner `⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏` animates at 120 ms intervals via `spinnerTickCmd()`, incrementing `m.spinnerFrame` and re-rendering the last message line

#### Scenario: Deltas build content, final stops the spinner
- **GIVEN** a streaming assistant message
- **WHEN** delta events arrive and then the final event arrives
- **THEN** the content is built up in place, `streaming` is set to false, and the spinner stops

#### Scenario: Empty response removes the placeholder
- **GIVEN** a streaming placeholder is shown
- **WHEN** the final event arrives before any delta
- **THEN** the placeholder is removed from the display entirely

#### Scenario: Pending system row keeps the spinner ticking
- **GIVEN** a pending system row from `/compact` or `/reset` (`chatMessage.pending`)
- **WHEN** `hasStreamingMessage()` is evaluated
- **THEN** it treats the pending row as a reason to keep ticking, and `replacePendingSystem` swaps the placeholder for the outcome in place

### Requirement: Thinking levels

The gateway SHALL support extended thinking for supported models. The level SHALL be stored in `m.thinkingLevel` and displayed in the header bar when set and not `"off"`. Valid levels SHALL be: `off`, `minimal`, `low`, `medium`, `high`.

`/think` (no argument) SHALL show the current level. `/think <level>` SHALL validate the input and call `client.SessionPatchThinking(sessionKey, level)` (command dispatch is captured in the `commands` spec). The spinner SHALL also appear while the model is thinking before any response deltas arrive, giving immediate feedback after sending.

#### Scenario: Show the current thinking level
- **WHEN** the user runs `/think` with no argument
- **THEN** the current level is shown

#### Scenario: Set a valid thinking level
- **WHEN** the user runs `/think <level>` with one of `off`, `minimal`, `low`, `medium`, `high`
- **THEN** the input is validated and `client.SessionPatchThinking(sessionKey, level)` is called
- **AND** the level is displayed in the header bar when set and not `"off"`

#### Scenario: Spinner during thinking
- **GIVEN** a message has been sent and the model is thinking
- **WHEN** no response deltas have yet arrived
- **THEN** the spinner appears to give immediate feedback

### Requirement: Header bar layout

The header line SHALL show:

- **Left:** agent name · model ID (last path component) · thinking level (if set and not `off`) · connection status (only when not connected) · update-available badge (only when `prefs.UpdateChecksEnabled()` and the startup check found a newer release)
- **Right:** context usage (`tokens: 65k/1.0m (7%)  $0.42`) when the gateway has reported a context window for the active session; otherwise the legacy `tokens: 125.5K (2.3K cached)  $0.42` shape.

The renderer SHALL cap the percentage at 999% so a runaway numerator never widens the header past three digits.

#### Scenario: Right-hand context usage shape
- **GIVEN** the gateway has reported a context window for the active session
- **WHEN** the header is rendered
- **THEN** the right side shows `tokens: 65k/1.0m (7%)  $0.42`
- **AND** otherwise it shows the legacy `tokens: 125.5K (2.3K cached)  $0.42` shape

#### Scenario: Percentage capped at 999%
- **WHEN** a runaway numerator would push the percentage past three digits
- **THEN** the renderer caps the displayed percentage at 999%

### Requirement: Header context-usage and cost loading

Two independent loads SHALL feed the right-hand side of the header:

- `loadContextUsage()` SHALL populate `m.promptTokens` and `m.contextWindow`. It calls `SessionsList(agentID)`, finds the entry whose `key` matches `m.sessionKey`, and reads `totalTokens` (numerator — a per-turn prompt-token snapshot of `input + cacheRead + cacheWrite`, intentionally excluding output) and `contextTokens` (denominator), falling back to `defaults.contextTokens` when the entry omits the window. This is what makes the percentage scoped to the *current session*; the older approach of calling `SessionUsage("")` returned gateway-wide aggregates and pegged the value at 999%. The cmd SHALL refresh on chat init, on every `historyRefreshMsg` (so the percentage tracks turn-by-turn), and on `modelSwitchedMsg` (a new model can change the window). The handler SHALL discard results whose `sessionKey` no longer matches `m.sessionKey` so a navigated-away-and-back race cannot apply a stale snapshot.
- `loadStats()` SHALL continue to call `client.SessionUsage()` for the cumulative cost figure shown on the right of the header (and for the `/stats` table). The percentage display SHALL fall back to its token+cache layout when `loadContextUsage` has not produced a window yet.

#### Scenario: Session-scoped context usage
- **WHEN** `loadContextUsage()` runs
- **THEN** it calls `SessionsList(agentID)`, matches the entry whose `key` is `m.sessionKey`, and reads `totalTokens` (numerator, `input + cacheRead + cacheWrite`, excluding output) and `contextTokens` (denominator)
- **AND** falls back to `defaults.contextTokens` when the entry omits the window

#### Scenario: Refresh triggers
- **WHEN** chat init, a `historyRefreshMsg`, or a `modelSwitchedMsg` occurs
- **THEN** `loadContextUsage()` refreshes so the percentage tracks turn-by-turn and picks up a new model's window

#### Scenario: Stale snapshot discarded
- **GIVEN** the user navigated away and back
- **WHEN** a context-usage result arrives whose `sessionKey` no longer matches `m.sessionKey`
- **THEN** the handler discards it

#### Scenario: Cost figure and fallback layout
- **WHEN** `loadStats()` runs
- **THEN** it calls `client.SessionUsage()` for the cumulative cost figure (also used by the `/stats` table)
- **AND** the percentage display falls back to its token+cache layout when `loadContextUsage` has not produced a window yet

### Requirement: Per-agent header colour override

The header background SHALL default to the accent purple but MAY be overridden per agent with `/header <hex>` (see the `commands` spec). The override SHALL be stored against the agent ID under `prefs.Agents[agentID].HeaderColor`; the renderer SHALL call `prefs.HeaderColorFor(m.agentID)` each frame and apply the colour to both `headerStyle` and the warn-badge background. Switching to another agent SHALL pick up that agent's own colour (or the default).

#### Scenario: Override the header colour for an agent
- **WHEN** the user runs `/header <hex>`
- **THEN** the colour is stored under `prefs.Agents[agentID].HeaderColor`
- **AND** the renderer applies `prefs.HeaderColorFor(m.agentID)` each frame to both `headerStyle` and the warn-badge background

#### Scenario: Switching agents picks up per-agent colour
- **WHEN** the user switches to another agent
- **THEN** the header uses that agent's own colour, or the default if none is set

### Requirement: Connection-status badge

The connection-status badge SHALL be rendered in the error colour and SHALL appear only when the gateway connection is degraded:

| Badge | Meaning |
|---|---|
| `⚠ disconnected` | The supervisor has just observed the WebSocket close; reconnect not yet attempted. |
| `⟳ reconnecting` (or `attempt N`) | A reconnect attempt is in progress. The attempt counter is shown from the second attempt onwards. |
| `✖ auth failed` | The gateway rejected the device token mid-session. The supervisor has stopped retrying — open `/connections` and re-pick the same connection so the connecting view's auth-recovery modal can prompt for a fix. |

A matching one-line system message SHALL also be added to the chat scrollback on disconnect (`Lost gateway connection — attempting to reconnect…`) and on recovery (`Reconnected to gateway.`) so the event is visible even after the badge clears. The full lifecycle is captured in the `authentication` spec.

#### Scenario: Disconnected badge on WebSocket close
- **WHEN** the supervisor observes the WebSocket close before a reconnect attempt
- **THEN** the header shows `⚠ disconnected` in the error colour

#### Scenario: Reconnecting badge with attempt counter
- **GIVEN** a reconnect attempt is in progress
- **WHEN** the header renders
- **THEN** it shows `⟳ reconnecting` (with `attempt N` from the second attempt onwards)

#### Scenario: Auth-failed badge stops retrying
- **GIVEN** the gateway rejected the device token mid-session
- **WHEN** the header renders
- **THEN** it shows `✖ auth failed` and the supervisor has stopped retrying
- **AND** the user must open `/connections` and re-pick the same connection so the auth-recovery modal can prompt for a fix

#### Scenario: Scrollback notes on disconnect and recovery
- **WHEN** the connection drops and later recovers
- **THEN** a one-line system message `Lost gateway connection — attempting to reconnect…` is added on disconnect and `Reconnected to gateway.` on recovery, visible even after the badge clears

### Requirement: Update-available badge

A second header badge — `↑ vX.Y.Z`, rendered in the same warn style as `⚠ disconnected` — SHALL appear when the daily startup update check finds a newer release. The check SHALL be owned by `internal/update`: a single `GET https://lucinate.ai/latest.json` with a 5-second timeout, fired once per day from `AppModel.Init()`. If anything goes wrong (offline, captive portal, malformed manifest, non-stable build), `update.Check` SHALL return `nil, nil` — no badge, no error.

The badge SHALL be suppressed once the user has seen it: `prefs.LatestSeenVersion` records the manifest version on every successful check, and the badge SHALL appear only when the manifest moves *past* that version. The whole feature MAY be toggled off via the `Check for updates on startup` row in `/settings`, or set `LUCINATE_DISABLE_UPDATE_CHECK=1` for an unconditional opt-out (useful in CI). The manifest URL itself MAY be overridden via `LUCINATE_UPDATE_MANIFEST_URL` for local testing.

#### Scenario: Badge appears for a newer release
- **GIVEN** update checks are enabled
- **WHEN** the daily `GET https://lucinate.ai/latest.json` (5-second timeout, once per day from `AppModel.Init()`) finds a version past `prefs.LatestSeenVersion`
- **THEN** the `↑ vX.Y.Z` badge appears in the warn style

#### Scenario: Check failure is silent
- **WHEN** the check hits an error (offline, captive portal, malformed manifest, non-stable build)
- **THEN** `update.Check` returns `nil, nil` with no badge and no error

#### Scenario: Opting out of the update check
- **WHEN** the `Check for updates on startup` row in `/settings` is off, or `LUCINATE_DISABLE_UPDATE_CHECK=1` is set
- **THEN** the update check is disabled
- **AND** `LUCINATE_UPDATE_MANIFEST_URL` may override the manifest URL for local testing

### Requirement: View region order

`chatModel.View()` (`internal/tui/chat.go`) SHALL assemble the chat view top→bottom in this fixed order. Every region between the header and the input SHALL reserve conversation-viewport height via `applyLayout`, and each SHALL render only when it has content:

1. **Header** — the status bar (connection, agent, model, token/cost).
2. **Info notifications** — informational one-shots such as `copied … to clipboard`, pinned just below the header.
3. **Conversation viewport** — the scrollable transcript.
4. **Completion menu** — slash-command / mention candidates, while active.
5. **Routine status row** — when a routine is active.
6. **Tool-activity strip** — what the agent is running this turn (collapses to a summary when idle).
7. **Queued-message footer** — messages typed while a turn streams, awaiting dispatch.
8. **Error notifications** — error one-shots; the bottommost region above the input.
9. **Input box.**
10. **Help line.**

Informational and error notifications SHALL be deliberately split to opposite ends. An informational confirmation reads naturally at the top beside the status bar, while an error surfaces at the bottom next to the input, where the user will act on it. Both SHALL be drawn from the same `chatModel.notifications` store, filtered on the `isError` flag by `renderInfoNotifications` / `renderErrorNotifications`.

#### Scenario: Fixed top-to-bottom region order
- **WHEN** `chatModel.View()` assembles the chat view
- **THEN** regions render in order: header, info notifications, conversation viewport, completion menu, routine status row, tool-activity strip, queued-message footer, error notifications, input box, help line
- **AND** each region between header and input reserves viewport height via `applyLayout` and renders only when it has content

#### Scenario: Notifications split to opposite ends
- **GIVEN** the shared `chatModel.notifications` store filtered on `isError`
- **WHEN** notifications render
- **THEN** informational rows pin to the top just below the header via `renderInfoNotifications` and error rows drop to the bottommost region above the input via `renderErrorNotifications`

### Requirement: Notifications

Ephemeral state messages — confirmation prompts, cancel acks, routine state changes (see the `routines` spec) — SHALL render as one or more width-padded rows, styled with `statusStyle` (or `errorStyle` for is-error rows). They SHALL be split by kind: informational rows pin to the top just below the header, and error rows drop to the bottommost region below the queued-message footer (see the "View region order" requirement for how they sit relative to the other regions).

The store SHALL be `chatModel.notifications []notification` (`internal/tui/notifications.go`). Notifications SHALL live outside `m.messages`, so they survive `historyRefreshMsg` and never reach the gateway. They SHALL be cleared at the top of the Enter handler whenever the user submits a non-empty input, and on the empty-Enter routine-advance path — the assumption is that any state worth showing in a notification has been read or no longer applies once the user takes their next action.

`m.notify(text)`, `m.notifyError(text)`, and `m.clearNotifications()` SHALL be the only entry points; each SHALL call `applyLayout()` so the conversation viewport reflows when notifications appear or disappear. Empty text SHALL be dropped silently so callers can `notify(maybeFmt(...))` without a guard.

This replaced the older pattern of appending `chatMessage{role:"system"}` rows for transient state. Persistent client-side rows (the inline `! cmd` / `!! cmd` shell-execution scrollback, gateway connect/disconnect notes, the `/help` / `/stats` / `/skills` info dumps) still go in `m.messages` because their value is in being scrollable history, not in a one-shot read.

#### Scenario: Notifications survive history refresh
- **GIVEN** notifications live in `chatModel.notifications` outside `m.messages`
- **WHEN** a `historyRefreshMsg` lands
- **THEN** notifications survive and never reach the gateway

#### Scenario: Notifications cleared on next action
- **WHEN** the user submits a non-empty input, or takes the empty-Enter routine-advance path
- **THEN** notifications are cleared at the top of the Enter handler

#### Scenario: Only entry points, reflow, empty dropped
- **WHEN** `m.notify(text)`, `m.notifyError(text)`, or `m.clearNotifications()` is called
- **THEN** each calls `applyLayout()` so the viewport reflows
- **AND** empty text is dropped silently so callers can `notify(maybeFmt(...))` without a guard

#### Scenario: Persistent rows still use the message list
- **WHEN** an inline `! cmd` / `!! cmd` shell-execution row, a gateway connect/disconnect note, or a `/help` / `/stats` / `/skills` info dump is produced
- **THEN** it goes in `m.messages` because its value is in being scrollable history, not a one-shot read

### Requirement: Routine status row

When `m.activeRoutine != nil`, a single styled row SHALL render immediately above the input box:

```
routine: demo — AUTO — sent: 5/10 — next: <40-char preview>
```

`AUTO`/`MANUAL` SHALL reflect the controller's mode; `(paused)` SHALL be appended when `paused` is set. The row SHALL be sourced by `routineStatusLine()` and styled by `routineStatusStyle` in `routines_chat.go`. `applyLayout()` SHALL subtract one from the viewport height when a routine is active, mirroring the notification-row accounting. The full controller surface is captured in the `routines` spec.

#### Scenario: Routine status row rendered above the input
- **GIVEN** `m.activeRoutine != nil`
- **WHEN** the chat view renders
- **THEN** a single styled row like `routine: demo — AUTO — sent: 5/10 — next: <40-char preview>` renders immediately above the input box, sourced by `routineStatusLine()` and styled by `routineStatusStyle`
- **AND** `AUTO`/`MANUAL` reflects the controller's mode and `(paused)` is appended when `paused` is set
- **AND** `applyLayout()` subtracts one from the viewport height while a routine is active

### Requirement: Tool activity strip

When the agent invokes a tool, it SHALL appear in an ephemeral strip above the input (not in the scrollback) — name, one-line argument summary, and a state glyph that animates while running and resolves to ✓ or ✖. Once the turn finishes the strip SHALL collapse to a one-line summary (`✓ called search ×3, read ×2`) that clears when the next turn begins. Lucinate SHALL opt into the `tool-events` capability on connect; backends without tool events (e.g. the OpenAI-compatible adapter) simply never emit them. Tool output bodies are not yet expandable — the rendering contract and the open follow-up for an expand/collapse affordance are captured in the `message-rendering` spec.

#### Scenario: Tool invocation shows in the strip
- **WHEN** the agent invokes a tool
- **THEN** an ephemeral strip above the input (not the scrollback) shows the name, a one-line argument summary, and a state glyph that animates while running and resolves to ✓ or ✖

#### Scenario: Strip collapses after the turn
- **WHEN** the turn finishes
- **THEN** the strip collapses to a one-line summary such as `✓ called search ×3, read ×2`
- **AND** the summary clears when the next turn begins

#### Scenario: Backends without tool events
- **GIVEN** a backend without tool events (e.g. the OpenAI-compatible adapter)
- **WHEN** the agent runs
- **THEN** no tool events are emitted even though lucinate opts into the `tool-events` capability on connect

### Requirement: History depth

The number of messages loaded from the gateway on session init SHALL be configurable. The default SHALL be 50. It MAY be changed via `/settings` ("History limit") in steps of 10 (range 10–500). The value SHALL be stored in `prefs.HistoryLimit` and passed to `loadHistory()` as the fetch limit.

When restored history is non-empty, a dimmed separator row SHALL be rendered above the new turn, labelled with the relative time of the most recent restored message (`Resumed from 2h ago`, `…`). The label SHALL come from `formatSeparatorLabel` (`render.go`), driven by the `timestampMs` field on the synthetic separator `chatMessage` (the role is captured in the `message-rendering` spec). How history loading fits into the session lifecycle is captured in the `sessions` spec.

#### Scenario: Configurable history limit
- **WHEN** the user changes "History limit" in `/settings`
- **THEN** the value (default 50, range 10–500 in steps of 10) is stored in `prefs.HistoryLimit` and passed to `loadHistory()` as the fetch limit

#### Scenario: Resumed-from separator on non-empty history
- **GIVEN** restored history is non-empty
- **WHEN** the chat view renders
- **THEN** a dimmed separator row appears above the new turn labelled with the relative time of the most recent restored message (e.g. `Resumed from 2h ago`), from `formatSeparatorLabel` driven by the `timestampMs` field on the synthetic separator `chatMessage`

### Requirement: Mid-turn history resync

After a turn finalises, the chat view SHALL fetch `chat.history` from the gateway and merge it into `m.messages`. The merge SHALL NOT be a wholesale replacement — that would wipe live state (the next routine step's placeholder, a system row the user just took an action on). (Tool activity lives outside `m.messages`, so it is untouched by the merge.)

The mechanism SHALL be a generation counter:

- `chatModel.gen` is a monotonically increasing `uint64` (starts at 1).
- Every `appendMessage(...)` stamps the current `m.gen` onto the row.
- A successful `final` (or `error`/`aborted`) calls `bumpGen()`, which captures the current value as the "boundary" and then advances the counter. The boundary is what the just-issued `refreshHistoryAt(boundary)` carries.
- When the resulting `historyRefreshMsg` lands, `mergeHistoryRefresh(server, boundary)` keeps every existing row with `gen > boundary` (the live tail — appended by the post-bump `drainQueue` / `maybeAdvanceRoutine` / recovery path) and prepends the server-fetched canonical state.

Practical consequences:

- Rows imported from `chat.history` carry `gen=0` (the chatMessage zero value), so any subsequent refresh treats them as history-side and replaces them cleanly.
- Tool activity is not part of `m.messages` at all (it lives in `chatModel.activeTools`, rendered in the tool-activity strip), so the merge neither preserves nor wipes it — it is reset per turn independently.
- Empty `final` acks (the gateway ping that arrives before any real content) intentionally do NOT bump the gen, because no turn has actually completed.

Tests SHALL pin the contract: `TestMergeHistoryRefresh_PreservesLiveTail`, `TestMergeHistoryRefresh_NoLiveTail`, `TestHandleEvent_FinalBumpsGen`, `TestHandleEvent_FinalEmptyAckDoesNotBumpGen`.

#### Scenario: Merge preserves the live tail
- **GIVEN** a successful `final` called `bumpGen()` capturing a boundary and `refreshHistoryAt(boundary)` was issued
- **WHEN** the `historyRefreshMsg` lands
- **THEN** `mergeHistoryRefresh(server, boundary)` keeps every existing row with `gen > boundary` (the live tail) and prepends the server-fetched canonical state, rather than doing a wholesale replacement

#### Scenario: Imported history rows are replaced cleanly
- **GIVEN** rows imported from `chat.history` carry `gen=0`
- **WHEN** a subsequent refresh runs
- **THEN** they are treated as history-side and replaced cleanly

#### Scenario: Empty final ack does not bump gen
- **WHEN** an empty `final` ack (the gateway ping before any real content) arrives
- **THEN** the gen is not bumped because no turn has actually completed

#### Scenario: Tool activity untouched by the merge
- **GIVEN** tool activity lives in `chatModel.activeTools`, outside `m.messages`
- **WHEN** the history merge runs
- **THEN** the merge neither preserves nor wipes tool activity; it is reset per turn independently

### Requirement: Connect timeout

Each (re)connect attempt SHALL have a per-attempt deadline applied to the WebSocket / HTTP handshake. The default SHALL be 15 seconds; the range SHALL be 5–300 seconds via `/settings` ("Connect timeout"). The configured value SHALL be loaded by `app.DefaultBackendFactory` (`app/factory.go`) for every backend dispatch, so the same setting governs the initial connect and the supervisor's reconnect attempts. It SHOULD be bumped when targeting a slow local LLM that cold-starts on first request.

#### Scenario: Per-attempt handshake deadline
- **WHEN** a connect or reconnect attempt runs
- **THEN** a per-attempt deadline (default 15s, range 5–300s via `/settings` "Connect timeout") applies to the WebSocket / HTTP handshake, loaded by `app.DefaultBackendFactory` for every backend dispatch so it governs both the initial connect and the supervisor's reconnect attempts

### Requirement: Scrolling, selection, and the mouse

The message history SHALL be a Bubble Tea viewport. Mouse capture (SGR cell-motion tracking) SHALL be on by default, which makes the three core interactions coexist cleanly:

- **Wheel / trackpad scrolls the history.** Wheel events SHALL be forwarded to the viewport directly. Because tracking is on, the terminal never translates the wheel into arrow keys, so scrolling cannot collide with input recall. If the user has scrolled up, new streaming output SHALL NOT yank them back down; new messages SHALL re-anchor to the bottom only when already there (`GotoBottom()` after each update).
- **Click-drag selects and copies.** Dragging over the transcript SHALL draw a reverse-video highlight; on release the selected text SHALL be copied to the clipboard (both OSC 52 through the terminal and the OS clipboard directly), with a "Copied …" confirmation row. This SHALL be implemented in `internal/tui/selection.go`. Selections SHALL be char-precise, span multiple lines, auto-scroll when dragged past the viewport edge, and strip styling from the copied text.
- **Up/Down remain input recall.** The bash-style walk through previously submitted messages (and queued-message editing) SHALL own the arrow keys exclusively; scrolling never reaches them.

Page Up/Down SHALL still scroll a page at a time. `/mouse off` SHALL opt out of capture, handing click-drag back to the terminal's native selection at the cost of wheel scrolling (the previous default behaviour); `/mouse on` SHALL restore the default.

#### Scenario: Wheel scrolls without hijacking recall
- **GIVEN** mouse capture (SGR cell-motion tracking) is on by default
- **WHEN** the user scrolls with the wheel or trackpad
- **THEN** wheel events are forwarded to the viewport directly and the terminal never translates them into arrow keys, so scrolling cannot collide with input recall

#### Scenario: Scrolled-up view is not yanked down
- **GIVEN** the user has scrolled up
- **WHEN** new streaming output arrives
- **THEN** the view is not yanked to the bottom; new messages re-anchor to the bottom only when already there (`GotoBottom()` after each update)

#### Scenario: Click-drag selects and copies
- **WHEN** the user drags over the transcript and releases
- **THEN** a reverse-video highlight is drawn and the selected text is copied to the clipboard via OSC 52 and the OS clipboard directly, with a "Copied …" confirmation row
- **AND** selections are char-precise, span multiple lines, auto-scroll past the viewport edge, and strip styling from the copied text, as implemented in `internal/tui/selection.go`

#### Scenario: Toggling mouse capture
- **WHEN** the user runs `/mouse off`
- **THEN** capture is opted out, handing click-drag back to the terminal's native selection at the cost of wheel scrolling
- **AND** `/mouse on` restores the default; Page Up/Down still scroll a page at a time
