# Tasks — room rules surface

## 1. Derivation (internal/rooms)

- [x] 1.1 `DiscussionBudget` from events: rounds used, member messages used, current round, end
      reason (`settled/silent_round`, `bounded/max_rounds`, `bounded/max_messages`, `idle`), with
      table tests over synthetic logs (empty, one round, three rounds, cap hit, pass-only).
- [x] 1.2 `PeerPull` from a member reply that cites a peer (`@handle`), matched against the roster
      and only for members not heard from since; test with a mention, a self-mention, an unknown
      handle, and a mention that pulls nobody.
- [x] 1.3 Header text helper (`round 2/3 · 7/10 messages`) that degrades at 80 columns
      (`2/3 · 7/10`) and never exceeds the width.

## 2. Rules overlay (internal/tui)

- [x] 2.1 `/rules` renders the four member rules and the budget table, capped to the pane with
      `… N more`, composer stays visible, `esc` closes it (reuse the `/help` path).
- [x] 2.2 `/help` in a room lists `/rules` in the command list.

## 3. Room view

- [x] 3.1 Budget line in the room header, derived from the log; end reason shown when the
      discussion ended, cleared when a new user message opens the next discussion.
- [x] 3.2 Peer-pull annotation next to the citing message (`@a pulled @b into round 2`).
- [x] 3.3 Mode line explains effect: broadcast = everyone on round 0, moderator = one member,
      round-robin = next member; states that later rounds follow peer mentions and the gateway's
      rotation.

## 4. Talk contract (replaces the per-preset peer-discussion flag)

- [x] 4.1 `DiscussionBlock()` (`internal/rooms/talk.go`) carries the five-point contract: the owner
      is the source of decisions, one concrete question to him ends the turn (no files, no
      `HANDOFF`), a peer mention opens a round, a conversation request is not an order for a
      document, and waiting on him is said out loud. Under 1200 bytes.
- [x] 4.2 The instruction is honest about enforcement: it is text the client injects into the
      first message, never a claim that the client blocks peer mentions.
- [x] 4.3 `SendOpening` appends the contract exactly once, after the orientation and before the
      brief pointer, including when the orientation is empty; later messages are untouched.
- [x] 4.4 `DiscussionBudget.EndText()` reports `settled — waiting for you`; the `/rules` overlay
      and `docs/room-rules.md` use the same label.
- [x] 4.5 Tests: five points and size (`talk_test.go`), first message carries it exactly once and
      after the orientation, second message does not, empty orientation still carries it
      (`rooms_test.go`), and the settled label (`discussion_test.go`, `rooms_rules_test.go`).

## 5. Verification

- [x] 5.1 `go vet ./...`, `gofmt -l .`, `go test ./...`.
- [ ] 5.2 Live pass against `hermes dashboard` v0.22.5: two profiles, broadcast round, one peer
      mention, one stop; header shows rounds/messages and the gateway's end reason.
- [x] 5.3 `docs/rooms.md` and `README.md` point at `docs/room-rules.md`.
