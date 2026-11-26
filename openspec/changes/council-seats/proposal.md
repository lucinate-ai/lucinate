# Council seats — a scored board of advisors, ported from the Roast Room form

## Why

`github.com/nateherkai/roast-room` is a working, opinionated protocol for one thing lucinate does not
do: taking a single hard decision, seating a board of advisors on it, running three rounds of
argument, and returning a **scored verdict** with a dissent, conditions, a cheapest test and kill
criteria. Its value is not the cartoon room. It is the contract:

- every seat answers in a fixed **card grammar** (`STANCE`, `OPTION SCORES`, `PILLAR SCORE`,
  `CONFIDENCE`, `RUIN RISK`, `DOOR`, `WHY` with framework + source id, `WHAT WOULD CHANGE MY MIND`),
  so cards are comparable and machine-readable;
- the score is **computed, never narrated**: weighted option means, bands at 7.5 / 6.0 / 4.5, a ruin
  cap, a one-way-door cap, a spread and a confidence number;
- the session is a **folder with a documented write order** (`council.json`, `round-N.md`,
  `cruxes.json`, `draft.json`, `verdict.json`), which is why any runtime can run the protocol and any
  viewer can replay it;
- a seat may only quote its person **verbatim with a source id**, checked against a compiled "brain".

lucinate already has the container for this: hosted rooms with a durable, append-only transcript, a
2–6 member roster of real Hermes profiles, mention routing, three gateway rounds, per-room shared
directories, handoff receipts with read-back verification, an evidence column and transcript export.
What is missing is the **decision surface**: options, per-seat scores, a computed verdict, replay and
a quote check. Today a room argues; it never lands anywhere a reader can act on.

The Roast Room also ships its own fallback for runtimes that cannot spawn sub-agents ("run the same
protocol sequentially yourself"), which is precisely the situation a TUI client is in. Porting the
protocol as Go code — not as a prompt — is a better fit than porting the harness.

## What changes

- **`internal/council`** (new package): seat roster loading, the card grammar and its parser, the
  scoring engine, the session folder contract, and replay. All pure and table-tested; the scoring
  engine is validated against the three example councils shipped with the upstream repository, whose
  published verdict (A 2.0 · B 7.3 · C 6.0, support 8.0, confidence 7, spread 1, 0/7/0 votes) is
  reproduced exactly by the port.
- **`lucinate council`** (new CLI): `replay <session-dir>` prints the verdict recomputed from the round
  files, `check <session-dir>` reports score drift and unverified quotes, `seats` lists the roster.
- **Seat quotes are verified** against the seat's brain (normalised, verbatim, source id), the one
  evidence axis rooms does not have — beside `HANDOFF` receipts, not replacing them.
- **A council declares its transport and its limits.** A council over a hosted room states the
  gateway's roster cap (6 members) and that turns are sequential; a council that asks for more seats
  than the transport allows refuses with the limit named instead of silently dropping seats.
- **TUI surface** (follow-on slice): a council stage inside the rooms view — stepper header, one row
  per card, plates for the option scores, cruxes block, verdict sheet — reusing the transcript idiom
  the rooms view already uses. No portraits, no browser, no Node.

Out of scope: the cartoon room and its portraits (`kie.ai`), the Node server and its SSE/browser
stage, the PDF builder, and `/newroaster`'s image pipeline. Those are presentation, and the terminal
has its own.

## Capabilities

### New Capabilities

- `council`: a bounded, scored council over named options — seats, cards, the scoring engine and its
  overrides, the session folder contract, replay, quote verification.

### Modified Capabilities

- `rooms`: a room can carry a council; the council's transport limits (roster cap, sequential turns)
  are stated in the room surface rather than worked around.

## Impact

- `internal/council/*.go` (new): `seat.go`, `card.go`, `score.go`, `session.go`, `quotes.go`.
- `internal/cli/council.go` (new) + `internal/cli/cli.go` (dispatch) + `internal/cli/help.go`.
- `internal/rooms/` — a council session directory under the room's shared dir; no change to the wire
  protocol, the discussion budget or `docs/room-rules.md`.
- `internal/tui/rooms.go`, `internal/tui/rooms_ux.go` — the follow-on stage and its `/council` entries.
- `openspec/specs/council/spec.md` (new), `openspec/specs/rooms/spec.md` (one requirement about
  declaring transport limits).
- `docs/council.md` (new, after implementation) — the lessons: what the card grammar costs, where the
  gateway's engine fits and where it does not, why the Chair is code.
- Testdata: the three example councils from the upstream repository, with its MIT notice, as golden
  fixtures.
