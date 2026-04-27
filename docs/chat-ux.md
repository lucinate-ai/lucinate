# Chat UX

## Input key bindings

| Key | Action |
|---|---|
| Enter | Send message |
| Alt+Enter | Insert newline |
| Ctrl+W | Delete word backward |
| Up arrow (empty input) | Recall last sent message for editing |
| Tab | Complete slash command or skill name |
| Page Up / Page Down | Scroll message history |
| Esc | Cancel in-progress response |

Alt+Enter is configured via `ta.KeyMap.InsertNewline.SetKeys("alt+enter")` in `chat.go`. Shift+Enter is not supported — `ReportAllKeysAsEscapeCodes` is disabled to preserve shifted punctuation input.

## Message recall

When the textarea is empty and the user presses Up arrow, the last entry in `m.pendingMessages` is popped and inserted into the textarea with the cursor at the end. This allows editing and resending a recently queued message without retyping it.

## Streaming animation

When a message is sent, an assistant message with `streaming: true` is appended to the display immediately so there is always visible feedback. A braille spinner (`⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`) animates at 120 ms intervals via `spinnerTickCmd()`. Each frame increments `m.spinnerFrame` and re-renders the last message line.

As delta events arrive from the gateway, the message content is built up in place. When the final event arrives, `streaming` is set to false and the spinner stops. If the final event arrives before any delta (empty response), the placeholder is removed from the display entirely.

See [message-rendering.md](message-rendering.md#streaming-placeholder) for the rendering side of this.

## Thinking levels

The gateway supports extended thinking for supported models. The level is stored in `m.thinkingLevel` and displayed in the header bar when set and not `"off"`.

Valid levels: `off`, `minimal`, `low`, `medium`, `high`.

`/think` (no argument) shows the current level. `/think <level>` validates the input and calls `client.SessionPatchThinking(sessionKey, level)`. See [commands.md](commands.md) for the command dispatch.

The spinner also appears while the model is thinking before any response deltas arrive, giving immediate feedback after sending.

## Header bar

The header line shows:

- **Left:** agent name · model ID (last path component) · thinking level (if set and not `off`) · connection status (only when not connected)
- **Right:** token summary (input / output / cache) · total cost

Stats are loaded on init and refreshed after each message exchange via `loadStats()`. The header is re-rendered on every `statsLoadedMsg`. Token and cost values come from `client.SessionUsage()`.

The connection-status badge is rendered in the error colour and only appears when the gateway connection is degraded:

| Badge | Meaning |
|---|---|
| `⚠ disconnected` | The supervisor has just observed the WebSocket close; reconnect not yet attempted. |
| `⟳ reconnecting` (or `attempt N`) | A reconnect attempt is in progress. The attempt counter is shown from the second attempt onwards. |
| `✖ auth failed — restart` | The gateway rejected the device token after restart. The supervisor has stopped retrying — Ctrl+C and re-run `lucinate` so the interactive auth recovery flow can run on stdin. |

A matching one-line system message is also added to the chat scrollback on disconnect (`Lost gateway connection — attempting to reconnect…`) and on recovery (`Reconnected to gateway.`) so the event is visible even after the badge clears. See [authentication.md](authentication.md#reconnect-after-disconnection) for the full lifecycle.

## History depth

The number of messages loaded from the gateway on session init is configurable. The default is 50. It can be changed via `/config` ("History limit") in steps of 10 (range 10–500). The value is stored in `prefs.HistoryLimit` and passed to `loadHistory()` as the fetch limit.

See [sessions.md](sessions.md#lifecycle) for how history loading fits into the session lifecycle.

## Scrolling

The message history is a Bubble Tea viewport. Mouse wheel events are forwarded to the viewport directly. Page Up/Down and arrow keys scroll when the input is not the active focus. New messages are anchored to the bottom via `GotoBottom()` after each update.
