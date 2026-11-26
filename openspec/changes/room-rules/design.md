# Design — room rules surface

## Authority

The gateway decides turn order; the client never schedules a member. `plan_next_task` replays the
complete log and returns at most one task, so every client-side number here is **derived from
events that already exist** — no new RPC, no state the client owns.

Coordinates already on the wire (`message.member`, `turn.*`): `round_index` (0..2),
`member_index`, `turn_id`, `thread_id`, `task_id`, `seen_through_seq`. Budgets are constants in
the gateway: `MAX_DISCUSSION_ROUNDS = 3`, `MAX_DISCUSSION_MESSAGES = 10`,
`MAX_DISCUSSION_DELTA_LINES = 24`, roster 2–6.

## Decisions

**D1 — Derive, never mirror.** The header counts member messages and rounds from the log it
already holds (`m.page.Events`). No extra polling, no optimistic counters that can drift from the
gateway's own view after a reconnect.

**D2 — The end reason is part of the display.** `settled / silent_round`, `bounded / max_rounds`,
`bounded / max_messages` are the gateway's words; the client shows them translated, not a generic
"room stopped". A room that ends because nobody had anything new to say is a different outcome from
one that hit the cap, and the user pays for both differently.

**D3 — `/rules` quotes the constitution verbatim.** The member prompt is the room's actual law;
paraphrasing it in the UI would let the two drift. The overlay renders the four rules and the
budget table from `docs/room-rules.md`, capped to the pane (same `fitNotice` path as `/help`).

**D4 — Peer pulls are annotated at the message that caused them.** The engine's second round is
triggered by a mention inside a member's reply. The transcript marks the citing message rather
than the resulting turn, because that is where the user can act (stop the room, or answer).

**D5 — Modes describe effects, not intentions.** `broadcast`, `moderator`, `round-robin` are text
shaping (`internal/rooms/routing.go`); the mode line says so. Renaming `round-robin` is rejected:
it is the vocabulary used by AutoGen and by every operator who has used one.

**D6 — "Peer discussion" is an instruction, not a client-side block.** The engine permits peer
pulls and the member prompt already invites them. A preset flag therefore injects a room
instruction into the first message (the same channel as the handoff contract). A real prohibition
needs a gateway change and is out of scope; claiming a rule the client cannot enforce would be the
same dishonesty the last UI pass removed.

## Rejected alternatives

- **Client-side suppression of peer mentions** (strip `@peer` from member replies): falsifies the
  transcript and the members' own record.
- **A client-side round counter that stops sending**: the engine already bounds rounds; a second
  limiter would surface as a room that "hangs" with work still pending on the gateway.
- **New `groups.rules` RPC**: upstream owns it; until it exists the log is the source of truth.

## Risks

- Log-derived budgets assume the events are complete for the discussion; a client that reconnects
  mid-discussion loads from the cursor, so the counter is recomputed from a full page on open
  (existing behaviour) and stays correct.
- `round_index` is bounded by the gateway (0..2) and validated on read; an unknown value is shown
  as `round ?` rather than crashing the header.

## Verification

- Unit tests in `internal/rooms` for the derivation (rounds, message count, end reason, peer pull).
- TUI tests for the overlay, the header line and the annotation, at 80x24 (no composer push-off).
- Live pass against the real gateway: a two-profile room, broadcast first round, one peer mention
  in a reply, then a stop — the header must show 2/3 rounds, the messages spent, and the end
  reason the gateway reported.
