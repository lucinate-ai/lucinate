# Tasks — rooms truthful rows

## 1. Member identity on turn rows

- [x] 1.1 Pure resolver in `internal/rooms`: `Event` + roster → member handle (`member_id` first,
      `member_index` fallback, `""` when unplaceable). Unit tests: id hit, index fallback, unknown
      member, `message.member` unchanged.
- [x] 1.2 `internal/tui`: `eventHandle` / `formatRoomsEventStyled` use the resolver for
      `turn.started`, `turn.settled`, `turn.failed`, `turn.deferred` and `turn.cancelled`; the actor
      fallback stays for unplaceable members. Render test: gateway-actor `turn.settled` →
      `@alojz finished`; `turn.cancelled` → `@alojz — przerwano: …`.
- [x] 1.3 The CLI transcript (`lucinate rooms log`) renders the same handle for those rows.

## 2. Evidence only when a write was claimed

- [x] 2.1 `claimsWrite`: negation guard, then marker **and** target (deliverable extension or a
      first-person delivery verb). Table test over: read-only path mention (the live regression
      sentence), `zapisałem … report.md` without receipt, `nie zapisałem pliku report.md`,
      opinion with no marker, and the two existing receipt cases (verified / wrong bytes).
- [x] 2.2 Re-run the existing evidence suite unchanged — `HANDOFF` receipts, duplicates and byte
      mismatches keep their current verdicts.

## 3. Verification

- [x] 3.1 `gofmt -l .` empty, `go vet ./...` clean, `go test -count=1 ./...` green.
- [x] 3.2 Live pass in the same room shape as the pass that found these defects: one broadcast
      round, one `/stop`; the transcript names `@alojz`/`@default`, never `install:<hash>`; a reply
      that only quotes a path carries no `⚠` row.
      Re-read of room `alojz-default-082746` with the rebuilt binary (`v1.25.0-25-g9e534c8-dirty`)
      on 2026-11-04 09:0x: the TUI shows `@alojz finished`, `@default finished`, `@alojz passed`,
      `@default passed` and `@alojz — przerwano: member turn cancelled` where the same log
      previously rendered `install:6361e5ff…`; `lucinate rooms log` agrees (`alojz finished`,
      `@alojz — przerwano: …`); the `⚠ deklaracja zapisu bez potwierdzenia` row under `@Default`'s
      read-only reply that mentioned `docs/rooms-live-check.md` is gone. One housekeeping row
      still prints the gateway id (`room.stop_requested install:…`) — it is the gateway's own
      receipt, not a member row, and stays out of scope per the proposal.
- [x] 3.3 `docs/rooms.md` mentions the resolver and the evidence rule in one paragraph.
