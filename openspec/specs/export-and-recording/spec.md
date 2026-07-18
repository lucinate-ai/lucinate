# Export and Recording Specification

## Purpose

Persist a session's conversation to disk via two slash commands: `/record` for live capture and `/export` for an after-the-fact dump. The implementation lives in `internal/tui/recorder.go` with the handlers in `internal/tui/commands.go`. Both commands draw from the same source — the canonical chat history the backend reports, never the streaming deltas — so the transcript reflects the conversation as the gateway / OpenAI-compat backend would replay it, not as the TUI rendered it. This spec covers the transcript source, on-disk file layout, the canonical-history tap, deduplication, the lifecycles of `/record` and `/export`, and the known limitations.

## Requirements

### Requirement: Transcript source is canonical history

Both `/record` and `/export` SHALL draw from the same source: the canonical chat history the backend reports, never the streaming deltas. The transcript is meant to be the conversation as the gateway / OpenAI-compat backend would replay it, not as the TUI rendered it. The system SHALL intentionally exclude streaming placeholders, the tool-activity strip, system rows (compact/reset notices, exec output, error banners) and the inline separators inserted at session resume.

#### Scenario: TUI-only rows excluded from the transcript

- **GIVEN** a session whose chat view contains streaming placeholders, a tool-activity strip, system rows (compact/reset notices, exec output, error banners) and session-resume separators
- **WHEN** the conversation is captured by `/record` or `/export`
- **THEN** only the canonical user/assistant turns the backend reports are written
- **AND** the TUI-only rows are excluded

### Requirement: Transcript file location and permissions

Transcripts SHALL be written to `<dataDir>/transcripts/` — `~/.lucinate/transcripts/` by default, or whatever `LUCINATE_DATA_DIR` / `config.SetDataDir` resolves to (see the `connections` spec for data-dir resolution). The directory SHALL be created on demand with mode `0o700`; individual files SHALL be mode `0o600`.

#### Scenario: Transcript directory created on demand

- **WHEN** a transcript is first written and `<dataDir>/transcripts/` does not exist
- **THEN** the directory is created with mode `0o700`
- **AND** the individual transcript file is written with mode `0o600`

#### Scenario: Data directory override honoured

- **GIVEN** `LUCINATE_DATA_DIR` or `config.SetDataDir` resolves the data directory to a non-default location
- **WHEN** a transcript is written
- **THEN** it is placed under `<dataDir>/transcripts/` for the resolved data directory

### Requirement: Transcript filename shape

Filenames SHALL follow `<kind>-<agent>-<session>-<UTCtimestamp>.md`, where `kind` is `record` or `export`. Agent and session names SHALL be passed through `sanitiseForPath`, which collapses anything outside `[A-Za-z0-9_-]` to `-` and trims separators — so `main session` becomes `main-session` and `Agent Name` becomes `Agent-Name`. The timestamp SHALL resolve to the second (`20060102T150405`); two recordings started inside the same second to the same session would collide and the second would truncate the first, which is fine in practice and avoided by `/record on` opening with `O_CREATE|O_TRUNC`.

#### Scenario: Names sanitised for the filesystem

- **GIVEN** an agent named `Agent Name` and a session named `main session`
- **WHEN** the transcript filename is built
- **THEN** `sanitiseForPath` yields `Agent-Name` and `main-session`, collapsing characters outside `[A-Za-z0-9_-]` to `-` and trimming separators

#### Scenario: Kind prefix distinguishes record from export

- **WHEN** a file is written by `/record`
- **THEN** its name begins with `record-`
- **AND** a file written by `/export` begins with `export-`

### Requirement: Transcript body format

The body SHALL be plain Markdown: a header listing connection / agent / model / session / timestamp, a `---` divider, then one `## User` or `## Assistant` block per turn. When a per-message `timestampMs` is reported by the backend it SHALL be appended to the heading as ` · <RFC3339>`. Assistant messages SHALL preserve any thinking content as a leading `> _thinking_` blockquote so the file is self-contained even when the chat view collapses thinking.

