# Tasks — room rules surface

## 1. Derivation (internal/rooms)

- [ ] 1.1 `DiscussionBudget` from events: rounds used, member messages used, current round, end
      reason (`settled/silent_round`, `bounded/max_rounds`, `bounded/max_messages`, `idle`), with
      table tests over synthetic logs (empty, one round, three rounds, cap hit, pass-only).
- [ ] 1.2 `PeerPull` from a member reply that cites a peer (`@handle`), matched against the roster
      and only for members not heard from since; test with a mention, a self-mention, an unknown
      handle, and a mention that pulls nobody.
- [ ] 1.3 Header text helper (`round 2/3 · 7/10 messages`) that degrades at 80 columns
      (`2/3 · 7/10`) and never exceeds the width.

## 2. Rules overlay (internal/tui)

- [ ] 2.1 `/rules` renders the four member rules and the budget table, capped to the pane with
      `… N more`, composer stays visible, `esc` closes it (reuse the `/help` path).
- [ ] 2.2 `/help` in a room lists `/rules` in the command list.

## 3. Room view

- [ ] 3.1 Budget line in the room header, derived from the log; end reason shown when the
      discussion ended, cleared when a new user message opens the next discussion.
- [ ] 3.2 Peer-pull annotation next to the citing message (`@a pulled @b into round 2`).
- [ ] 3.3 Mode line explains effect: broadcast = everyone on round 0, moderator = one member,
      round-robin = next member; states that later rounds follow peer mentions and the gateway's
      rotation.

## 4. Presets

- [ ] 4.1 `peer discussion` flag on a room preset (`on` default, `off` optional), injected into
      the first message as a room instruction alongside the handoff contract; `lucinate rooms
      presets` shows the flag.
- [ ] 4.2 The instruction is honest about enforcement: "peers may pull each other in" / "answer
      only when addressed", never "the client will block peer mentions".

## 5. Verification

- [ ] 5.1 `go vet ./...`, `gofmt -l .`, `go test ./...`.
- [ ] 5.2 Live pass against `hermes dashboard` v0.22.5: two profiles, broadcast round, one peer
      mention, one stop; header shows rounds/messages and the gateway's end reason.
- [ ] 5.3 `docs/rooms.md` and `README.md` point at `docs/room-rules.md`.
