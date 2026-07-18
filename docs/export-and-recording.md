# Export and recording — lessons and rationale

The behavioural contract for export and recording lives in
[`openspec/specs/export-and-recording/spec.md`](../openspec/specs/export-and-recording/spec.md) —
the transcript source, on-disk file layout, the canonical-history tap, deduplication, the
`/record` and `/export` lifecycles, and the known limitations are all captured there as
requirements and scenarios. This file keeps the hard-won lessons, pitfalls, and design
rationale behind that behaviour: the "why it works this odd way" that the spec's requirements
don't dwell on.

## Why the transcript taps canonical history, not the rendered view

Both commands draw from the canonical chat history the backend reports, never the streaming
deltas. Streaming placeholders, the tool-activity strip, system rows (compact/reset notices,
exec output, error banners) and the inline separators inserted at session resume are
intentionally excluded — the transcript is meant to be the conversation as the gateway /
OpenAI-compat backend would replay it, not as the TUI rendered it.

The canonical view arrives through two `tea.Msg` types produced by `fetchHistory`:
`historyLoadedMsg` on chat-view entry, and `historyRefreshMsg`, the server-canonical resync
issued from the chat-event `final`/`error`/`aborted` handlers. The subtle bit is the refresh:
`mergeHistoryRefresh` keeps the live tail intact for display, but the `messages` slice on the
message itself is the authoritative gateway view up to that point — that slice, not the merged
display state, is what gets recorded.

## Why dedup is needed, and the `role|ts|hex` scheme

Each canonical refresh delivers the *entire* history (capped by `historyLimit`), so every row
arrives repeatedly. Without dedup the transcript would gain a fresh copy of the whole
conversation on every turn. `recorder.seenSig` guards against this, keyed by
`messageSignature(role, timestampMs, sourceText)`.

Two non-obvious choices are baked in:

- `sourceText` is `chatMessage.raw` when the row was glamour-rendered and `chatMessage.content`
  otherwise. Preferring the markdown source guarantees the dedup key matches the bytes that get
  written, and avoids ANSI-rendered assistant turns leaking escape codes into the transcript.
- The hash is FNV-64a, formatted as `role|ts|hex` rather than a bare content hash. This makes a
  user/assistant collision on identical content impossible, and lets a repeated user message at
  a later timestamp still be recorded.

The signature set lives only for the lifetime of one recording. A new `/record on` starts with
an empty set and reseeds the file from scratch — see below.

## Why `/record on` opens with `O_TRUNC`

The underlying `os.OpenFile` uses `O_CREATE|O_TRUNC`, so a new recording reseeds the file from
scratch rather than appending. This is deliberate: the empty signature set and a truncated file
have to agree, or a re-recording would re-dedup against rows it never actually wrote. Truncation
also neatly handles the same-second filename collision — two recordings started inside the same
UTC second to the same session collide, and the second truncates the first, which is fine in
practice.

## Why `/record on` captures already-loaded history immediately

`/record on` calls `recordCanonical(m.messages)` right after attaching the recorder, so any
history already in the chat view is captured before the next refresh. It deliberately does *not*
re-fetch from the gateway here — the assumption is that `m.messages` already reflects the most
recent canonical state, because `historyLoadedMsg` and every `historyRefreshMsg` have already
passed through.

## Why all four teardown sites must stop recording

The chat model's teardown sites in `app.go` — `sessionSelectedMsg`, `cronTranscriptMsg`,
`newSessionCreatedMsg`, `sessionCreatedMsg` — each call `stopRecording` before the field is
reassigned. If any one of them missed it, a recording would silently outlive its session and
keep writing another session's turns into the old file. Recording does **not** resume on the new
chat; the user has to opt in again.

## Why a write failure stops recording rather than retrying

A write failure on `recordCanonical` (disk full, broken permissions, file removed under us)
closes the recorder and surfaces a one-shot system error. Because the field is then cleared,
subsequent canonical refreshes are no-ops — the alternative was repeated error rows on every
turn, which would be more annoying than informative.

## Limitations

- **Backend-agnostic but content-limited.** Both backends expose canonical history through the
  same `Backend.ChatHistory` RPC, so the recorder works against OpenClaw and OpenAI-compat
  alike. The OpenClaw gateway, however, does not expose tool call payloads in its history view —
  only the user/assistant turns survive the round-trip — so even if we wanted to surface tool
  I/O in the transcript today we couldn't pull it from the canonical fetch. Tool-card capture
  would need a separate event-side tee.
- **No live tail capture.** Streaming deltas are intentionally excluded. A recording started
  mid-turn captures the assistant reply only after `final` lands and the refresh fires.
- **History limit.** `historyLimit` (a chat-view preference, see the `chat-ux` spec) caps the
  canonical fetch. For very long sessions, older turns may have already fallen off the window
  when `/record on` runs — the recorder only sees what `fetchHistory` returns. `/export` has the
  same constraint.
- **Filename collisions.** Two recordings started inside the same UTC second to the same session
  truncate the first. Practically impossible for `/record`, plausible only for tight scripted
  `/export` loops.
