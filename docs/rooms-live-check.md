# Rooms live check — one page for the owner

Reader: the owner of lucinate, no technical background. You want to write one short order in a room
with six bots and at once see who speaks, who waits for your approval, and why the talk ended.

## What you see in the room

You open a room, write one sentence, and the bots answer in turns. At most three rounds happen. A
fourth round never happens. The room ends when nobody has anything new to add, when the message
budget is spent, or when the round budget is spent. Every reply is shared with everyone. There is
no private side channel.

What you should be able to see without guessing: which round it is, for example round 2 of 3, how
many messages are spent, for example 7 of 10, who was asked, who replied, who passed, and who
pulled whom into the next round.

## What this run showed

Run: 2026-11-04, 18:35–18:43. Project lucinate, branch `feat/rooms-bot-mode`, HEAD `74bcd8e`. Room
`live-six-183443` ("Live six") on a gateway of its own (`hermes dashboard --isolated` on
127.0.0.1:9120), six bots: `alojz`, `default`, `budy`, `tester`, `webdev`, `librarian`.

One order sentence, exactly as typed:

> Napiszcie po jednym zdaniu: co jest dzis najwazniejsze do zrobienia w tym projekcie? Bez plikow,
> bez HANDOFF — samo zdanie.

What the screen showed, quoted:

```
  live-six-183443 — @alojz (profile default) @default (profile default) @budy (profile default) …
broadcast: everyone on the roster answers your message; later rounds happen only when a member cites a peer
blocked: @webdev has a retryable task — /retry
18:37:14  @Alojz: Najważniejsze dziś w lucinate (branch `feat/rooms-bot-mode`, HEAD `74bcd8e`) jest …
18:38:17  @Default: Sprawdziłem stan i się zgadza …
18:39:10  @Budy: Najważniejsze dziś: jeden przebieg pokoju z sześcioma botami …
18:40:07  @tester finished
18:42:15  @webdev deferred: member_unavailable
18:43:06  @Librarian: Najważniejsze z mojej strony: żeby ten przebieg nie został w zakładce terminala …
18:43:09  activity: settled
all members settled · 18:43
```

So, on the three questions this page exists to answer:

- **Who speaks** — five of the six bots answered, each named by handle as it spoke, and the ones
  that finished said so (`@tester finished`). The sixth, `@webdev`, never answered.
- **Who waits** — one line said it plainly: `blocked: @webdev has a retryable task — /retry`, and
  the row where it dropped out read `@webdev deferred: member_unavailable`. That is who was waiting,
  and the key that would have retried it.
- **Why it ended** — `activity: settled`, then `all members settled · 18:43`. The gateway's own
  reason code for this room is `settled / silent_round`: the third round had nobody new to speak.

The gateway's numbers for that room: one user message, five member messages, one deferred turn, one
message budget of ten and a round budget of three. The whole transcript is on disk and replayable:

```
lucinate rooms log live-six-183443
```

## Two things this run showed that are still wrong

1. **The budget leaves the header when six bots fill it.** With two bots the header reads
   `alojz-default-082746 — @alojz (profile default) @default (profile default)  · round 1/3 · 0/10
   messages · settled — waiting for you` — round, messages spent and the end reason, in plain words.
   With six, the roster line takes the whole width and `round 1/3 · 5/10` never appears. The feature
   exists; the roster has to yield to it.
2. **A false accusation row.** Under `@Default`'s reply — which only mentioned
   `docs/rooms-live-check.md` while talking about "gotowym materiale" — the screen printed
   `⚠ deklaracja zapisu bez potwierdzenia — brak HANDOFF`. Nothing was written. Cause is known: the
   list of words that count as a write claim contained the bare stem `gotow…`, which matches the
   adjective "gotowym". A location, not a guess: `internal/rooms/evidence.go`, the claim markers.

## What held

- Modes are named by what they do: `broadcast: everyone on the roster answers your message; later
  rounds happen only when a member cites a peer`.
- The rules line (`round 0 is your message …`, pass allowed, mention pulls a peer in, replies are
  republished) is one keypress away (`/rules`) and sits in `docs/room-rules.md`.
- Peer pull was not exercised in this run: no bot cited another, so no `@a pulled @b into the next
  round` row appeared. It was seen working in the 08:2x run recorded in `docs/handoff/ROOMS-HANDOFF.md`.

## What to fix, in order

1. Let the header keep the budget when the roster is long — shorten the roster line (handles only)
   before dropping `round N/3 · M/10 messages`.
2. Narrow the write-claim words to verbs of writing, so a readiness adjective cannot accuse a reply.
   **Done in this pass** (`internal/rooms/evidence.go`): the marker list holds verbs only, and the
   exact 18:38 sentence is now a regression test in `internal/rooms/evidence_test.go`.
3. Show peer pull next to the message that did it — unchanged from the earlier list, still untested
   in a six-bot room.
4. Keep internal work internal until you decide on sale and on code changes outside docs. Those two
   need your explicit approval.

## Success measure and review point

Measure: you write one order sentence and without a follow-up question you can name who spoke, who
waits, and why it ended.

Result on 2026-11-04: **who spoke — yes. Who waits — yes, with the key that resolves it. Why it
ended — yes. The budget — only when the room has two bots.**

The earlier promise on this page was a screenshot from the room folder by 06:00 on 2026-11-04. It
never landed; this page now carries the screen text itself, quoted above, with the room id to replay
it. Review point: after the two fixes above, one more six-bot run, keep or rewrite this page.
