## MODIFIED Requirements

### Requirement: Routine-active navigation gate

Slash commands that leave the current chat SHALL route through `gateNavigation()` (`internal/tui/routines_chat.go`), which confirms with a y/n prompt before anything the user cannot recover is discarded. The gate guards two kinds of loss — a cancelled routine and a discarded send queue — so it can fire whether or not a routine is active. Two things qualify:

- an **active routine** — every navigation that strands or replaces the chat model (`/agents`, `/agent`, `/agent <name>`, `/sessions`, `/crons`, `/crons all`, `/connections`, `/routine <name>`, `/routines`) SHALL cancel it, because the routine controller cannot survive a chat-view reset and silently dropping it would leak the open log file; and
- **queued messages** — the send queue (messages typed while the agent was busy and not yet delivered) is dropped when the chat model is rebuilt wholesale. Only the navigations that replace it SHALL guard the queue (`replacesChat=true`): `/agents`, `/agent`, `/agent <name>`, `/connections`. Overlay navigations that return to the same chat model (`/sessions`, `/crons`, `/crons all`, `/routines`, `/routine <name>`, `/export routine`) keep the queue and SHALL NOT warn about it.

When neither a routine nor — for a chat-replacing navigation — a queued message is at risk, the navigation SHALL run immediately with no prompt. Otherwise a `pendingNavConfirm` SHALL be set and its prompt SHALL be assembled from whichever effects apply — cancelling the active routine, discarding N queued messages, or both joined with "and" (e.g. `Switching agents will discard 2 queued messages. Continue? (y/n)`). The prompt SHALL be rendered as a band pinned directly above the input (`renderNavConfirm`), reserving conversation-viewport height like the other view regions, rather than as a top-of-screen notification.

The Enter handler SHALL resolve it: `y` cancels any in-flight turn and clears the queue (`cancelTurn`), ends the routine cleanly if one is active (`endRoutine`, closing the log file), and dispatches the navigation; `n` or Esc dismisses the prompt, keeping the routine and the queue. The `pendingNavConfirm` state SHALL be independent of the generic `pendingConfirmation` so the two flows do not compete. See the `routines` spec (slash commands and gating) for the full rationale.

#### Scenario: Navigation gated while a routine is active
- **GIVEN** a routine is active
- **WHEN** the user submits a chat-stranding command such as `/agents`, `/sessions`, or `/connections`
- **THEN** `gateNavigation()` sets a `pendingNavConfirm` and renders the prompt as a band directly above the input

#### Scenario: Navigation gated when queued messages would be discarded
- **GIVEN** no routine is active and the send queue is non-empty
- **WHEN** the user submits a chat-replacing command (`/agents`, `/agent`, `/agent <name>`, or `/connections`)
- **THEN** `gateNavigation()` sets a `pendingNavConfirm` whose prompt states how many queued messages would be discarded

#### Scenario: Overlay navigation does not warn about queued messages
- **GIVEN** no routine is active and the send queue is non-empty
- **WHEN** the user submits an overlay command that returns to the same chat model (`/crons`, `/sessions`, `/routines`, or `/export routine`)
- **THEN** no `pendingNavConfirm` is set and the navigation runs immediately, leaving the queue intact

#### Scenario: Confirming navigation ends the routine
- **GIVEN** a `pendingNavConfirm` is showing
- **WHEN** the user answers `y`
- **THEN** any in-flight turn is cancelled and the queue cleared, the routine ends cleanly if one was active (closing the log file), and the navigation is dispatched

#### Scenario: Declining navigation continues the routine
- **GIVEN** a `pendingNavConfirm` is showing
- **WHEN** the user answers `n` or presses Esc
- **THEN** the prompt is dismissed, and any active routine and queued messages are kept
