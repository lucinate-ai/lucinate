## Why

When the agent is busy, messages typed and submitted are held in a send queue for later delivery. Switching agents or connections rebuilds the chat model wholesale and silently drops that queue — the messages are never sent and the user gets no warning. The existing navigation gate already protects an active routine from the same class of navigations, so extending it to cover queued messages closes an easy-to-hit data-loss gap.

## What Changes

- The navigation gate now asks for a y/n confirmation before a navigation that would **discard queued messages**, not only before one that would cancel an active routine. It can therefore fire with no routine active at all.
- Only the navigations that rebuild the chat model wholesale guard the queue: `/agents`, `/agent`, `/agent <name>`, and `/connections`. Overlay navigations that return to the same chat model (`/sessions`, `/crons`, `/crons all`, `/routines`, `/routine <name>`, `/export routine`) keep the queue and do not warn about it.
- The confirmation prompt is assembled from whatever is genuinely at risk — an active routine, queued messages, or both — and reworded accordingly (e.g. `Switching agents will discard 2 queued messages. Continue? (y/n)`).
- The prompt is rendered as a band pinned **directly above the input**, where the answer is typed, rather than as a top-of-screen notification. It becomes a new fixed region in the chat view's top-to-bottom order and reserves conversation-viewport height like the other regions.
- On confirm, the in-flight turn is aborted and the queue cleared before navigating; on decline nothing is lost.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `commands`: the "Routine-active navigation gate" requirement becomes a general navigation gate that also confirms before discarding queued messages, scopes the queue guard to the chat-replacing navigations, and renders the prompt above the input rather than as a notification.
- `routines`: the slash-command gating requirement is updated for the reworded prompt and to note that the same gate now also guards queued messages (and can fire without an active routine).
- `chat-ux`: the view-region-order requirement gains the navigation-confirm prompt as a fixed region directly above the input.

## Impact

- Code: `internal/tui/routines_chat.go` (`gateNavigation`), `internal/tui/commands.go` (gate call sites), `internal/tui/chat.go` (View assembly and prompt resolution), `internal/tui/render.go` (`renderNavConfirm` / `navConfirmHeight`), `internal/tui/completion.go` (`applyLayout`), `internal/tui/styles.go` (`navConfirmStyle`).
- No API, dependency, or persisted-state changes. No breaking changes — with an empty queue and no active routine the affected navigations still run immediately with no new prompt.
