# Tasks — sessions source

## 1. Backend pass-through

- [x] 1.1 `internal/backend/hermes/backend.go`: `sessionListEntry` gains `Source string
      \`json:"source"\``; `SessionsList` copies it into the TUI row.
- [x] 1.2 Extend `TestSessions_CreateAndList` to assert `source: "tui"` survives the mapping.

## 2. The browser shows it

- [x] 2.1 `internal/tui/sessions.go`: `sessionListEntry` gains `Source` (`json:"source"`).
- [x] 2.2 The row's metadata line renders `<source> · <relative time>` when the source is non-empty
      and `<relative time>` alone when it is empty. Dim styling, no new colour per source.
- [x] 2.3 `FilterValue()` includes the source, so `/bot_room` and `/cron` filter the list.
- [x] 2.4 Tests: tag rendered, tag omitted when empty, filter matches on source and still on title.

## 3. Verification

- [x] 3.1 `gofmt -l .` empty, `go vet ./...` clean, `go test -count=1 ./...` green.
- [x] 3.2 Live check against the running gateway: open `/sessions` on the `hermes` connection, see
      the source tag on at least one real row (a room or CLI session), and type `/` + a source
      fragment to confirm the list narrows.
      Verified 2026-11-04 on `hermes dashboard` (31 profiles): the browser rendered real sources —
      `Explore available local skills · tui · 9h ago`, `Naprawić katastrofalny błąd … · cli ·
      2026-10-09`, `Prośba o dodanie własnych uwag · api_server · 2026-10-09` — instead of the bare
      age. Typing `/cli` in the filter put the `cli` rows at the top of the list. Note recorded in
      the delta: the browser's `/` filter is the fuzzy one, so it ranks a source match rather than
      excluding every other row; the spec scenario says exactly that, not "only these remain".
- [x] 3.3 `docs/sessions.md` gains one paragraph on the tag and the filter.
