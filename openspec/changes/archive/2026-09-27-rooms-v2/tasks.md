# Tasks — rooms v2

## 1. Routing

- [x] `internal/rooms/routing.go`: mention parsing (gateway grammar, email-safe), `FindMember`,
      `ResolveMentions`, `Route` for broadcast/moderator/round-robin, `ParseRoutingMode`
- [x] `internal/rooms/routing_test.go`: grammar, unknown handles, mode precedence, cursor
- [x] `internal/rooms/roomprefs.go`: per-room preferences and `rooms-prefs.json`
- [x] TUI: `/mode`, `/moderator`, `/broadcast`, `/round-robin`; routed sends; header routing note

## 2. Streaming

- [x] `internal/rooms/stream.go`: fold the log into one stream per member, deltas cumulative or
      appending, empty replies marked
- [x] `internal/rooms/stream_test.go`
- [x] TUI: 120 ms braille spinner, live rows, placeholder removal on an empty answer

## 3. Reconnect / resume

- [x] `internal/rooms/backoff.go`: schedule (500 ms, ×2, capped at 30 s) and `IsDisconnect`
- [x] `internal/rooms/backoff_test.go`
- [x] `rooms.Client.Err()` so a dropped socket is distinguishable from a deliberate close
- [x] TUI: redial with backoff, resume `groups.log` from the held cursor, status reporting

## 4. Export

- [x] `internal/rooms/export.go`: markdown + JSON renderers, `exports/` paths, `WriteExport`
- [x] `internal/rooms/export_test.go`
- [x] TUI: `/export [md|json|both]`

## 5. Compaction

- [x] `internal/rooms/compact.go`: split on the keep window, request prompt, local brief,
      `ApplyCompaction`
- [x] `internal/rooms/compact_test.go`
- [x] TUI: `/compact [N]`, `/compact local [N]`, brief rendering, per-room persistence

## 6. Member UX

- [x] `internal/rooms/colors.go`: palette, deterministic `ColorFor`, `NormalizeHex`
- [x] `internal/rooms/usage.go`: usage extraction and per-member aggregation
- [x] `internal/rooms/search.go`: `Find`, snippets, `MatchLine`
- [x] TUI: `/header`, `/cost`, `/find` with highlighting

## Verification

- [x] `go test ./...` green; `internal/rooms` coverage ≥ 70 %
- [x] Live pass over a real WebSocket gateway (in-process) covering routing, streaming, an
      empty reply, a dropped socket, the refused redial, resume, export, compaction, cost and find
- [x] `openspec/specs/rooms/spec.md` written
