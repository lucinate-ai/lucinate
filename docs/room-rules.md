# Room rules — what a hosted room actually enforces

The behavioural contract lives in [`openspec/specs/rooms/spec.md`](../openspec/specs/rooms/spec.md).
This file records the rules engine behind a room: who may speak, when, how many times, and how a
client should surface that instead of inventing its own.

Verified against Hermes v0.22.5 (2026.9.24). The engine is
`gateway/hosted_room_discussion.py` in the Hermes install — deterministic, restart-safe, and the
only authority on turn order. The client shapes text; it does not decide who runs.

## The room is a bounded discussion, not a free-for-all

A user message opens a **discussion** (one `thread_id`). From that point the gateway replays the
complete room log and produces **at most one next member task**. Nothing is scheduled ahead, so a
crash or a restart reconstructs the same next task from durable state rather than from memory.

Hard budgets (module constants):

| Limit | Value | Effect |
|---|---|---|
| roster | 2–6 local profiles | `groups.create` refuses anything else (code 5111) |
| rounds per discussion | 3 (`MAX_DISCUSSION_ROUNDS`) | a fourth round never happens; the discussion ends `bounded / max_rounds` |
| member messages per discussion | 10 (`MAX_DISCUSSION_MESSAGES`) | ends `bounded / max_messages` |
| transcript delta per turn | 24 lines (`MAX_DISCUSSION_DELTA_LINES`) | each member sees a bounded window, not the whole log |
| member reply | 64 KiB | longer replies are truncated with a notice to share the rest as a file |

## Who speaks, and when

- **Round 0** is the user's message: `@mention` selects the responders; **no mention means
  everyone**. `@all` / `@everyone` are reserved handles for the whole roster.
- **Rounds 1 and 2 are opt-in.** A member is pulled into the next round only when a *peer*
  explicitly cites them by handle — and only if that peer has not been heard from since. This is
  the answer to "can the profiles discuss among themselves": yes, but one cited round at a time,
  never free-running, and never past the third round.
- Within a round the speaking order **rotates** (`_rotate`), so the same member is not always
  first; `member_index` in the task identity records the position.
- A round in which nobody speaks ends the discussion as `settled / silent_round`; the client shows
  it as `settled — waiting for you`, because the ball is back with the owner rather than the room
  being closed.
- Every member keeps a **watermark** (last seq seen per thread). Pulling a peer in later still
  hands them the complete delta since their watermark, so they miss nothing and burn no turns
  while silent.

## The constitution every member receives

Each member turn carries the same prompt frame, and it is the clearest statement of the room's
rules. Quoted verbatim from `_build_prompt`:

```
[Discussion: "<room>"] You are @<handle>, one participant with @<peers> and the user.

New messages in this thread since your last turn (oldest first):
  <bounded delta>

Rules for this Discussion:
- Reply with one conversational message only when you have something new worth adding.
- If you have nothing new to add, reply with exactly "(pass)".
- Mention a teammate by handle to pull them into the next round; do not repeat points already made.
- Never reveal content from private conversations. Your reply is published verbatim.
```

Two consequences a client must respect: a member may legitimately say nothing (`(pass)` becomes
`turn.settled` with `passed: true`), and every member reply **is republished to the peers** as a
user-role message — the room is a shared transcript, not a set of private channels.

The constitution says nothing about the **owner** being the audience: it only tells members to
cite a peer, which is why a room can run three rounds of members addressing each other and end
with nobody having asked the owner a question (`internal/rooms/talk.go`). So the client appends
its own **talk contract** to the first message of every room — after the project orientation and
before the brief pointer — stating that the owner is a participant, that missing decisions are his
to give, that one concrete question to him ends the turn (no files, no `HANDOFF` in it), that a
peer mention opens a round so it needs his blessing, that a request for a conversation is not an
order for a document, and that waiting on him should be said out loud. The gateway's payload has
no system channel, so the first user message is the only place it can ride; `SendOpening` sends it
once, so later turns stay clean. This is text shaping, not enforcement: the client shapes what
members read, it cannot decide who runs — the contract is instruction, and the gateway remains the
authority on turns.

## Interventions are supervised, not scripted

The room report carries what the driver is doing (`groups.state.driver_status.member_activity`:
`[{member_id, status}]`) and what is blocking it (`pending_actions`). Resolving an approval or a
retry uses the exact turn coordinates (`discussion_event_id`, `member_id`, `member_index`,
`round_index`, `task_id`, `thread_id`, `turn_id`, `execution_generation`) — the same coordinates
the log carries, which is why a client can address one turn precisely. `groups.stop` cancels the
room's current work and is recorded as `room.stop_requested`.

## What the client can see, and what it should show

Every `message.member` and terminal event carries `round_index`, `member_index`, `turn_id` and
`thread_id`. So the client can derive, without guessing:

- which round the room is in (0-based, max 3),
- how many member messages the discussion has spent (cap 10),
- who was addressed and who replied or passed,
- who pulled whom into the next round (a member mention in a reply).

`lucinate`'s routing modes are a **text-shaping layer** on top of this engine, and the code says
so (`internal/rooms/routing.go`): *"the hosted-room protocol keeps mention parsing server-side, so
the only way a client can steer a message is to add the mention itself."* `broadcast` sends the
text untouched (no mention → everyone), `moderator` prefixes `@moderator`, `round-robin` prefixes
the next handle. None of them can stop the server from running the rounds a member's mentions
request.

## How the wider field does it

- AutoGen's group chat exposes explicit speaker selection — `auto` (LLM picks), `manual`,
  `round_robin` — and every agent sees every message (`SelectorGroupChat`, AutoGen docs). The
  Hermes engine is the `manual`+mention variant with a bounded round budget.
- Multi-agent debate implementations converge on 2–3 rounds and then a synthesis or human
  decision; measured results report quality *declining* past three rounds. The gateway's
  `MAX_DISCUSSION_ROUNDS = 3` matches that finding rather than contradicting it.
- A2A (Linux Foundation) models cross-agent work as tasks with explicit lifecycle. Hermes' own
  `groups.peer.*` / `replicate` / `promote` / `demote` surface is the same idea for rooms spanning
  two installations (RoomLink); `lucinate` reports `room_link.enabled` and does not use it.

## What this means for the product

The rules already exist; the product gap is **making them visible and steerable**, not inventing
new policy:

1. **`/rules`** — an overlay that quotes the constitution and the budgets above, so a user knows
   why a member passed, why a fourth round never comes, and why a silent room settled.
2. **Budget line in the room header** — `round 2/3 · 7/10 messages`, derived from the log; turns
   cost money, and this is the number that explains the bill.
3. **Modes that say what they do to the engine** — `broadcast` (everyone answers round 0),
   `moderator` (all un-mentioned messages to one member), `round-robin` (address the next member;
   later rounds still follow peer mentions and the server's rotation).
4. **Peer-pull visibility** — show "`@a` pulled `@b` into round 2" next to the message that did
   it, instead of leaving the second round unexplained.
5. **An explicit "peer discussion" switch** — since the engine allows it by default, a room
   preset may carry "allow peers to pull each other in", enforced by the first message rather than
   by pretending the client can forbid it. A stricter mode would need a gateway change; the
   client must not claim a rule it cannot enforce.

Out of scope for now: RoomLink (rooms spanning installations) — advertised by the gateway, unused
by the client, and a separate change.
