# Room rules — make the discussion policy visible and steerable

## Why

A hosted room already runs under a strict, deterministic policy in the gateway
(`gateway/hosted_room_discussion.py`): round 0 answers the user's mentions (or everyone), later
rounds exist only when a peer is cited by handle, a discussion is capped at 3 rounds and 10
member messages, a member may pass, and each member sees a bounded delta of the shared transcript.

None of that is visible in `lucinate`. A user sees members appear and disappear, a room end
without explanation, and a second round with no cause they can point at. Worse, the client's own
"routing modes" look like policy while they are only text shaping — they cannot stop the engine
from running rounds a member's mentions request. The result is a room whose rules the user cannot
read, audit, or reason about — in a product whose whole point is several profiles talking at once.

## What changes

- **`/rules`** — an overlay in a room quoting the constitution every member receives (reply only
  when you have something new, `(pass)` when you do not, mention a peer to pull them into the next
  round, never reveal private content) and the hard budgets (2–6 members, 3 rounds, 10 messages).
  Capped to the pane like the in-room `/help`, `esc` closes it.
- **Budget line** — the room header reports the live position derived from the log's own turn
  coordinates: `round 2/3 · 7/10 messages`, and names the end reason when a discussion ends
  (`settled — silent round`, `bounded — max rounds`, `bounded — max messages`).
- **Peer-pull annotation** — when a member's reply cites a peer, the transcript marks it
  (`@a pulled @b into round 2`), so a second round is never a mystery.
- **Mode labels that tell the truth** — `broadcast` = no mention (everyone answers round 0),
  `moderator` = every un-mentioned message goes to one member, `round-robin` = address the next
  member; the mode line states that later rounds follow peer mentions and the gateway's rotation,
  not the client's cursor.
- **`peer discussion` flag on a room preset** — the engine allows peer pulls by default; a preset
  may state the room's intent in its first message ("peers may pull each other in" / "answer only
  when addressed"), enforced as a room instruction rather than pretended as a client-side block.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `rooms`: rules overlay, budget indicator, peer-pull annotation, honest mode wording, preset flag.

## Impact

- `internal/tui/rooms.go`, `internal/tui/rooms_ux.go` — `/rules` overlay, budget header, annotation.
- `internal/rooms/log.go` (or equivalent) — read `round_index`/`member_index` off events.
- `internal/rooms/roomprefs.go` — preset `peer discussion` flag.
- Tests in `internal/rooms` (derivation) and `internal/tui` (rendering).
- `docs/room-rules.md` — already written; the spec delta lands here.