#### Scenario: Header and per-turn blocks

- **WHEN** a transcript is written
- **THEN** it begins with a header listing connection, agent, model, session and timestamp, followed by a `---` divider
- **AND** each turn is a `## User` or `## Assistant` block

#### Scenario: Per-message timestamp appended

- **GIVEN** the backend reports a `timestampMs` for a message
- **WHEN** that message's heading is written
- **THEN** ` · <RFC3339>` is appended to the heading

#### Scenario: Thinking content preserved

- **GIVEN** an assistant message with thinking content
- **WHEN** it is written to the transcript
- **THEN** the thinking is preserved as a leading `> _thinking_` blockquote so the file is self-contained even when the chat view collapses thinking

### Requirement: Canonical-history tap

The chat view SHALL receive canonical messages through two `tea.Msg` types, both produced by `fetchHistory` in `internal/tui/history.go`:

- `historyLoadedMsg` — initial fetch on chat-view entry.
- `historyRefreshMsg` — server-canonical resync issued from the chat-event `final`/`error`/`aborted` handlers in `internal/tui/events.go`. The merge boundary keeps the live tail intact (see `mergeHistoryRefresh` in `chat.go`) but the `messages` slice on the message itself is the authoritative gateway view of the conversation up to that point.

Both arms SHALL call `chatModel.recordCanonical(msg.messages)` after consuming the message. When `chatModel.recorder` is `nil` (recording is off) this SHALL be a no-op; otherwise the slice SHALL be forwarded to `recorder.writeNew`, which iterates and writes any rows it hasn't seen before.

#### Scenario: Initial and refresh messages both feed the recorder

- **GIVEN** the chat view processes a `historyLoadedMsg` or a `historyRefreshMsg`
- **WHEN** the message is consumed
- **THEN** `chatModel.recordCanonical(msg.messages)` is called with the authoritative gateway view

#### Scenario: Recording off is a no-op

- **GIVEN** `chatModel.recorder` is `nil`
- **WHEN** `recordCanonical` is called
- **THEN** it is a no-op
- **AND** when the recorder is non-nil the slice is forwarded to `recorder.writeNew`, which writes only rows it has not seen before

### Requirement: Deduplication of repeated canonical rows

Because each canonical refresh delivers the entire history (capped by `historyLimit`), every row arrives repeatedly, so the system SHALL deduplicate before writing. `recorder.seenSig` SHALL be a `map[string]bool` keyed by `messageSignature(role, timestampMs, sourceText)`, where `sourceText` is `chatMessage.raw` when the row was glamour-rendered and `chatMessage.content` otherwise — preferring the markdown source guarantees the dedup key matches the bytes that get written and avoids ANSI-rendered assistant turns leaking escape codes into the transcript. The hash SHALL be FNV-64a, formatted alongside role and timestamp as `role|ts|hex` so a user/assistant collision on identical content is impossible and a repeated user message at a later timestamp is recorded.

The signature set SHALL live only for the lifetime of one recording. A new `/record on` SHALL start with an empty set and reseed the file from scratch (the underlying `os.OpenFile` uses `O_TRUNC`).

#### Scenario: Repeated rows written once

- **GIVEN** a canonical refresh redelivers rows already written in this recording
- **WHEN** `recorder.writeNew` iterates the slice
- **THEN** rows whose `messageSignature(role, timestampMs, sourceText)` is already in `recorder.seenSig` are skipped

#### Scenario: Signature prefers markdown source

- **GIVEN** a glamour-rendered assistant row
- **WHEN** its signature is computed
- **THEN** `sourceText` is `chatMessage.raw` (falling back to `chatMessage.content` when not rendered), so the dedup key matches the written bytes and no ANSI escape codes leak into the transcript

#### Scenario: Signature distinctness across role and timestamp

- **GIVEN** identical content across a user and an assistant row, or a repeated user message at a later timestamp
- **WHEN** the FNV-64a signature is formatted as `role|ts|hex`
- **THEN** a user/assistant collision on identical content is impossible
- **AND** the repeated user message at the later timestamp is recorded

