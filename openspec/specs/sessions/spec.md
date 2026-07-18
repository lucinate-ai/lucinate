# Sessions Specification

## Purpose

A session is the unit of conversation between lucinate and an agent: it is created when a user picks an agent, restored deterministically on restart, browsed and managed from within the TUI, and fed by a queue that keeps fast typing from dropping messages. This spec covers the session lifecycle, the session browser, the compact and reset commands, and message queueing — including deterministic session keys, one-shot and launch-time overrides, and the async history and stats loads.

## Requirements

### Requirement: Session creation and deterministic keys

The system SHALL create a session when the user selects an agent in the agent picker (see the `agents` spec). It SHALL call `client.CreateSession(agentID, key)` and pass the returned `sessionKey` to `newChatModel()`. The session key SHALL be deterministic for non-default agents (based on agent ID) so the same session is restored on restart.

The same default-key rule SHALL be reused by the one-shot CLI mode: `app.Send` (`lucinate send`) calls `CreateSession` with `MainKey` for the default agent and the literal `"main"` for any other agent, so a scripted dispatch lands on the same conversation as "open the picker, pick the agent, hit enter". See the `one-shot` spec for the full lifecycle.

#### Scenario: Picking an agent creates a session

- **WHEN** the user selects an agent in the agent picker
- **THEN** `client.CreateSession(agentID, key)` is called and the returned `sessionKey` is passed to `newChatModel()`

#### Scenario: Deterministic restore on restart

- **GIVEN** a non-default agent
- **WHEN** its session is created
- **THEN** the session key is derived from the agent ID
- **AND** the same session is restored on restart

#### Scenario: One-shot dispatch lands on the same conversation

- **WHEN** `app.Send` (`lucinate send`) creates a session
- **THEN** it uses `MainKey` for the default agent and the literal `"main"` for any other agent
- **AND** the dispatch lands on the same conversation as picking the agent interactively

### Requirement: Session key override from the chat launcher

`lucinate chat --session <key>` SHALL override the default key at the picker's `CreateSession` site: `AppModel.initialSession` is consumed in the `viewSelect` block of `update`, beating both the literal `"main"` and `MainKey`. The override SHALL be one-shot — cleared once consumed so a follow-up agent pick on the same picker does not keep landing on the original key. See the `chat-launch` spec.

#### Scenario: Explicit session key beats the defaults

- **GIVEN** `lucinate chat --session <key>`
- **WHEN** the `viewSelect` block of `update` consumes `AppModel.initialSession`
- **THEN** the supplied key is used instead of the literal `"main"` or `MainKey`

#### Scenario: Override is cleared after use

- **GIVEN** a session-key override has been consumed
- **WHEN** the user picks another agent on the same picker
- **THEN** the override is no longer applied and the pick does not keep landing on the original key

### Requirement: Async history and stats load on chat init

On `chatModel.Init()`, the system SHALL run two async commands in parallel:

- `loadHistory()` — fetches the last N messages from the gateway (`client.SessionHistory()`), strips `System:` lines (see the `message-rendering` spec, history cleanup), and populates the viewport.
- `loadStats()` — fetches token usage and cost via `client.SessionUsage()` for the header bar.

History depth (N) SHALL be configurable; see the `chat-ux` spec (history depth).

#### Scenario: History and stats load in parallel

- **WHEN** `chatModel.Init()` runs
- **THEN** `loadHistory()` and `loadStats()` are dispatched in parallel

#### Scenario: History is fetched, cleaned, and rendered

- **WHEN** `loadHistory()` runs
- **THEN** it fetches the last N messages via `client.SessionHistory()`, strips `System:` lines, and populates the viewport

#### Scenario: Stats load the header bar

- **WHEN** `loadStats()` runs
- **THEN** it fetches token usage and cost via `client.SessionUsage()` for the header bar

### Requirement: Session browser

`/sessions` SHALL emit `showSessionsMsg{}`, which navigates to the sessions view (`sessionsModel` in `internal/tui/sessions.go`). The model SHALL call `client.SessionsList(agentID)` and parse the response into `sessionItem` values grouped into two lists:

- **Conversations** — regular sessions.
- **Scheduled** — sessions whose key contains `:cron:`, used by scheduled/automated agents.

Both lists SHALL be sorted by `updatedAt` descending. Selecting a session SHALL return `sessionSelectedMsg` and a new `chatModel` SHALL be constructed with the chosen key, loading its history.

#### Scenario: Opening the session browser

- **WHEN** the user runs `/sessions`
- **THEN** `showSessionsMsg{}` navigates to the sessions view (`sessionsModel`), which calls `client.SessionsList(agentID)` and parses the response into `sessionItem` values

#### Scenario: Sessions grouped and sorted

