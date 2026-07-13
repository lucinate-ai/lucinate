# Message rendering

## Message roles

Each chat message has a role that determines how it is displayed in the TUI:

- `user` — sent by the local user; shown right-aligned.
- `assistant` — returned by the agent; rendered as markdown via Glamour.
- `system` — local-only notices (errors, status, command output); shown in a muted style and never sent to the gateway.
- `separator` — a dim divider row inserted between restored history and a new turn; labelled with the relative time of the most recent restored message (e.g. `Resumed from 2h ago`). The `timestampMs` field on `chatMessage` carries the unix-ms used by `formatSeparatorLabel`.

Tool calls are **not** message rows. They are tracked separately and rendered in an ephemeral strip above the input — see [Tool activity strip](#tool-activity-strip).

## Hiding injected content from history

Some content needs to be sent to the gateway as part of a user message (so the agent sees it) but must not be shown in the local chat history when it is loaded back. Lucinate uses two complementary conventions for this.

### System: line prefix

Each line of the block is prefixed with `System: ` (or `System (<qualifier>): ` for gateway-rewritten variants like `System (untrusted):`). `prefixAllLines()` in `internal/tui/skills.go` applies the prefix; `stripSystemLines()` in `internal/tui/history.go` removes matching lines on history reload, and `isSystemLine()` recognises both forms.

This convention is used for the **skill catalog** prepended to the first user message of each session — see [skills.md](skills.md) and [backend_openclaw.md](backend_openclaw.md).

```
System: Available agent skills (activate with /skill-name):
System:   - review: Perform a code review
```

### `<local-agent-skill>` envelope

When the user invokes a skill (`/review` alone or `use /review on x`), `expandSkillReferences()` in `internal/tui/skills.go` produces a payload prefixed with `Please use the following skill(s):` followed by one or more `<local-agent-skill name="…">…</local-agent-skill>` blocks. `stripLocalAgentSkillBlocks()` in `internal/tui/history.go` elides the preamble line and every block (inclusive) on history reload. See [skills.md](skills.md#activation) for the full payload shape and substitution rules.

Both strippers are applied to user messages on history reload.

## Markdown rendering

Assistant messages are conditionally rendered with Glamour. `looksLikeMarkdown()` in `internal/tui/history.go` checks for code fences, bold markers, pipe tables, list prefixes, headings, and numbered lists. If none are found the text is shown as plain text, avoiding unwanted paragraph indentation on short replies.

The Glamour renderer is created in `setSize()` with a wrap width equal to the terminal width minus 4. `wordWrap()` in `render.go` is applied after rendering and preserves lines containing box-drawing characters (table borders) so they are not split.

## Tool activity strip

When the agent invokes a tool, lucinate shows it in an ephemeral **activity strip** rendered directly above the input, not inline in the scrollback. The strip is driven by `agent` events with `stream == "tool"` from the gateway — declared via the `tool-events` capability on connect (see `internal/client/client.go`).

Tool calls are deliberately kept **off** the message list (`chatModel.activeTools`, a `[]toolActivity`). An earlier design appended a `role: "tool"` message per call, which froze the streaming assistant row so each subsequent tool split the reply onto a fresh row. Because the gateway streams the whole turn's text *cumulatively* (every delta carries all text so far), that split made each post-tool row re-render everything before it — the "delta accumulation / repeated content" bug. Keeping tools out of the message list leaves the streaming assistant row intact, so the whole turn lands on one row and the cumulative full-replace is correct.

While a turn is in flight (or any tool is still running), the strip lists each tool on its own line — a state glyph, the tool name, and a one-line argument summary:

```
✓ search (query="hello world")
⠋ read (path="/big/file")
```

The running glyph cycles through the same braille spinner as the streaming cursor (`⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`), then flips to `✓` on success or `✖` on error (errors append a one-line message extracted from the tool result). When more than `maxToolStripRows` tools have run, the oldest fold into a leading `…N earlier` line so the newest stay visible.

Once the turn is idle and every tool has resolved, the strip collapses to a single summary line grouping calls by name, which persists until the next turn begins:

```
✓ called search ×3, read ×2
✖ called fetch ×2 (1 failed)
```

The mapping from event payload to strip lives in `handleAgentEvent` (`internal/tui/events.go`):

- `phase: "start"` — appends a `toolActivity{state: "running"}`. The streaming assistant row is left untouched.
- `phase: "update"` — currently a no-op. Partial result streaming is deferred (see issue for expand/collapse output).
- `phase: "result"` — finds the matching entry by `callID` and flips its state to `"success"` or `"error"`.

The strip is cleared at the start of each turn (`resetToolActivity`), and on error/aborted so a tool left mid-run can't strand a spinning glyph. Its rendered height is subtracted from the viewport in `applyLayout` (via `toolStripHeight`), the same way the completion menu and notification rows reserve space.

The `summariseArgs` helper picks a human-readable key from the args object (priority order: `command`, `path`, `file`, `filePath`, `query`, `url`, `name`, `message`, `text`) or falls back to compact JSON, truncated to 80 runes.

The full output of a tool call is not currently rendered; only the header line is. An expand/collapse affordance similar to the official OpenClaw TUI's Ctrl+O is tracked separately.

## Streaming placeholder

When the user sends a message, an empty assistant message with `streaming: true` is appended immediately so there is always something to animate (see [chat-ux.md](chat-ux.md#streaming-animation)). As delta events arrive, the message content is built up incrementally. If the final event arrives before any delta (e.g. an error), the placeholder message is removed from the display.

The same spinner glyph also decorates *pending system rows* — `chatMessage.pending` flags a system message as in-flight, and the renderer appends the current spinner frame after the body. `/compact` and `/reset` use this to give visible feedback while their actions run; the result handler clears `pending` (or replaces the row entirely) once the outcome lands.
