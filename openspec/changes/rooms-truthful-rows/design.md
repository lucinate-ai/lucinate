# Design — rooms truthful rows

## Evidence from the live run (2026-11-04 08:27–08:35, gateway v0.22.5)

Raw log (`lucinate rooms log alojz-default-082746 --json`), two consecutive events of one turn:

```
message.member  actor {"kind":"member","id":"m1","display_name":"Alojz","profile":"alojz"}
                payload {"member_id":"m1","member_index":0,"round_index":0,…}
turn.settled    actor {"kind":"gateway","id":"install:6361e5ff3c924dcd8c742d702c4921da"}
                payload {"member_id":"m1","member_index":0,"passed":false,"round_index":0,…}
turn.cancelled  actor {"kind":"gateway","id":"install:6361e5ff…"}
                payload {"member_id":"m1","member_index":0,"reason":"member turn cancelled"}
```

Screen, same run:

```
08:28:19  install:6361e5ff3c924dcd8c742d702c4921da finished
  @install:6361e5ff3c924dcd8c742d702c4921da — przerwano: member turn cancelled
  ⚠ deklaracja zapisu bez potwierdzenia — brak HANDOFF      ← under a reply that only quoted a path
```

## Decision 1 — identify a turn's member from the payload, not the actor

`Event.Speaker()` is right for `message.member` (the actor *is* the member) and wrong for `turn.*`
(the actor is the gateway that scheduled or settled the turn). The payload carries `member_id`
and `member_index`, and `Room.Members` carries `member_id` → `profile` → handle, so the room
already holds the answer.

- Add one resolver in `internal/rooms` (pure, testable): given an `Event` and the roster, return
  the member's handle — `member_id` match first (stable across reconnect and roster edits),
  `member_index` as the fallback (the gateway always sets both on turn events), and `""` when the
  roster cannot place the member.
- The TUI row formatter and `eventHandle` consult that resolver before falling back to
  `Event.Speaker()`. A row whose member is unplaceable keeps today's rendering rather than
  inventing a name — a lost label must not become a wrong label.
- `Event.Speaker()` itself is left alone: it describes an actor, and other call sites (export,
  CLI rows) legitimately use it.

Why not a `Dispatch` map in the TUI: the roster is already in `roomsModel.openRoom`, the resolver
is a pure function over data both surfaces share, and a test can drive it without a model.

## Decision 2 — a claim needs a claim marker and a target

`claimsWrite` today:

```go
for _, marker := range claimMarkers { if strings.Contains(lower, marker) { return true } }
return claimFilePattern.MatchString(text)   // ← a bare ".md" is enough
```

The docstring says the claim shape is "plik X.md in the same breath as a marker", so the code is
the bug, not the intent. New rule:

1. **Negation first.** A reply that denies writing is not a claim: `nie zapisa`, `nie utworz`,
   `nie wdroż`, `bez zapisu`, `nic nie zapisa`, `not saved`, `no file written`, `didn't write`.
   Checked before any marker so `nie zapisałem pliku report.md` stays silent.
2. **Marker and target.** Claimed only when a marker from `claimMarkers` appears **and** the reply
   names a deliverable (`.md`, `.go`, `.json`, `.html`) or an explicit `HANDOFF`-less first-person
   delivery (`zapisałem`, `utworzyłem`, `wdrożyłem`, `wygenerowałem`).
3. **Everything else is `none`.** A path in a sentence is a reference; an opinion is an opinion.
   The row stays off, which is the behaviour the comment promises ("painting every opinion with a
   red cross taught readers to ignore the column entirely").

Consequence accepted: a bare "zapisałem" with no path and no first-person delivery verb no longer
raises the flag. That reply is unverifiable anyway — the room's contract asks for
`HANDOFF <path> bytes=<n>`, and `VerifyReply` still verifies every receipt it is given.

## Decision 3 — a control row is an action, so it gets wording rather than an actor

The `room.*` control kinds have no member behind them: the actor is the gateway installation, which
is why the default branch (`ev.Kind + " " + ev.Speaker()`) leaked `room.stop_requested install:<hash>`
after every `/stop`. The fix is not another `case` per kind in two formatters — it is one resolver
the TUI and the CLI both call:

- `ControlLabel(kind)` maps a known control kind to its wording (`stop requested`, `room created`,
  `roster changed`); unknown kinds keep their own name.
- `EventLabel(ev)` appends the speaker **only** when that speaker is not an installation id, so a
  future gateway kind with a member or person behind it still reads correctly, and one with an
  installation id cannot regress into the same defect.

`Event.Speaker()` keeps its meaning (it describes an actor, and export/CLI still use it), so the
unplaceable-member fallback documented under Decision 1 is untouched.

## Tests that must exist afterwards

- Resolver: `member_id` hit; `member_index` fallback when `member_id` is absent; unknown member
  returns `""`; a `message.member` event still resolves through its own actor.
- Rows: a `turn.settled` event with a gateway actor renders `@alojz finished`, not `install:<hash>`.
- Evidence: a reply that only quotes `docs/rooms-live-check.md` is `none`; the live sentence from
  this run is the regression fixture. A reply with `zapisałem … report.md` and no receipt is
  `claimed`. `nie zapisałem pliku report.md` is `none`. `HANDOFF … bytes=<right>` stays `verified`
  and a wrong byte count stays `failed` (existing tests must keep passing).
