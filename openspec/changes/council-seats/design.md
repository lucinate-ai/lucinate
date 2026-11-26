# Design — council seats

## What the upstream project actually is

`roast-room` (MIT, ~19k lines of markdown, ~1.5k lines of code) is four separable layers. Only two
of them are worth porting, and knowing which is which is most of the work.

| Layer | Where | Size | Portable? |
|---|---|---|---|
| **Seat content** — one "brain" per advisor | `.claude/skills/roast-room/brains/<slug>/` | ~720 lines, ~100 KB per seat, 12 seats | **Yes**, as data |
| **Seat behaviour** — one agent file per seat | `.claude/agents/council-<key>.md` | ~60 lines | **Yes**, as a system prompt |
| **Protocol** — brief, board, rounds, scoring, verdict | `SKILL.md`, `references/{round-prompts,scoring,seats,report-template,live-room}.md` | ~600 lines of contract | **Yes**, and it should be code, not a prompt |
| **Room** — a local browser stage | `app/` (Node server + `app.js` + portraits + jsPDF) | ~700 lines + assets | **No.** Presentation for a browser. |

The decisive property is that **the room never calls a model**. In its default (`viewer`) mode the
protocol runs in an agent session that writes files; a zero-dependency Node server scans the session
folder once a second, parses `round-N.md` into cards with the same parser the API runner uses, and
emits `phase / setup / round_start / seat_start / seat_done / round_done / cruxes / draft / verdict`
over SSE. The folder is the transport. That is why the same protocol runs under Claude Code, under
Codex, under an API runner, and — this is the point — under a client that cannot spawn sub-agents at
all. `SKILL.md` says so in its own section: *"Run the same protocol sequentially yourself."*

lucinate is that client. It gains nothing from the browser stage and everything from the contract.

## The protocol, compressed

1. **Brief**: the decision in one line, 2–4 options always including "don't do it" / "keep doing what
   you do now", the business, resources, success and horizon, and the founder's lean (the council
   tests the lean, it does not echo it). Under 300 words; every invented number marked `(assumed)`.
2. **Board**: a seat is `{key, name, short, initials, slug, pillar, question, dimension, color}`.
   Every seat at the table gets a relevance weight 1–3 (3 = the decision lives in this pillar; exactly
   one or two seats at 3). The board always keeps one seat that guards the downside and one most
   likely to disagree. Board policy is `ask` / `auto` / `default`; named seats and `--all` override it.
3. **Three rounds**, each with a fixed card grammar: opening positions, then debate (each seat must
   name the seat it disagrees with, concede something, answer each crux, and report whether it moved),
   then a ratification vote on the Chair's draft resolution.
4. **Scoring** (all of it from the cards, computed):

   ```
   option score = Σ(weight × seat score) / Σ(weight)          one decimal
   band: ≥7.5 GO · 6.0–7.4 GO WITH CONDITIONS · 4.5–5.9 RESHAPE · <4.5 DON'T DO IT
   WAIT replaces a band only with a named trigger and date
   overrides: any seat at RUIN RISK likely caps at RESHAPE until its mitigation is endorsed
              a one-way door with spread ≥4 caps at GO WITH CONDITIONS
   spread     = max − min seat score on the recommended option
   confidence = round(mean seat confidence × 2) − 1 when spread ≥5, clamped 1..10
   support    = weighted mean of the Round 3 FINAL SCORE
   votes      = endorse / amend / dissent counts
   ```
5. **Verdict**: label, the call, three to five arguments each tagged with seat + framework, the fights
   (per crux: who was on each side, who moved), a minority report in the dissenting seat's own words
   and what would have to be true for it to be right, checkable conditions, dated next steps, the
   cheapest test, kill criteria, and a revisit date.
6. **Verification**: every quote attributed to a source id is checked verbatim against that seat's
   brain; anything not found is removed from the verdict and reported. A seat may never be presented
   as the real person.

## What lucinate already has, and what it does not

| Roast Room | lucinate today | Verdict |
|---|---|---|
| seat = sub-agent reading a brain | member = Hermes profile with its own `SOUL.md` and skills | **reuse** — a seat is a profile plus a brain file |
| Chair = the agent session running the skill | nothing | **build** — and build it as Go, not as a prompt |
| 3 rounds | gateway rounds: 3, `MAX_DISCUSSION_ROUNDS` | **reuse the number, not the semantics** (below) |
| option scoring, overrides, verdict | nothing | **build** (`internal/council/score.go`) |
| session folder + live viewer | `.rooms/<id>/` shared dir + live TUI transcript | **reuse the container**, add the folder contract |
| past sessions replay + PDF | transcript export (`md`/`json`), `rooms log` | **build replay**, skip PDF |
| quotes checked against the brain | `HANDOFF` receipts with read-back + evidence column | **build** — different axis (prose vs files) |
| portraits, table, mouth frames | — | **drop** |

