## Context

The cron job list screen currently sorts by `nextRunAtMs` (ascending), placing the next-due job at the top. This is a temporal sort that supports a monitoring use case, but makes finding a specific job by name harder. The connections and agents managers already sort alphabetically by name as their default — this change brings the crons manager in line with that convention.

The gateway's `CronListParams.SortBy` field already accepts `"name"` as a value; the protocol and wire contract are unchanged. See proposal.md for motivation.

## Goals / Non-Goals

**Goals:**
- Change the default sort order of the cron job list from nextRunAtMs (temporal) to name (alphabetical, ascending).
- Apply the change uniformly across all three call sites: the list substate load, the slash-command handler, and the cron name resolver.

**Non-Goals:**
- No UI changes to the rendered row content.
- No new fields or parameters in the protocol layer.
- No user-configurable sort preference.

## Decisions

**Server-side sort over client-side reordering.** The gateway already supports `SortBy: "name"`, so the sort happens on the wire — the TUI never sees unsorted data. This is simpler and more efficient than fetching a temporal sort and re-sorting locally. The fake backend used in unit tests was updated to honour `SortBy: "name"` so the test faithfully exercises the real code path.

**Three call sites, all changed.** `loadJobs` (list substate), the slash-command handler (`commands.go`), and the cron name resolver (`chat.go`) all fetch the full job list. Changing all three ensures consistency: the list view, the cron command resolver, and any future consumer all see the same ordering.

## Risks / Trade-offs

- **Users who relied on temporal sort** for monitoring next-due jobs will need to scan the list differently. This is mitigated by the agent-filter toggle (`a` key), which still works and can narrow the list per-agent. If temporal sort is needed later, it could be exposed as a user-configurable preference.
- **No breaking changes.** The list is reloaded on every refresh (`r`) and on entry, so the new sort order applies immediately without migration.

## Migration Plan

No data or API migration. The change is a trivially revertible one-line parameter change at each of three sites. Rollback is a straight revert of the `internal/tui/crons.go`, `internal/tui/commands.go`, and `internal/tui/chat.go` changes.