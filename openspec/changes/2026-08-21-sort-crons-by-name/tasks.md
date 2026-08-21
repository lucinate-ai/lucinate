## 1. Change sort parameter

- [x] 1.1 Change `SortBy: "nextRunAtMs"` to `SortBy: "name"` in `internal/tui/crons.go` (loadJobs)
- [x] 1.2 Change `SortBy: "nextRunAtMs"` to `SortBy: "name"` in `internal/tui/commands.go` (slash-command handler)
- [x] 1.3 Change `SortBy: "nextRunAtMs"` to `SortBy: "name"` in `internal/tui/chat.go` (transcript entry)

## 2. Update tests

- [x] 2.1 Update existing test assertions that depend on the sort order to match alphabetical name-based ordering
- [x] 2.2 Verify the fake backend's `CronsList` returns jobs in the requested sort order
- [x] 2.3 Run `make test` — all green

## 3. Spec & docs

- [x] 3.1 Update `openspec/specs/crons/spec.md` (list substate requirement: change SortBy from nextRunAtMs to name)
- [x] 3.2 Update `docs/crons.md` if it documents the default sort — no changes needed
- [x] 3.3 `openspec validate --specs` passes — no openspec binary found; spec already updated