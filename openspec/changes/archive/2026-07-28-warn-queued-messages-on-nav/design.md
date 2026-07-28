## Context

The chat model holds a send queue (`pendingMessages`) — messages the user typed and submitted with Enter while the agent was busy (`m.sending`), which are queued rather than sent and normally flushed on turn completion. A separate gate, `gateNavigation()` (`internal/tui/routines_chat.go`), already intercepts navigations that strand or replace the chat model and, when a routine is active, asks the user to confirm before the routine's controller (and its open log file) is lost.

Nothing gave the send queue the same protection. Switching agents (`/agent`, `/agents`, `/agent <name>`) or connections (`/connections`) resolves into a fresh `newChatModel(...)`, discarding the old model and its queue with no prompt. The queued messages were never sent.

## Goals / Non-Goals

**Goals:**
- Confirm before a navigation silently discards queued messages.
- Reuse the existing confirmation mechanism rather than add a parallel one.
- Only warn when the queue is genuinely at risk, so the common case gains no friction.
- Make the prompt discoverable — next to where the user answers it.

**Non-Goals:**
- Preserving or forwarding the queued messages to the new agent/session (they were composed for the current agent; dropping-with-confirmation is the right semantic).
- Changing how or when the queue drains on the normal turn-completion path.
- Protecting the half-typed input draft (submitting `/agent` makes the draft the command itself, so there is nothing to lose there).

## Decisions

**Extend `gateNavigation` rather than add a second gate.** The routine gate already models "confirm before this navigation loses something you can't get back". A queued-message discard is the same shape, so `gateNavigation` takes a new `replacesChat bool` and builds the prompt from whichever effects apply — an active routine, queued messages, or both, joined with "and". One gate, one `pendingNavConfirm`, one resolution path. The alternative — a dedicated queued-message gate — would duplicate the confirm/resolve plumbing and risk two prompts competing for the input.

**Scope the queue guard with `replacesChat`, decided per call site.** Only navigations that rebuild the chat model wholesale lose the queue: `/agents`, `/agent`, `/agent <name>`, `/connections` pass `replacesChat=true`. Overlay navigations (`/crons`, `/sessions`, `/routines`, `/routine <name>`, `/export routine`) return to the same chat model, so the queue survives them — they pass `false` and stay silent. This keeps the routine guard (which fires for all of them) and the queue guard (which fires for four) as separate concerns on one gate. Warning about discard on an overlay that keeps the queue would be a false alarm.

**Render from `pendingNavConfirm`, pinned above the input.** The prompt was previously pushed through `m.notify()`, landing it in the top-of-screen info-notification region — far from the input and easy to miss. Instead the View renders directly from `m.pendingNavConfirm` via `renderNavConfirm()`, as a new fixed region directly above the input (below the error-notification region), styled with the accent colour so it reads as "needs an answer" without the alarm of error red. It reserves conversation-viewport height in `applyLayout` like the other regions, and the gate/resolution paths call `applyLayout` so the viewport shrinks when the prompt appears and grows back when it is answered. Rendering off `pendingNavConfirm` rather than the ephemeral notification store also means the prompt is present exactly while the question is pending and vanishes the instant it is answered.

**Resolution reuses the existing handler.** On `y` the handler aborts any in-flight turn and clears the queue (`cancelTurn`, which the queue-populated state always implies is running) and ends the routine (`endRoutine`, a no-op when none is active) before dispatching the navigation. On `n`/Esc it dismisses the prompt; the decline message reports whether a routine or the queue was being protected.

## Risks / Trade-offs

- [A chat-replacing navigation is added later without setting `replacesChat=true`] → It would silently drop the queue again. Mitigation: the flag is a required argument, so every call site must state its intent, and the `commands` spec enumerates which navigations replace the chat model.
- [The queue guard fires at command time for `/agents`/`/agent`, before the picker actually replaces the model] → If the user backs out of the picker the warning was premature. Trade-off accepted: the prompt fires at the moment the user signals intent to switch, matching how the routine guard already behaves, and a heads-up is harmless.
- [A very long prompt wraps on a narrow terminal] → `navConfirmHeight` measures the rendered height via `lipgloss.Height` rather than assuming one row, so `applyLayout` still reserves the right space.