#### Scenario: New recording reseeds the file

- **WHEN** `/record on` is run again
- **THEN** the signature set starts empty and the file is reseeded from scratch via `os.OpenFile` with `O_TRUNC`

### Requirement: /record start lifecycle

`/record on` SHALL open the file, write the header, attach a `*transcriptRecorder` to `chatModel`, then immediately call `recordCanonical(m.messages)` so any history already loaded into the chat view is captured before the next refresh. The chat view SHALL NOT re-fetch from the gateway at this point — the assumption is that `m.messages` reflects the most recent canonical state, since `historyLoadedMsg` and every `historyRefreshMsg` have already passed through.

#### Scenario: Already-loaded history captured on start

- **WHEN** `/record on` runs
- **THEN** the file is opened, the header is written, a `*transcriptRecorder` is attached to `chatModel`, and `recordCanonical(m.messages)` is called immediately
- **AND** the chat view does not re-fetch from the gateway, relying on `m.messages` as the most recent canonical state

### Requirement: /record stop and teardown

`/record off` and `chatModel.stopRecording()` SHALL close the writer and clear the field. The chat model's teardown sites in `app.go` — `sessionSelectedMsg`, `cronTranscriptMsg`, `newSessionCreatedMsg`, `sessionCreatedMsg` — SHALL call `stopRecording` before the field is reassigned, so a recording does not silently outlive its session. Recording SHALL NOT resume on the new chat; the user has to opt in again.

#### Scenario: Explicit stop

- **WHEN** `/record off` or `chatModel.stopRecording()` is invoked
- **THEN** the writer is closed and the recorder field is cleared

#### Scenario: Recording does not outlive its session

- **GIVEN** an active recording
- **WHEN** a `sessionSelectedMsg`, `cronTranscriptMsg`, `newSessionCreatedMsg`, or `sessionCreatedMsg` teardown fires in `app.go`
- **THEN** `stopRecording` is called before the field is reassigned
- **AND** recording does not resume on the new chat until the user opts in again

### Requirement: /record write-failure handling

A write failure on `recordCanonical` (disk full, broken permissions, file removed under us) SHALL close the recorder and surface a one-shot system error in the chat view. Subsequent canonical refreshes SHALL be no-ops because the field has been cleared — the alternative was repeated error rows on every turn, which would be more annoying than informative.

#### Scenario: Write failure surfaces once

- **GIVEN** an active recording
- **WHEN** `recordCanonical` fails to write (disk full, broken permissions, file removed)
- **THEN** the recorder is closed and a one-shot system error is shown in the chat view
- **AND** subsequent canonical refreshes are no-ops because the field has been cleared

### Requirement: /export one-shot file dump

