# Sessions — show where a session came from, and let the filter use it

## Why

The Hermes gateway already tells the client which surface a session belongs to: its `session.list`
rows carry a `source` field (`internal/backend/hermes/backend_test.go:311` has `"source":"tui"` in
the fixture, and the gateway's own `hermes_cli/session_listing.py` filters lists by source). The
client drops it — `sessionListEntry` (`internal/backend/hermes/backend.go:519`) has no `Source`
field — so the session browser shows rooms, cron runs, subagent sessions and terminal sessions as
one undifferentiated list of titles and previews.

That matters now that rooms are the product surface on this branch: a room's session is a Hermes
session with its own source, and the owner cannot tell a room transcript from a CLI conversation in
the browser, nor narrow the list to one kind of work.

## What changes

- `internal/backend/hermes/backend.go`: `sessionListEntry` gains `Source`; `SessionsList` passes it
  through into the session-browser row (`internal/tui/sessions.go` `sessionListEntry`, JSON key
  `source`). An absent or empty source stays empty — no invented label.
- `internal/tui/sessions.go`: each row shows the source as a short dim tag before the relative time
  (`bot_room · 2h ago`), omitted when the source is empty. The row's filter value includes the
  source, so `/bot_room` (or `/cron`) narrows the list with the filter that already exists — no new
  key binding.
- `docs/sessions.md`: one paragraph on the tag and the filter.

## Out of scope

- Filtering server-side (`source=` in the RPC), a per-source grouping, and any colour scheme beyond
  the existing dim tag. The browser keeps one list.
- The TUI's own `source` values it sends when creating a session (`override`, `messaging`,
  `fallback`) — unchanged.
