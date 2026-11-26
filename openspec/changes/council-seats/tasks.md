# Tasks — council seats

Slice 1 and 2 are hermetic: no gateway, no model, no network. They are where the risk lives, so they
come first and they land alone.

## 0. Fixtures

- [ ] 0.1 Vendor the three example councils from `github.com/nateherkai/roast-room`
      (`sessions/2026-10-09-example-take-the-seed`, `…-first-sales-hire`, `…-raise-coaching-price`)
      into `internal/council/testdata/upstream/` with the upstream MIT notice in
      `testdata/upstream/LICENSE`.
- [ ] 0.2 Record the published verdict of the `take-the-seed` council as `want.json` (A 2.0 · B 7.3 ·
      C 6.0, support 8.0, confidence 7, spread 1, endorse 0 / amend 7 / dissent 0) so the golden test
      has a written expectation rather than a recomputed one.

## 1. Card grammar and scoring (the part that must be right)

- [ ] 1.1 `internal/council/card.go`: `Card` with `Seat`, `Stance`, `OptionScores map[string]float64`,
      `Pillar`, `FinalScore`, `Confidence`, `Ruin`, `Door`, `Call`, `Why`, `Risk`, `ChangeMind`,
      `ThisWeek`, `Question`, `DisagreeWith`, `Concede`, `Moved`, `Vote`, `Amendment`, `OneLine`.
      Parser reads a card out of markdown body text; fields may share a line separated by `·` or `|`.
- [ ] 1.2 The option-scores line is read to end-of-line before the `·` field split (options are
      `·`-separated), and `PILLAR SCORE` / `FINAL PILLAR SCORE` / `FINAL SCORE` stay distinct.
      Table tests for: three options, two options, a missing score, a score outside 0–10, a `FINAL`
      card, and the `·`-shared-line shape the upstream writer produces.
- [ ] 1.3 `splitCards(md)`: `## <Seat>` headings (accepting key, short name, full name) with Chair
      sections dropped; fallback to `SEAT:` line starts when there are no headings. Tests against all
      three upstream round files: seven cards from `take-the-seed` round 1, four from the quick
      session, and the `## Chair notes` / `## Chair` sections never becoming a card.
- [ ] 1.4 `internal/council/score.go`: weighted option mean (one decimal), band, ruin cap, one-way-door
      cap, unanimity check, spread, confidence, resolution support, vote counts. Pure functions, no
      formatting inside.
- [ ] 1.5 Golden test: for each upstream example, parse every round, score the last round, and compare
      `Verdict` field by field with `want.json`. A mismatch names the field and both values.
- [ ] 1.6 Override table test: ruin-likely seat caps at RESHAPE with the mitigation missing; one-way
      door with spread ≥4 caps at GO WITH CONDITIONS; spread ≥5 subtracts the confidence point; a
      unanimous round 1 with scores within 1 point raises the unanimity flag.

## 2. Session folder, seats and quotes

- [ ] 2.1 `internal/council/session.go`: `Session` reads a directory — `council.json`, `brief.md`,
      `round-N.md`, `cruxes.json`, `draft.json`, `verdict.json`, `verdict.md` — reporting which files
      exist, which are unparseable, and whether the council is complete. A missing file is a state, an
      unparseable one is an error the reader retries (never silently absent).
- [ ] 2.2 `Session.Verify()`: recompute the verdict from the cards and compare with `verdict.json`;
      report drift field by field without rewriting anything.
- [ ] 2.3 `internal/council/seat.go`: roster JSON in the upstream schema, `Roster.Validate()` (unique
      keys, a pillar, a question, one to two seats at weight 3), board policy (`ask|auto|default`),
      quick-board selection (two highest weights + the downside seat + the dissent seat), and the
      transport's `MaxSeats` with a refusal that names the limit.
- [ ] 2.4 `internal/council/quotes.go`: normalise (lower-case, fold quotes/apostrophes, collapse
      punctuation and whitespace), then require every `"…" (S123)` quote of ≥5 words to appear in the
      seat's brain text. Returns verified/unverified per quote; an unverified quote makes the verdict
      fail closed. Port the semantics of both the upstream Python script and the app's `checkQuotes`.
