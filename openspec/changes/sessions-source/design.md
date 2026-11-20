# Design — sessions source

## Where the data already is

- The gateway's `session.list` rows carry `source`; the backend's own fixture proves it:
  `internal/backend/hermes/backend_test.go:311` →
  `{"id":"20260719_000000_stored",…,"source":"tui"}`.
- The backend declares the row as `sessionListEntry` (`internal/backend/hermes/backend.go:519`)
  without a `Source` field, so `encoding/json` drops the value and it never reaches the TUI.
- The TUI's own row shape is `internal/tui/sessions.go:203` (key, derivedTitle, lastMessagePreview,
  updatedAt, model) and its `FilterValue()` (`internal/tui/sessions.go:33`) is what the existing
  bubbles filter matches.

## Decisions

1. **Pass the value through, do not derive it.** A source the gateway did not send stays empty; the
   row renders no tag. Deriving one from the session key would be a guess (room sessions and CLI
   sessions look alike on the wire), and a wrong label is worse than none.
2. **A tag, not a column.** The browser is a list of two-line rows; the tag rides the metadata line
   before the relative time (`bot_room · 2h ago`). It is dim, so a list where every row is `tui`
   stays quiet, and it costs no extra line.
3. **The filter, not a new key.** `FilterValue()` becomes
   `title + preview + model + source`, so `/bot_room`, `/cron` or `/cli` narrows the list with the
   `/` filter the view already advertises. Adding a modifier-key chord here would collide with the
   list widget's own bindings and with the app-level keys.
4. **No grouping.** Sorting and grouping stay as they are; this change is about naming, and a
   grouped list is a different (and unproven) design.

## Tests that must exist afterwards

- `internal/backend/hermes`: `SessionsList` passes `source` through (extend the existing
  `TestSessions_CreateAndList` to assert the field, using the fixture that already carries it).
- `internal/tui`: a row with source `bot_room` renders `bot_room · 2h ago`; a row with an empty
  source renders no `·` tag at all; `FilterValue()` contains the source so a query of `bot_room`
  matches, and matching still works on the title alone.
