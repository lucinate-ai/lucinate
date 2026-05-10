# Export and recording

Two slash commands let the user persist a session's conversation to disk: `/record` for live capture and `/export` for an after-the-fact dump. The implementation lives in `internal/tui/recorder.go` and the handlers in `internal/tui/commands.go`.

Both commands draw from the same source: the canonical chat history the backend reports, never the streaming deltas. Streaming placeholders, tool cards, system rows (compact/reset notices, exec output, error banners) and the inline separators inserted at session resume are intentionally excluded — the transcript is meant to be the conversation as the gateway / OpenAI-compat backend would replay it, not as the TUI rendered it.

## Files on disk

Transcripts are written to `<dataDir>/transcripts/` — `~/.lucinate/transcripts/` by default, or whatever `LUCINATE_DATA_DIR` / `config.SetDataDir` resolves to (see [connections.md](connections.md) for data-dir resolution). The directory is created on demand with mode `0o700`; individual files are mode `0o600`.

Filenames follow `<kind>-<agent>-<session>-<UTCtimestamp>.md`, where `kind` is `record` or `export`. Agent and session names are passed through `sanitiseForPath`, which collapses anything outside `[A-Za-z0-9_-]` to `-` and trims separators — so `main session` becomes `main-session`, `Agent Name` becomes `Agent-Name`. The timestamp resolves to the second (`20060102T150405`); two recordings started inside the same second to the same session would collide and the second would truncate the first, which is fine in practice and avoided by `/record on` opening with `O_CREATE|O_TRUNC`.

The body is plain Markdown: a header listing connection / agent / model / session / timestamp, a `---` divider, then one `## User` or `## Assistant` block per turn. When a per-message `timestampMs` is reported by the backend it's appended to the heading as ` · <RFC3339>`. Assistant messages preserve any thinking content as a leading `> _thinking_` blockquote so the file is self-contained even when the chat view collapses thinking.

## Canonical-history tap

The chat view receives canonical messages through two `tea.Msg` types, both produced by `fetchHistory` in `internal/tui/history.go`:

- `historyLoadedMsg` — initial fetch on chat-view entry.
- `historyRefreshMsg` — server-canonical resync issued from the chat-event `final`/`error`/`aborted` handlers in `internal/tui/events.go`. The merge boundary keeps the live tail intact (see `mergeHistoryRefresh` in `chat.go`) but the `messages` slice on the message itself is the authoritative gateway view of the conversation up to that point.

Both arms call `chatModel.recordCanonical(msg.messages)` after consuming the message. When `chatModel.recorder` is `nil` (recording is off) this is a no-op; otherwise the slice is forwarded to `recorder.writeNew`, which iterates and writes any rows it hasn't seen before.

## Dedup signature

Each canonical refresh delivers the entire history (capped by `historyLimit`), so every row arrives repeatedly. `recorder.seenSig` is a `map[string]bool` keyed by `messageSignature(role, timestampMs, sourceText)`, where `sourceText` is `chatMessage.raw` when the row was glamour-rendered and `chatMessage.content` otherwise — preferring the markdown source guarantees the dedup key matches the bytes that get written, and avoids ANSI-rendered assistant turns leaking escape codes into the transcript. The hash is FNV-64a, formatted alongside role and timestamp as `role|ts|hex` so a user/assistant collision on identical content is impossible and a repeated user message at a later timestamp is recorded.

The signature set lives only for the lifetime of one recording. A new `/record on` starts with an empty set and reseeds the file from scratch (the underlying `os.OpenFile` uses `O_TRUNC`).

## Lifecycle

`/record on` opens the file, writes the header, attaches a `*transcriptRecorder` to `chatModel`, then immediately calls `recordCanonical(m.messages)` so any history already loaded into the chat view is captured before the next refresh. The chat view does not re-fetch from the gateway at this point — the assumption is that `m.messages` reflects the most recent canonical state, since `historyLoadedMsg` and every `historyRefreshMsg` have already passed through.

`/record off` and `chatModel.stopRecording()` close the writer and clear the field. The chat model's teardown sites in `app.go` — `sessionSelectedMsg`, `cronTranscriptMsg`, `newSessionCreatedMsg`, `sessionCreatedMsg` — call `stopRecording` before the field is reassigned, so a recording does not silently outlive its session. Recording does **not** resume on the new chat; the user has to opt in again.

A write failure on `recordCanonical` (disk full, broken permissions, file removed under us) closes the recorder and surfaces a one-shot system error in the chat view. Subsequent canonical refreshes are no-ops because the field has been cleared — the alternative was repeated error rows on every turn, which would be more annoying than informative.

## /export

`exportTranscript` in `commands.go` is the synchronous one-shot path. It opens a fresh `export-...md` file, writes the same header (with `kind = "export"`), and walks the supplied `[]chatMessage` slice — typically `m.messages` — applying the same role and content filters as the recorder. Streaming rows are skipped explicitly (so an export issued mid-turn doesn't capture a half-typed assistant message); empty content is skipped; `raw` wins over `content` for rendered turns. The function returns the resolved path so the chat view can surface it.

`/export routine` doesn't write a file. `extractUserPromptsForRoutine` walks `m.messages` for non-empty user rows (preferring `raw` over `content`) and the handler dispatches `showRoutinesMsg{prefillSteps: …}`. `app.go`'s `showRoutinesMsg` arm constructs the routines model, calls `routinesModel.openCreateFormWithSteps(steps)` to skip the list and drop straight into the create form, and pre-populates one step per prompt. The user fills in the name and mode, then saves through the normal routines flow (see [routines.md](routines.md)). An empty session reports a no-op error rather than opening an empty form.

## Limitations

- **Backend-agnostic but content-limited.** Both backends expose canonical history through the same `Backend.ChatHistory` RPC, so the recorder works against OpenClaw and OpenAI-compat alike. The OpenClaw gateway, however, does not expose tool call payloads in its history view — only the user/assistant turns survive the round-trip — so even if we wanted to surface tool I/O in the transcript today we couldn't pull it from the canonical fetch. Tool-card capture would need a separate event-side tee.
- **No live tail capture.** Streaming deltas are intentionally excluded. A recording started mid-turn captures the assistant reply only after `final` lands and the refresh fires.
- **History limit.** `historyLimit` (a chat-view preference, see [chat-ux.md](chat-ux.md#history-depth)) caps the canonical fetch. For very long sessions, older turns may have already fallen off the window when `/record on` runs — the recorder only sees what `fetchHistory` returns. `/export` has the same constraint.
- **Filename collisions.** Two recordings started inside the same UTC second to the same session truncate the first. Practically impossible for `/record`, plausible only for tight scripted `/export` loops.

## Tests

`internal/tui/recorder_test.go` covers the header layout, dedup across refreshes, role/streaming filters, raw-vs-rendered fidelity, the export round-trip, routine extraction, path sanitisation, filename shape, and signature distinctness. The tests use `config.SetDataDir(t.TempDir())` so they don't touch the user's real `~/.lucinate`.