- [ ] 2.5 Tests: a verbatim quote passes, a paraphrase fails, a wrong source id with a real quote is
      reported as a source mismatch, and a quote from another seat's brain does not verify for this one.

## 3. CLI

- [ ] 3.1 `lucinate council replay <dir>` prints the recomputed verdict as the report template does
      (label, call, option scores with their math, why, fights, minority, conditions, next steps,
      cheapest test, kill criteria), and appends a drift line when `verdict.json` disagrees.
- [ ] 3.2 `lucinate council check <dir>` prints score drift and unverified quotes, exit 1 on either.
- [ ] 3.3 `lucinate council seats [--roster <path>]` prints the roster as a table (seat, pillar,
      question, on the default board or not).
- [ ] 3.4 Dispatch in `internal/cli/cli.go`, help text in `internal/cli/help.go`, plus a
      `staticcheck`-clean, `gofmt`-clean pass and `go test ./...` green.

## 4. Runner (first transport: direct per-seat sends)

- [ ] 4.1 `runner.go` drives the protocol through a `Speaker` interface: `Brief`, `Round1`, `Cruxes`,
      `Round2`, `Draft`, `Round3`, `Verdict`, each returning either a card or a structured Chair
      object; the runner owns the session folder and the write order.
- [ ] 4.2 Seat transport: one `backend.Backend` session per seat, run in parallel with a bounded
      concurrency, each with the seat's system prompt (brain `_hot.md` first, then at most three more
      files). Malformed card: one resend with the missing fields named, then the seat is reported
      **missing** and excluded from the score — never zero.
- [ ] 4.3 Room transport (bounded): a council over a hosted room refuses more than `MaxSeats`, states
      the sequential-turn and 10-message budget in the room header, and reads cards back from the
      durable log through the same parser.
- [ ] 4.4 Cost: every council reports tokens and calls per seat and in total, in the session
      (`cost.json`) and in the TUI, reusing the rooms usage idiom.
- [ ] 4.5 Hermetic test with a fake `Speaker` covering: happy path three rounds, a seat that returns a
      malformed card twice, a seat that times out, and a quick council that skips round 3 on a settled
      spread with no ruin risk.

## 5. TUI surface

- [ ] 5.1 `/council <decision>` in a room starts a council: brief preview, board preview with weights
      and the transport limit, `y` to convene, `esc` to cancel (the confirm is its own substate).
- [ ] 5.2 The stage: stepper header (`brief · openings · fault lines · debate · vote · verdict`); one
      row per card showing `Short · stance · A/B/C`; `enter` opens the full card; the plates line shows
      each option's running score and its math.
- [ ] 5.3 The cruxes block and the peer-style disagreement links (`@a vs @b`) rendered from
      `cruxes.json`, so a second round is never a mystery.
- [ ] 5.4 The verdict sheet: label, call, scorecard table, why/fights/minority, conditions and next
      steps, with `e` writing `verdict.md` to the export directory.
- [ ] 5.5 Render tests with `teatest/v2` for the stage and the verdict sheet; key dispatch registered in
      `Actions()` for the substate.

## 6. Specs, docs, verification

- [ ] 6.1 `openspec/specs/council/spec.md` created by archiving this change; one requirement in
      `openspec/specs/rooms/spec.md` about declaring a council's transport limits.
- [ ] 6.2 `docs/council.md`: what the card grammar costs, where the gateway's engine fits (3 rounds) and
      where it does not (roster cap, sequential turns, no orchestrator seat), why the Chair is code, and
      the golden-test trick.
- [ ] 6.3 `make test`, `gofmt -l .` empty, `go vet ./...` clean.
- [ ] 6.4 Live pass: one 4-seat council against a real gateway in a room, one 5-seat council through the
      seats transport, both replayed with `lucinate council replay` and both matching their
      `verdict.json`.
