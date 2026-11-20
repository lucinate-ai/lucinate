## Why

The cron job list screen sorts jobs by `nextRunAtMs` (ascending), which places the next-due job at the top. This is a temporal sort that serves a monitoring use case, but it makes finding a specific job by name harder — the user must scan through the whole list or read each row's name chip. Sorting by name alphabetically is the more natural default for a management list, matching the behaviour of the connections and agents managers.

## What Changes

- Sort the cron job list by `name` (ascending) instead of `nextRunAtMs` (ascending) in all three call sites: `crons.go` (list substate), `commands.go` (slash-command entry), and `chat.go` (transcript entry).
- The server-side sort parameter is a free-form `SortBy` string; the gateway accepts `"name"` as a valid value, so no protocol changes are needed.
- The `SortDir` stays `"asc"` (A–Z alphabetical order).

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `crons`: the list-view default sort changes from temporal to alphabetical.

## Impact

- Code: three `SortBy: "nextRunAtMs"` → `SortBy: "name"` changes across `internal/tui/crons.go`, `internal/tui/commands.go`, and `internal/tui/chat.go`.
- Tests: existing tests that assert on the sort order of the returned list will need their expected ordering updated to match alphabetical sort.
- Docs: `docs/crons.md` notes the default sort.
- No protocol, backend, or wire-contract changes.
- No breaking changes — the list is reloaded on every refresh anyway.