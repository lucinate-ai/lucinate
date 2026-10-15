## MODIFIED Requirements

### Requirement: List substate loads and renders jobs with local filtering

The list view SHALL load on `Init()` via `loadJobs()`, which calls `CronsList(Enabled: "all", SortBy: "name", SortDir: "asc")`. The full slice SHALL be cached on the model so the agent-filter toggle (`a` key) can re-apply locally without a round-trip — server-side filtering is not exposed in `CronListParams`. Each row SHALL render:

- **Line 1**: bold name + dim relative-time chip (`in 8h`, `due`, `—`).
- **Line 2**: chips for session target (`main`/`isolated`), wake mode (`now`/`heartbeat`), agent ID, and a status badge (`ok`/`error`/`disabled`/`idle`).

The list substate SHALL bind: `enter` opens detail; `r` refreshes; `n` opens the create form; `d` opens the create form pre-populated from the highlighted job (the duplicate flow — see the duplicate flow requirement); `esc` emits `goBackFromCronsMsg{}` to return to the view that opened the browser (chat, or the agent picker).

#### Scenario: Initial load and sort

- **WHEN** the list substate initialises via `Init()`
- **THEN** `loadJobs()` calls `CronsList(Enabled: "all", SortBy: "name", SortDir: "asc")` and caches the full slice on the model