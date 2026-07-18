# Message rendering — lessons and rationale

The behavioural contract for message rendering lives in
[`openspec/specs/message-rendering/spec.md`](../openspec/specs/message-rendering/spec.md) — the
message roles and their display, injected-content hiding, conditional markdown rendering, the
tool-activity strip, the streaming placeholder, and the queued-message footer are all captured
there as requirements and scenarios. This file keeps the hard-won lessons, pitfalls, and design
rationale behind that contract: the "why it works this odd way" that the spec's requirements
don't dwell on.

## Why injected content must be hidden from restored history

Some content needs to be sent to the gateway as part of a user message (so the agent sees it) but
must not be shown in the local chat history when it is loaded back. If it isn't stripped on
reload, the skill catalog and skill envelopes reappear as raw noise in the transcript. Two
complementary conventions keep them out, and both strippers are applied to user messages on
history reload.

The **`System:` line prefix** is used for the skill catalog prepended to the first user message of
each session. Each line carries `System: ` so `stripSystemLines()` can match and remove it:

```
System: Available agent skills (activate with /skill-name):
System:   - review: Perform a code review
```

The `System (<qualifier>): ` form (e.g. `System (untrusted):`) exists for gateway-rewritten
variants — the gateway re-labels injected content it doesn't fully trust, and `isSystemLine()`
has to recognise both forms or the qualified lines leak through on reload.

The **`<local-agent-skill>` envelope** covers explicit skill invocations. `stripLocalAgentSkillBlocks()`
elides the `Please use the following skill(s):` preamble and every block inclusive; the preamble
line matters because without it the orphaned lead-in would survive the block strip.

## Markdown-detection heuristic and the box-drawing wrap gotcha

Assistant messages are only rendered through Glamour when `looksLikeMarkdown()` finds markdown
signals (code fences, bold markers, pipe tables, list prefixes, headings, numbered lists). The
point of the heuristic is to avoid Glamour's unwanted paragraph indentation on short plain
replies — running everything through the renderer looks wrong for a one-line answer.

The Glamour renderer is created with a wrap width of the terminal width **minus 4**, and
`wordWrap()` in `render.go` runs afterwards. The gotcha: `wordWrap()` must preserve lines
containing box-drawing characters (table borders), otherwise it splits table borders mid-row and
the rendered table falls apart.

## Why tool calls stay off the message list (the delta-accumulation bug)

Tool calls are deliberately kept **off** the message list (`chatModel.activeTools`) and shown in
the ephemeral strip instead. An earlier design appended a `role: "tool"` message per call, which
froze the streaming assistant row so each subsequent tool split the reply onto a fresh row.
Because the gateway streams the whole turn's text *cumulatively* (every delta carries all text so
far), that split made each post-tool row re-render everything before it — the "delta accumulation
/ repeated content" bug. Keeping tools out of the message list leaves the streaming assistant row
intact, so the whole turn lands on one row and the cumulative full-replace is correct.

The strip is also cleared on error/aborted, not just at the start of a turn, so a tool left
mid-run can't strand a spinning glyph.

## Why `summariseArgs` truncates to 80 runes

`summariseArgs` picks a human-readable key from the args object (priority order: `command`,
`path`, `file`, `filePath`, `query`, `url`, `name`, `message`, `text`) or falls back to compact
JSON. It truncates to 80 runes because the summary is a single line in the strip: an untruncated
`command` or JSON blob would blow past the terminal width and wreck the strip layout. The full
tool output isn't rendered at all yet — only the header line — with an expand/collapse affordance
(like the official OpenClaw TUI's Ctrl+O) tracked separately.
