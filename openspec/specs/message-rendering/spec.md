# Message Rendering Specification

## Purpose

Define how chat messages are displayed in the TUI: the role each message carries and its visual treatment, the conventions that keep injected content out of restored history, conditional markdown rendering of assistant replies, the ephemeral tool-activity strip that keeps tool calls off the message list, the streaming placeholder, and the queued-message footer. Together these rules keep the transcript readable and its reading order intuitive while the agent streams a turn.

## Requirements

### Requirement: Message roles and their display

Each chat message SHALL carry a role that determines how it is displayed in the TUI. The system SHALL support the following roles:

- `user` — sent by the local user; shown right-aligned.
- `assistant` — returned by the agent; rendered as markdown via Glamour.
- `system` — local-only notices (errors, status, command output); shown in a muted style and never sent to the gateway.
- `separator` — a dim divider row inserted between restored history and a new turn; labelled with the relative time of the most recent restored message (e.g. `Resumed from 2h ago`). The `timestampMs` field on `chatMessage` carries the unix-ms used by `formatSeparatorLabel`.

Tool calls SHALL NOT be message rows. They are tracked separately and rendered in an ephemeral strip above the input — see the tool-activity strip requirement.

#### Scenario: Assistant reply rendered as markdown

- **WHEN** a message with role `assistant` is displayed
- **THEN** it is rendered as markdown via Glamour

#### Scenario: System notice is local-only

- **GIVEN** a message with role `system`
- **THEN** it is shown in a muted style
- **AND** it is never sent to the gateway

#### Scenario: Separator labelled with relative time

- **WHEN** a `separator` row is inserted between restored history and a new turn
- **THEN** it is labelled with the relative time of the most recent restored message (e.g. `Resumed from 2h ago`) via `formatSeparatorLabel`, using the `timestampMs` field on `chatMessage`

### Requirement: Hiding injected content from restored history

Some content needs to be sent to the gateway as part of a user message (so the agent sees it) but MUST NOT be shown in the local chat history when it is loaded back. The system SHALL use two complementary conventions for this, and both strippers SHALL be applied to user messages on history reload.

**`System:` line prefix.** Each line of the block SHALL be prefixed with `System: ` (or `System (<qualifier>): ` for gateway-rewritten variants like `System (untrusted):`). `prefixAllLines()` in `internal/tui/skills.go` applies the prefix; `stripSystemLines()` in `internal/tui/history.go` removes matching lines on history reload, and `isSystemLine()` recognises both forms. This convention is used for the **skill catalog** prepended to the first user message of each session — see the `skills` spec and the `backend-openclaw` spec.

```
System: Available agent skills (activate with /skill-name):
System:   - review: Perform a code review
```

**`<local-agent-skill>` envelope.** When the user invokes a skill (`/review` alone or `use /review on x`), `expandSkillReferences()` in `internal/tui/skills.go` SHALL produce a payload prefixed with `Please use the following skill(s):` followed by one or more `<local-agent-skill name="…">…</local-agent-skill>` blocks. `stripLocalAgentSkillBlocks()` in `internal/tui/history.go` SHALL elide the preamble line and every block (inclusive) on history reload. See the `skills` spec for the full payload shape and substitution rules.

#### Scenario: System-prefixed lines removed on reload

- **GIVEN** a user message whose lines are prefixed `System: ` or `System (<qualifier>): ` (e.g. `System (untrusted):`)
- **WHEN** history is reloaded
- **THEN** `stripSystemLines()` removes the matching lines, recognised by `isSystemLine()` in both forms

#### Scenario: Skill envelope elided on reload

- **GIVEN** a user message containing the `Please use the following skill(s):` preamble followed by one or more `<local-agent-skill name="…">…</local-agent-skill>` blocks
- **WHEN** history is reloaded
- **THEN** `stripLocalAgentSkillBlocks()` elides the preamble line and every block inclusive

### Requirement: Conditional markdown rendering of assistant messages