- **WHEN** the sessions list is built
- **THEN** regular sessions appear under Conversations and sessions whose key contains `:cron:` appear under Scheduled
- **AND** both lists are sorted by `updatedAt` descending

#### Scenario: Selecting a session opens its chat

- **WHEN** the user selects a session
- **THEN** `sessionSelectedMsg` is returned and a new `chatModel` is constructed with the chosen key, loading its history

### Requirement: Compact command

`/compact` SHALL use the confirmation pattern (see the `commands` spec, confirmation pattern) before taking action. It SHALL call `backend.SessionCompact()`, which summarises older messages in the session context to reduce token usage. On OpenClaw the gateway SHALL run the pass server-side; on the OpenAI-compatible backend the pass SHALL run locally (a streaming `POST /v1/chat/completions` against the agent's configured model — see the `backend-openai` spec, compaction). While the pass is in flight the confirmation handler SHALL show a pending `Compacting session…` system row with the streaming spinner attached. On success the placeholder SHALL be replaced in place with `Session compacted.`; the smaller context SHALL be picked up on the next chat send.

#### Scenario: Compact confirmed and run server-side

- **GIVEN** an OpenClaw backend
- **WHEN** the user confirms `/compact`
- **THEN** `backend.SessionCompact()` runs the pass server-side while a pending `Compacting session…` row with the streaming spinner is shown

#### Scenario: Compact run locally on the OpenAI-compatible backend

- **GIVEN** the OpenAI-compatible backend
- **WHEN** `/compact` runs
- **THEN** the pass runs locally as a streaming `POST /v1/chat/completions` against the agent's configured model

#### Scenario: Compact succeeds

- **WHEN** the compaction pass completes successfully
- **THEN** the placeholder is replaced in place with `Session compacted.`
- **AND** the smaller context is picked up on the next chat send

### Requirement: Reset command

`/reset` SHALL use the confirmation pattern (see the `commands` spec, confirmation pattern) before taking action. It SHALL call `backend.SessionDelete()` to permanently remove the session, then immediately create a replacement via `backend.CreateSession()`. While the round-trip is in flight the chat view SHALL show a pending `Clearing session…` system row with the spinner attached. On success the new session key SHALL be returned as `sessionClearedMsg{newSessionKey}` and the chat model SHALL reinitialise with an empty history; on error the placeholder SHALL be replaced in place with the error.

#### Scenario: Reset deletes and replaces the session

- **WHEN** the user confirms `/reset`
- **THEN** `backend.SessionDelete()` permanently removes the session and `backend.CreateSession()` immediately creates a replacement while a pending `Clearing session…` row with the spinner is shown

#### Scenario: Reset succeeds

- **WHEN** the reset round-trip completes successfully
- **THEN** `sessionClearedMsg{newSessionKey}` is returned and the chat model reinitialises with an empty history

#### Scenario: Reset fails

- **WHEN** the reset round-trip errors
- **THEN** the placeholder is replaced in place with the error

### Requirement: Message queueing while a response is in flight

While `m.sending == true` (a response is in flight), new user input SHALL be appended to `m.pendingMessages []string` rather than sent immediately. This prevents messages from being dropped when the user types quickly.

After each response, `drainQueue()` (in `chat.go`) SHALL:

1. If there are pending messages, dequeue the first one and send it as if the user typed it fresh (including command detection, skill prepend, etc.).
2. Refresh history once the queue is fully drained.

Local (`!`) and remote (`!!`) exec results SHALL also trigger queue draining. See the `shell-execution` spec for the exec flow.

#### Scenario: Input queued during a response

- **GIVEN** `m.sending == true`
- **WHEN** the user submits new input
- **THEN** it is appended to `m.pendingMessages` rather than sent immediately

#### Scenario: Queue drained after a response

- **WHEN** a response completes and `drainQueue()` runs
- **THEN** the first pending message is dequeued and sent as if freshly typed (including command detection, skill prepend, etc.)
- **AND** history is refreshed once the queue is fully drained

#### Scenario: Exec results trigger draining

- **WHEN** a local (`!`) or remote (`!!`) exec result arrives
- **THEN** it triggers queue draining

### Requirement: Pre-seeded queue from the chat launcher

`lucinate chat <message>` SHALL pre-seed the same queue: `newChatModel` appends the supplied text to `pendingMessages` at construction time, and the `historyLoadedMsg` handler SHALL return `m.drainQueue()` once the scrollback has rendered — so the user turn appears after the loaded history, matching what a human typing the same message would see. See the `chat-launch` spec.

#### Scenario: Launch message appears after loaded history

- **GIVEN** `lucinate chat <message>`
- **WHEN** `newChatModel` is constructed
- **THEN** the supplied text is appended to `pendingMessages`
- **AND** the `historyLoadedMsg` handler returns `m.drainQueue()` once the scrollback has rendered, so the user turn appears after the loaded history