`exportTranscript` in `commands.go` SHALL be the synchronous one-shot path. It SHALL open a fresh `export-...md` file, write the same header (with `kind = "export"`), and walk the supplied `[]chatMessage` slice — typically `m.messages` — applying the same role and content filters as the recorder. Streaming rows SHALL be skipped explicitly (so an export issued mid-turn doesn't capture a half-typed assistant message); empty content SHALL be skipped; `raw` SHALL win over `content` for rendered turns. The function SHALL return the resolved path so the chat view can surface it.

#### Scenario: Synchronous export with recorder filters

- **WHEN** `/export` is run
- **THEN** `exportTranscript` opens a fresh `export-...md` file, writes the header with `kind = "export"`, and walks the supplied `[]chatMessage` slice (typically `m.messages`)
- **AND** streaming rows are skipped, empty content is skipped, and `raw` wins over `content` for rendered turns
- **AND** the resolved path is returned so the chat view can surface it

#### Scenario: Mid-turn export skips the half-typed reply

- **GIVEN** an assistant reply is still streaming
- **WHEN** `/export` is issued
- **THEN** the streaming row is skipped so the half-typed assistant message is not captured

### Requirement: /export routine prefill

`/export routine` SHALL NOT write a file. `extractUserPromptsForRoutine` SHALL walk `m.messages` for non-empty user rows (preferring `raw` over `content`) and the handler SHALL dispatch `showRoutinesMsg{prefillSteps: …}`. The `showRoutinesMsg` arm in `app.go` SHALL construct the routines model, call `routinesModel.openCreateFormWithSteps(steps)` to skip the list and drop straight into the create form, and pre-populate one step per prompt. The user then fills in the name and mode and saves through the normal routines flow (see the `routines` spec). An empty session SHALL report a no-op error rather than opening an empty form.

#### Scenario: User prompts prefill the routine create form

- **WHEN** `/export routine` is run on a non-empty session
- **THEN** `extractUserPromptsForRoutine` collects non-empty user rows (preferring `raw` over `content`), the handler dispatches `showRoutinesMsg{prefillSteps: …}`, and `app.go` opens the create form via `routinesModel.openCreateFormWithSteps(steps)` with one step per prompt
- **AND** no file is written

#### Scenario: Empty session is a no-op

- **GIVEN** a session with no user prompts
- **WHEN** `/export routine` is run
- **THEN** a no-op error is reported rather than opening an empty form

### Requirement: Backend-agnostic capture limited to user/assistant turns

Both backends SHALL expose canonical history through the same `Backend.ChatHistory` RPC, so the recorder works against OpenClaw and OpenAI-compat alike. The OpenClaw gateway, however, does not expose tool call payloads in its history view — only the user/assistant turns survive the round-trip — so tool I/O cannot be pulled from the canonical fetch today. Tool-card capture would need a separate event-side tee.

#### Scenario: Tool payloads absent from canonical history

- **GIVEN** an OpenClaw session with tool activity
- **WHEN** the transcript is captured from `Backend.ChatHistory`
- **THEN** only the user/assistant turns are recorded because the gateway does not expose tool call payloads in its history view
- **AND** capturing tool cards would require a separate event-side tee

### Requirement: No live tail capture

Streaming deltas SHALL be intentionally excluded from transcripts. A recording started mid-turn SHALL capture the assistant reply only after `final` lands and the refresh fires.

#### Scenario: Mid-turn recording waits for final

- **GIVEN** `/record on` is run while an assistant reply is streaming
- **WHEN** the turn completes
- **THEN** the assistant reply is captured only after `final` lands and the canonical refresh fires, not from the streaming deltas

### Requirement: History-limit constraint

`historyLimit` (a chat-view preference, see the `chat-ux` spec) SHALL cap the canonical fetch. For very long sessions, older turns may have already fallen off the window when `/record on` runs — the recorder only sees what `fetchHistory` returns. `/export` SHALL have the same constraint.

#### Scenario: Older turns beyond the window are unavailable

- **GIVEN** a session longer than `historyLimit`
- **WHEN** `/record on` or `/export` runs
- **THEN** only the turns within the capped canonical fetch returned by `fetchHistory` are captured; older turns that fell off the window are unavailable

### Requirement: Filename collision within the same second

Two recordings or exports started inside the same UTC second to the same session SHALL collide and the second SHALL truncate the first. This is practically impossible for `/record` and plausible only for tight scripted `/export` loops.

#### Scenario: Same-second collision truncates

- **GIVEN** two exports started inside the same UTC second for the same session
- **WHEN** the second file is opened
- **THEN** it resolves to the same filename and truncates the first

### Requirement: Test coverage

`internal/tui/recorder_test.go` SHALL cover the header layout, dedup across refreshes, role/streaming filters, raw-vs-rendered fidelity, the export round-trip, routine extraction, path sanitisation, filename shape, and signature distinctness. The tests SHALL use `config.SetDataDir(t.TempDir())` so they don't touch the user's real `~/.lucinate`.

#### Scenario: Tests isolated from the user's data directory

- **WHEN** the recorder tests run
- **THEN** they exercise header layout, dedup across refreshes, role/streaming filters, raw-vs-rendered fidelity, the export round-trip, routine extraction, path sanitisation, filename shape, and signature distinctness
- **AND** they use `config.SetDataDir(t.TempDir())` so the user's real `~/.lucinate` is untouched