Assistant messages SHALL be conditionally rendered with Glamour. `looksLikeMarkdown()` in `internal/tui/history.go` SHALL check for code fences, bold markers, pipe tables, list prefixes, headings, and numbered lists; if none are found the text SHALL be shown as plain text, avoiding unwanted paragraph indentation on short replies. The Glamour renderer SHALL be created in `setSize()` with a wrap width equal to the terminal width minus 4. `wordWrap()` in `render.go` SHALL be applied after rendering and SHALL preserve lines containing box-drawing characters (table borders) so they are not split.

#### Scenario: Short plain reply not rendered as markdown

- **GIVEN** an assistant message with none of code fences, bold markers, pipe tables, list prefixes, headings, or numbered lists
- **WHEN** `looksLikeMarkdown()` evaluates it
- **THEN** it is shown as plain text, avoiding unwanted paragraph indentation

#### Scenario: Table borders preserved on wrap

- **WHEN** `wordWrap()` runs after rendering, with the Glamour wrap width set to the terminal width minus 4
- **THEN** lines containing box-drawing characters (table borders) are preserved and not split

### Requirement: Tool-activity strip

When the agent invokes a tool, the system SHALL show it in an ephemeral **activity strip** rendered directly above the input, not inline in the scrollback. The strip SHALL be driven by `agent` events with `stream == "tool"` from the gateway — declared via the `tool-events` capability on connect (see `internal/client/client.go`).

Tool calls SHALL be kept **off** the message list (`chatModel.activeTools`, a `[]toolActivity`). An earlier design appended a `role: "tool"` message per call, which froze the streaming assistant row so each subsequent tool split the reply onto a fresh row. Because the gateway streams the whole turn's text *cumulatively* (every delta carries all text so far), that split made each post-tool row re-render everything before it — the "delta accumulation / repeated content" bug. Keeping tools out of the message list leaves the streaming assistant row intact, so the whole turn lands on one row and the cumulative full-replace is correct.

While a turn is in flight (or any tool is still running), the strip SHALL list each tool on its own line — a state glyph, the tool name, and a one-line argument summary:

```
✓ search (query="hello world")
⠋ read (path="/big/file")
```

The running glyph SHALL cycle through the same braille spinner as the streaming cursor (`⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`), then flip to `✓` on success or `✖` on error (errors append a one-line message extracted from the tool result). When more than `maxToolStripRows` tools have run, the oldest SHALL fold into a leading `…N earlier` line so the newest stay visible.

Once the turn is idle and every tool has resolved, the strip SHALL collapse to a single summary line grouping calls by name, which persists until the next turn begins:

```
✓ called search ×3, read ×2
✖ called fetch ×2 (1 failed)
```

The mapping from event payload to strip lives in `handleAgentEvent` (`internal/tui/events.go`):

- `phase: "start"` — appends a `toolActivity{state: "running"}`. The streaming assistant row is left untouched.
- `phase: "update"` — currently a no-op. Partial result streaming is deferred (see issue for expand/collapse output).
- `phase: "result"` — finds the matching entry by `callID` and flips its state to `"success"` or `"error"`.

The strip SHALL be cleared at the start of each turn (`resetToolActivity`), and on error/aborted so a tool left mid-run cannot strand a spinning glyph. Its rendered height SHALL be subtracted from the viewport in `applyLayout` (via `toolStripHeight`), the same way the completion menu and notification rows reserve space. The `summariseArgs` helper SHALL pick a human-readable key from the args object (priority order: `command`, `path`, `file`, `filePath`, `query`, `url`, `name`, `message`, `text`) or fall back to compact JSON, truncated to 80 runes. The full output of a tool call is not currently rendered; only the header line is. An expand/collapse affordance similar to the official OpenClaw TUI's Ctrl+O is tracked separately.

#### Scenario: Tool call rendered in the strip, not the scrollback

- **GIVEN** the agent invokes a tool, surfaced as an `agent` event with `stream == "tool"` (enabled via the `tool-events` capability)
- **WHEN** `handleAgentEvent` receives `phase: "start"`
- **THEN** a `toolActivity{state: "running"}` is appended to `chatModel.activeTools` and shown in the strip above the input
- **AND** the streaming assistant row is left untouched, keeping the whole turn on one row so the cumulative full-replace stays correct

#### Scenario: Tool result flips its glyph