## Constraint ledger — where the hosted room does not fit, stated plainly

These came out of the code, not out of taste, and each one changes the design:

1. **The roster cap is 6** (`internal/rooms/rooms.go`: `MinMembers=2, MaxMembers=6`, mirroring the
   gateway's `hosted_room_discussion.MIN/MAX_DISCUSSION_MEMBERS`; `groups.create` refuses 5111
   otherwise). The upstream default board is **seven** and `--all` is nine. A full roast-room council
   therefore does not fit in one hosted room. Do not "fix" the client to 128 — 128 is the persistence
   cap, not the rule.
2. **Turns are sequential and scheduled one at a time.** The gateway reconstructs *at most one next
   member task* from the durable log. Upstream round 1 spawns every seat in one message and collects
   the cards together; a room serialises them. Nine seats would be nine turns.
3. **Round 2 is opt-in by mention.** A member is only pulled into a later round when a peer cites its
   handle, and only if it has not spoken since. Upstream round 2 is "every seat answers every other
   seat". Same round count, different mechanics.
4. **No token deltas in the log** (`hosted_room_member_activity.py` deliberately keeps them out at the
   room-log byte budget). The only live signal is `driver_status.member_activity`.
5. **There is no orchestrator seat.** Nothing in the gateway's model guarantees a turn for a member
   that must compute scores, write `cruxes.json` and draft a resolution between rounds. Putting the
   Chair in the roster makes it a member subject to rotation and peer pulls — the one role the protocol
   says must never argue.

Consequence: **the Chair must be the client**, not a room member. That is also the single biggest
improvement over the original, where "never argue a side, never add your own pillar" is a prompt-level
instruction the model can drift from. In Go it is structural: the Chair computes, the seats talk.

Therefore a council has two possible transports, and the design makes that explicit rather than
pretending one fits everywhere:

- **`seats` transport (preferred)** — the runner sends each seat its prompt directly through the
  configured `backend.Backend` (one session per seat, parallel), collects cards, and only then, if the
  session is being watched in a room, posts the condensed round to the room as a message. No roster
  cap, real parallelism, one model per seat.
- **`room` transport (small councils only)** — a hosted room of at most 5 seats + 1 moderator profile,
  sequential turns, three gateway rounds, cards read back out of the durable log. Honest for
  `--quick` (4 seats) and for a 5-seat board; refused with the limit named when the board is wider.

## The session folder contract

Byte-compatible with upstream, so a Roast Room session can be replayed here and a lucinate session can
be opened in the upstream room:

```
<council dir>/
  brief.md            # the brief + options + the weights table
  council.json        # title, decision, brief, mode, options[], weights{}, seats[], assumptions[]
  round-1.md          # one "## <Short> (weight n)" heading per card, cards verbatim, "## Chair notes" last
  cruxes.json         # 1-3 cruxes with seats on each side, unanimous flag, one-line note
  round-2.md
  draft-resolution.md
  draft.json          # option, label, recommendation, conditions, next_steps, cheapest_test, kill_criteria
  round-3.md
  verdict.md          # the report template
  verdict.json        # written last; its presence is what says "the council is over"
```

Rules the code enforces: files appear in that order (a viewer tailing the folder never sees `round-2`
before `round-1`), a JSON file that does not parse is retried rather than treated as absent (a
half-written file is a writing state, not a missing one), and `verdict.json` carries no scores — the
scores are recomputed from the cards so a stale number cannot outlive its card. The folder lives under
the room's shared directory (`.rooms/<id>/council/`) when a room is attached, and under
`<lucinate data dir>/councils/<date>-<slug>/` otherwise.

## Scoring is code, with the upstream sessions as the golden test

The published example verdict claims `A 2.0 · B 7.3 · C 6.0`, support 8.0, confidence 7, spread 1,
0 endorse / 7 amend / 0 dissent. Recomputing those numbers from `round-3.md` with the weights in
`council.json` reproduces them exactly (verified against the checked-in session while writing this
proposal: `Σ(w·s)/Σw` over seven cards). That makes the port falsifiable before any UI exists:

- `internal/council/score_test.go` parses the three shipped example sessions as golden fixtures and
  asserts the recomputed verdict equals the published one, field by field.
