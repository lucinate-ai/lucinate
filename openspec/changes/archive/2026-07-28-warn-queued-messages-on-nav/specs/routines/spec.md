## MODIFIED Requirements

### Requirement: Slash commands and navigation gating

The system SHALL register two entries in `slashCommands`:

| Command | Behaviour |
|---|---|
| `/routine <name>` | Activate the named routine. Bare `/routine` is an error pointing at `/routines`. Tab completion uses `m.routineNames` populated by `loadRoutineNames()` at chat init and after any manager-view CRUD. |
| `/routines` | Open the manager via `showRoutinesMsg{}` |

Only one routine SHALL run at a time per session. Both commands SHALL route through `gateNavigation()` (`routines_chat.go`) when a routine is already active, showing:

```
Starting routine "other" will cancel the active routine "demo". Continue? (y/n)
```

The same gate SHALL cover the navigations that strand or replace the chat model — `/agents`, `/agent <name>`, `/sessions`, `/crons`, `/crons all`, `/connections` — for the same reason: the active routine controller can't survive a chat-view reset, and silently dropping it would leak the open log file. On `y`, the gate SHALL cancel any in-flight turn (`cancelTurn`) and end the routine (`endRoutine`) before dispatching the navigation. On `n` or Esc the prompt SHALL clear and the routine SHALL continue. `startRoutine` itself still has a defensive `if m.activeRoutine != nil` guard, but in normal flow the gate runs first.

The prompt SHALL render as a band pinned directly above the input, not as a top-of-screen notification. The same gate SHALL also guard **queued messages** and can therefore fire with no routine active — the chat-replacing navigations (`/agents`, `/agent`, `/agent <name>`, `/connections`) would otherwise drop the send queue silently. See the `commands` spec (navigation gate) for that dimension.

#### Scenario: Bare /routine is an error

- **WHEN** the user submits `/routine` with no name
- **THEN** it is an error pointing at `/routines`

#### Scenario: Starting a second routine prompts to cancel the first

- **GIVEN** routine `demo` is active
- **WHEN** the user runs `/routine other` (or a stranding navigation such as `/agents`, `/agent <name>`, `/sessions`, `/crons`, `/crons all`, `/connections`)
- **THEN** `gateNavigation()` shows the "Continue? (y/n)" prompt as a band directly above the input
- **AND** on `y` it cancels the in-flight turn via `cancelTurn` and ends the routine via `endRoutine` before dispatching the navigation
- **AND** on `n` or Esc the prompt clears and the routine continues

#### Scenario: Tab completion of routine names

- **WHEN** the user types `/routine ` and presses Tab
- **THEN** completion draws from `m.routineNames`, populated by `loadRoutineNames()` at chat init and after any manager-view CRUD