- **WHEN** `handleAgentEvent` receives `phase: "result"`
- **THEN** it finds the matching entry by `callID` and flips its state to `"success"` (`✓`) or `"error"` (`✖`), appending a one-line message extracted from the tool result on error

#### Scenario: Oldest tools folded when the strip overflows

- **WHEN** more than `maxToolStripRows` tools have run
- **THEN** the oldest fold into a leading `…N earlier` line so the newest stay visible

#### Scenario: Strip collapses to a summary when idle

- **WHEN** the turn is idle and every tool has resolved
- **THEN** the strip collapses to a single summary line grouping calls by name (e.g. `✓ called search ×3, read ×2`), which persists until the next turn begins

#### Scenario: Strip cleared to avoid a stranded spinner

- **WHEN** a new turn starts (`resetToolActivity`), or the turn errors or is aborted
- **THEN** the strip is cleared so a tool left mid-run cannot strand a spinning glyph

#### Scenario: Argument summary key selection

- **WHEN** `summariseArgs` builds the one-line argument summary
- **THEN** it picks the first present key in priority order `command`, `path`, `file`, `filePath`, `query`, `url`, `name`, `message`, `text`, or falls back to compact JSON, truncated to 80 runes

### Requirement: Streaming placeholder and pending-row spinner

When the user sends a message, the system SHALL immediately append an empty assistant message with `streaming: true` so there is always something to animate (see the `chat-ux` spec for the streaming animation). As delta events arrive, the message content SHALL be built up incrementally. If the final event arrives before any delta (e.g. an error), the placeholder message SHALL be removed from the display.

The same spinner glyph SHALL also decorate *pending system rows* — `chatMessage.pending` flags a system message as in-flight, and the renderer appends the current spinner frame after the body. `/compact` and `/reset` use this to give visible feedback while their actions run; the result handler SHALL clear `pending` (or replace the row entirely) once the outcome lands.

#### Scenario: Placeholder appended on send

- **WHEN** the user sends a message
- **THEN** an empty assistant message with `streaming: true` is appended immediately
- **AND** its content is built up incrementally as delta events arrive

#### Scenario: Error before any delta removes the placeholder

- **GIVEN** an assistant placeholder is streaming
- **WHEN** the final event arrives before any delta (e.g. an error)
- **THEN** the placeholder message is removed from the display

#### Scenario: Pending system row shows a spinner

- **GIVEN** a system message with `chatMessage.pending` set (e.g. from `/compact` or `/reset`)
- **WHEN** it is rendered
- **THEN** the current spinner frame is appended after the body
- **AND** once the outcome lands the result handler clears `pending` or replaces the row entirely

### Requirement: Queued-message footer

Messages the user submits while a turn is streaming SHALL be held in `chatModel.pendingMessages` and rendered by `renderPendingMessages` as dim/italic `You:` shadows in a fixed footer **below** the tool-activity strip — not in the scrollable transcript. This keeps the reading order intuitive: transcript (what happened) → tool strip (what's running) → queued shadows (what's next). Only an error notification sits lower, between the queue and the input; see the `chat-ux` spec (view region order) for the full top-to-bottom layout. The footer's height SHALL be reserved in `applyLayout` via `pendingHeight`, and `applyLayout` SHALL be re-run wherever the queue changes (enqueue, `drainQueue`, up-arrow recall, cancel) so the viewport reclaims the rows as the queue drains. Each queued message SHALL be dispatched in FIFO order by `drainQueue` once the in-flight turn finalises.

#### Scenario: Queued message shown as a footer shadow

- **GIVEN** a turn is streaming
- **WHEN** the user submits another message
- **THEN** it is held in `chatModel.pendingMessages` and rendered by `renderPendingMessages` as a dim/italic `You:` shadow in the fixed footer below the tool-activity strip, not in the scrollable transcript

#### Scenario: Viewport reclaims rows as the queue drains

- **WHEN** the queue changes (enqueue, `drainQueue`, up-arrow recall, or cancel)
- **THEN** `applyLayout` re-runs, reserving the footer height via `pendingHeight` so the viewport reclaims the rows as the queue drains

#### Scenario: Queued messages dispatched in FIFO order

- **WHEN** the in-flight turn finalises
- **THEN** `drainQueue` dispatches each queued message in FIFO order
