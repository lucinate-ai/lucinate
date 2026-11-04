# Rooms — say who spoke, and only accuse when a write was claimed

## Why

A live pass against `hermes dashboard` (2026-11-04, room `alojz-default-082746`,
`docs/handoff/ROOMS-HANDOFF.md`) put two rooms defects on screen that no unit test had caught:

1. **The room addressed the gateway, not the member.** Every gateway-owned row renders through
   `Event.Speaker()`, and for a `turn.*` event the actor is the gateway itself:
   `actor: {kind: gateway, id: "install:6361e5ff…"}`. The transcript therefore read
   `install:6361e5ff3c924dcd8c742d702c4921da finished` after every reply and
   `@install:6361e5ff… — przerwano: member turn cancelled` after `/stop`. The member who actually
   spoke or was cancelled is in the event payload (`member_id: "m1"`, `member_index: 0`) and on
   the roster, so the client knew and dropped it.
2. **The evidence column accused replies that wrote nothing.** `claimsWrite` returns true when the
   reply contains a file extension, because `claimFilePattern` is OR-ed with the marker list. A
   member answering "the branch is at `docs/rooms-live-check.md`" — a read-only statement — was
   marked `⚠ deklaracja zapisu bez potwierdzenia — brak HANDOFF`. The function's own docstring
   says the claim shape is a marker *and* a deliverable path in the same breath, so the code and
   its stated intent disagree; the visible effect is a warning the owner learns to ignore.

Both defects are in the room surface whose whole promise is that the owner can trust the
transcript: who spoke, who stopped, and whether a file was really delivered.

## What changes

- **Turn rows name the member.** `turn.started`, `turn.settled`, `turn.failed`, `turn.deferred`
  and `turn.cancelled` resolve their handle from the event (`member_id`, then `member_index`) against
  the room's roster, falling back to the actor only when the roster has no such member. A cancelled
  turn reads `@alojz — przerwano: member turn cancelled`.
- **A claim needs a claim.** `claimsWrite` fires on a write marker (`zapisa…`, `wdroż…`, `utworz…`,
  `gotow…`) **and** a deliverable path in the same reply; a bare path mention is discussion, not a
  claim. A negated marker (`nie zapisałem`, `bez zapisu`, `nic nie zapisałem`) is not a claim
  either. The evidence row stays `none` (no row) for a read-only reply.

## Out of scope

- Merging the per-turn `install:… finished` housekeeping rows away, or reordering the transcript.
  This change fixes who a row names, not which rows exist.
- Any change to the gateway, to `docs/room-rules.md`, or to the discussion contract.
