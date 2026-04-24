# Message rendering

## Message roles

Each chat message has a role that determines how it is displayed in the TUI:

- `user` — sent by the local user; shown right-aligned.
- `assistant` — returned by the agent; rendered as markdown via Glamour.
- `system` — local-only notices (errors, status, command output); shown in a muted style and never sent to the gateway.

## The System: prefix convention

Some content needs to be sent to the gateway as part of a user message (so the agent sees it) but must not be shown in the local chat history when it is loaded back. The convention is to prefix every such line with `System: `:

```
System: Available agent skills (activate with /skill-name):
System:   - review: Perform a code review
```

`prefixAllLines()` in `internal/tui/skills.go` applies this prefix to a block of text. Content that uses this convention includes:

- The skill catalog prepended to the first user message (see [skills.md](skills.md)).
- Injected skill bodies sent when the user activates a skill.

## History cleanup

When session history is fetched from the gateway, each user message may contain `System:` prefixed lines that were injected at send time. `stripSystemLines()` in `internal/tui/history.go` removes those lines before the messages are added to the display, so the user only sees what they actually typed.

`isSystemLine()` matches two forms:

- `System: ...` — the standard local prefix.
- `System (<qualifier>): ...` — a variant the gateway may substitute (e.g. `System (untrusted):`) when rewriting message content for safety reasons.

## Markdown rendering

Assistant messages are conditionally rendered with Glamour. `looksLikeMarkdown()` in `internal/tui/history.go` checks for code fences, bold markers, pipe tables, list prefixes, headings, and numbered lists. If none are found the text is shown as plain text, avoiding unwanted paragraph indentation on short replies.

The Glamour renderer is created in `setSize()` with a wrap width equal to the terminal width minus 4. `wordWrap()` in `render.go` is applied after rendering and preserves lines containing box-drawing characters (table borders) so they are not split.

## Streaming placeholder

When the user sends a message, an empty assistant message with `streaming: true` is appended immediately so there is always something to animate (see [chat-ux.md](chat-ux.md#streaming-animation)). As delta events arrive, the message content is built up incrementally. If the final event arrives before any delta (e.g. an error), the placeholder message is removed from the display.