- Card parsing is tested on the same fixtures: the option-scores line is parsed to end-of-line (it
  uses `·` between options, which is also the field separator elsewhere — the upstream parser special
  cases exactly this and so must ours), `PILLAR SCORE`/`FINAL PILLAR SCORE` and `FINAL SCORE` are
  distinct fields, and `VOTE` maps to endorse/amend/dissent.
- Overrides get their own table: ruin cap, one-way-door cap, unanimity check, and the confidence
  penalty at spread ≥5.

## Quote verification, the axis rooms lacks

`rooms.VerifyReply` proves a **file** was written (receipt, read-back, duplicate). A council needs the
opposite proof: that a **sentence** attributed to a real person is actually that person's words.
Port `check_quotes` (both the Python script's semantics and the app's regex): normalise lower-case,
collapse punctuation and whitespace, fold apostrophes, then require the quote (≥5–6 words) to appear
inside the seat's concatenated brain text; accept a `source id` only when the quote is found. A quote
that fails is **removed from the verdict and reported**, never softened to "probably". The same check
runs at card time (`lucinate council check`) and at verdict time.

## What we deliberately do not port

- **The browser stage**: portraits with four mouth frames, rebuttal arrows, plates, the waiting card,
  playback speed, the PDF. A terminal has a transcript and a keyboard; the numbers are the theatre.
- **The Node server**: its whole job is turning files into SSE for a browser. `lucinate` is already a
  client of a durable log; a file tailer would be a second, weaker source of truth.
- **`kie.ai` portraits and `opencv` frame generation**: an image pipeline with an external paid API,
  for a TUI with no pixels to put it in.
- **`/newroaster`'s fetch pipeline as-is**: the *idea* (build a brain from public work, verify every
  quote) is worth having, and our own corpus and skills already do the fetching. It belongs in a
  seat-building skill, not in this change.

## Content decision — whose brains

Two honest options, and the choice is the owner's, not the code's:

- **Public figures** (what ships): prebuilt brains, immediately useful, and every line carries the
  upstream guardrail — simulated, never presented as the person, verbatim quotes only with a source.
  Cost: ~100 KB of curation per seat and a licence/attribution question for any brain we redistribute.
- **Our own specialists**: seats are profiles we already run (`alojz`, `budy`, `librarian`, …), the
  brain is the profile's `SOUL.md` plus its book shelf, and the "simulation of a real person" problem
  disappears entirely. Cheaper, ours, and the interesting one for this fleet: a council of our own
  advisors, grounded in `BAZA-WIEDZY`.

The engine is identical either way; only the `brains/<slug>/` content differs. Start with our own
profiles (3–5 seats) so the mechanics can be proven without a curation project, and add external seats
later if they earn their place.

## Effort, in slices that each stand alone

| Slice | What lands | Estimate |
|---|---|---|
| 1 | `card.go` + `score.go` + `session.go` (read) + `council replay` + golden tests on the three fixtures | 1.5–2 d |
| 2 | `seat.go` (roster, weights, board policy, transport limits) + `quotes.go` + `council check` | 1 d |
| 3 | `runner.go` behind a `Speaker` interface (parallel per-seat sends), resend-once on a malformed card, cost reporting per council | 3–4 d |
| 4 | TUI stage in the rooms view + `/council`, export of `verdict.md` | 3–4 d |
| 5 | Content: 3–5 in-house seat brains + the seat-building skill | 1–2 d per seat, mostly unattended |

Slices 1–2 have no dependency on a gateway, a model or a network, and they are where the risk is: if
the card grammar and the arithmetic do not reproduce the published verdict, nothing above them is
worth building. That is why they come first.

## Risks

- **Token cost.** A full council is 21 seat runs plus 4 Chair calls, about 1M tokens upstream. Every
  council must report its own cost (`rooms` already reports usage per member) and `--quick` must exist
  from day one.
- **Card drift.** A model that skips `OPTION SCORES` produces an unscoreable card. One resend, then the
  seat is reported **missing** — never scored as 0, which would silently move the verdict.
- **Brain size.** ~25k tokens of brain per seat is fine for a hosted model and not for a small local
  one; the seat loader must report the brain's token-ish size and the transport must be able to drop to
  `_hot.md` only.
- **The gateway's own rules.** If a council runs in a room, the room's budget still applies (3 rounds,
  10 messages). A 5-seat council spends 10 messages in two rounds. Say so in the header rather than
  discovering it as a silent stop.
- **Licensing.** Upstream is MIT; its example sessions and any redistributed brain need the notice.
