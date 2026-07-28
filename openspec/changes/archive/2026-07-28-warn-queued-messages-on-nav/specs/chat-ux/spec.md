## MODIFIED Requirements

### Requirement: View region order

`chatModel.View()` (`internal/tui/chat.go`) SHALL assemble the chat view top→bottom in this fixed order. Every region between the header and the input SHALL reserve conversation-viewport height via `applyLayout`, and each SHALL render only when it has content:

1. **Header** — the status bar (connection, agent, model, token/cost).
2. **Info notifications** — informational one-shots such as `copied … to clipboard`, pinned just below the header.
3. **Conversation viewport** — the scrollable transcript.
4. **Completion menu** — slash-command / mention candidates, while active.
5. **Routine status row** — when a routine is active.
6. **Tool-activity strip** — what the agent is running this turn (collapses to a summary when idle).
7. **Queued-message footer** — messages typed while a turn streams, awaiting dispatch.
8. **Error notifications** — error one-shots.
9. **Navigation-confirm prompt** — the y/n band shown while a `pendingNavConfirm` is pending (a routine cancel and/or queued-message discard); the bottommost region above the input, where the answer is typed.
10. **Input box.**
11. **Help line.**

Informational and error notifications SHALL be deliberately split to opposite ends. An informational confirmation reads naturally at the top beside the status bar, while an error surfaces at the bottom next to the input, where the user will act on it. Both SHALL be drawn from the same `chatModel.notifications` store, filtered on the `isError` flag by `renderInfoNotifications` / `renderErrorNotifications`. The navigation-confirm prompt SHALL be rendered separately by `renderNavConfirm` off the `pendingNavConfirm` state — not the notification store — so it is present exactly while the question is pending and clears the instant it is answered.

#### Scenario: Fixed top-to-bottom region order
- **WHEN** `chatModel.View()` assembles the chat view
- **THEN** regions render in order: header, info notifications, conversation viewport, completion menu, routine status row, tool-activity strip, queued-message footer, error notifications, navigation-confirm prompt, input box, help line
- **AND** each region between header and input reserves viewport height via `applyLayout` and renders only when it has content

#### Scenario: Notifications split to opposite ends
- **GIVEN** the shared `chatModel.notifications` store filtered on `isError`
- **WHEN** notifications render
- **THEN** informational rows pin to the top just below the header via `renderInfoNotifications` and error rows drop toward the bottom above the input via `renderErrorNotifications`

#### Scenario: Navigation-confirm prompt sits directly above the input
- **GIVEN** a `pendingNavConfirm` is pending
- **WHEN** `chatModel.View()` assembles the chat view
- **THEN** `renderNavConfirm` draws the prompt as the bottommost region above the input box, below the error-notification region, and `applyLayout` reserves its height
