# council — delta for council-seats

## ADDED Requirements

### Requirement: A council decides one decision over named options

A council SHALL be built from one decision, two to four named options, and a roster of seats. The
options SHALL always include a status-quo option ("don't do it" or "keep doing what you do now"). Every
seat at the table SHALL carry a relevance weight between 1 and 3, and the roster SHALL refuse a board
where more than two seats carry weight 3.

#### Scenario: A two-option decision gets a status quo

- **WHEN** the user brings only one proposal
- **THEN** the brief carries the proposal and a do-not-proceed option
- **AND** every seat scores both options

#### Scenario: Weights are bounded

- **WHEN** a board assigns weight 3 to three seats
- **THEN** validation fails naming the three seats
- **AND** no council is created

### Requirement: Every seat card conforms to one grammar

A seat's answer SHALL be parsed as a card carrying a stance, one score per option on the 0–10 scale, a
pillar score, a confidence on the 0–5 scale, a ruin-risk level, a door type, the call, one to three
arguments each naming a framework or principle, the risk the seat sees, what would change its mind, what
it would do this week, and the question it would ask the owner. A ratification card SHALL additionally
carry a vote, a final score, a final pillar score and one line for the owner.

#### Scenario: Option scores are read to the end of the line

- **WHEN** a card carries `OPTION SCORES: A 2/10 · B 7/10 · C 6/10` followed on the same line by
  `PILLAR SCORE: 6/10`
- **THEN** the parser reads three option scores and a pillar score
- **AND** the `·` between options is not treated as a field separator

#### Scenario: A final card's score fields stay distinct

- **WHEN** a ratification card carries `FINAL SCORE: 8/10` and `FINAL PILLAR SCORE: 7/10`
- **THEN** the resolution support uses `FINAL SCORE` and the scorecard uses `FINAL PILLAR SCORE`

#### Scenario: A malformed card is asked for once

- **WHEN** a seat's card is missing its option scores
- **THEN** the seat is asked once more with the missing fields named
- **AND** a second malformed card marks the seat missing
- **AND** a missing seat is excluded from the score rather than counted as zero

### Requirement: The score is computed, never narrated

The system SHALL compute each option's score as the weight-weighted mean of the seats' scores for that
option, rounded to one decimal, and SHALL derive the verdict label from the recommended option's score:
7.5 and above GO, 6.0 to 7.4 GO WITH CONDITIONS, 4.5 to 5.9 RESHAPE, below 4.5 DON'T DO IT. The system
SHALL compute the spread as the highest minus the lowest seat score on the recommended option, the
confidence as twice the mean seat confidence rounded, reduced by one when the spread is 5 or more and
clamped to 1–10, and the resolution support as the weighted mean of the final scores.

#### Scenario: The published example reproduces

- **WHEN** the `take-the-seed` example council is scored from its round 3 cards
- **THEN** the option scores are A 2.0, B 7.3, C 6.0
- **AND** support is 8.0, confidence 7, spread 1, and the votes are 0 endorse, 7 amend, 0 dissent

#### Scenario: A model's stated verdict does not override the arithmetic

- **WHEN** a verdict models a label that its own scores do not support and names no override
- **THEN** the label is replaced by the computed band
- **AND** the substitution is reported to the reader

### Requirement: Two overrides cap a verdict, and each names its cause

A seat ending at ruin risk likely on the recommended option SHALL cap the verdict at RESHAPE until the
resolution names a mitigation that seat endorsed. A one-way-door recommendation with a spread of 4 or
more SHALL cap the verdict at GO WITH CONDITIONS and make the cheapest test a precondition. Every
applied override SHALL be stated in one line naming the seat that caused it.

#### Scenario: One seat at ruin risk outweighs six enthusiasts

- **WHEN** six seats recommend GO at 8/10 and one seat records `RUIN RISK: likely`
- **THEN** the verdict is capped at RESHAPE
- **AND** the cap line names that seat

#### Scenario: A one-way door with a wide spread

- **WHEN** the recommended option is a one-way door and its seat scores span 4 or more points
- **THEN** the verdict is capped at GO WITH CONDITIONS
- **AND** the cheapest test becomes a precondition of the recommendation

### Requirement: A unanimous opening round is challenged, not celebrated

When every seat's top option is the same and their scores for it sit within one point in round 1, the
system SHALL mark the round unanimous and SHALL name the strongest unargued alternative as a crux, with
the seats whose pillars are most at risk asked to argue it at full strength.

#### Scenario: Seven seats agree in round 1

- **WHEN** all seats pick option B with scores between 7 and 8
- **THEN** the round is marked unanimous
- **AND** a crux naming the strongest alternative is written into the session

### Requirement: A session is a folder with a fixed write order

A council session SHALL be written as a directory holding `council.json`, `brief.md`, `round-1.md`,
`cruxes.json`, `round-2.md`, `draft-resolution.md`, `draft.json`, `round-3.md`, `verdict.md` and
`verdict.json`, in that order. `verdict.json` SHALL be written last and SHALL NOT carry option scores,
support, confidence or votes. A reader SHALL treat an unparseable file as a write in progress and retry,
and SHALL treat a missing file as a state rather than an error.

#### Scenario: A verdict that outlives its cards

- **WHEN** `verdict.json` holds scores that differ from the ones recomputed from the round files
- **THEN** replay reports the drift field by field
- **AND** neither the folder nor the verdict file is rewritten

#### Scenario: A half-written JSON file

- **WHEN** a reader opens `cruxes.json` while it is still being written
- **THEN** the reader retries rather than reporting the cruxes as absent

### Requirement: A quote is either verbatim in the seat's brain or it is removed

Every sentence presented as an advisor's own words SHALL carry a source id and SHALL be found verbatim,
after normalising case, apostrophes, punctuation and whitespace, in that seat's brain text. A quote that
is not found SHALL be removed from the verdict and reported. The system SHALL NOT soften an unverified
quote to a paraphrase, and SHALL NOT attribute a line to the person a seat simulates.

#### Scenario: A paraphrase with a plausible source id

- **WHEN** a verdict quotes a sentence that appears in no brain file of that seat
- **THEN** the quote is removed from the verdict
- **AND** the removal is reported with the source id it claimed

#### Scenario: Another seat's real quote

- **WHEN** a card attributes to Graham a line that exists only in Buffett's brain
- **THEN** the quote does not verify for Graham

### Requirement: A council declares its transport and its limits

A council SHALL state which transport runs it and what that transport allows. When the transport is a
hosted room, the council SHALL refuse a board wider than the room's roster limit, naming that limit, and
SHALL state that member turns are sequential and share the room's message budget. The system SHALL NOT
silently drop seats to fit a transport.

#### Scenario: A seven-seat board in a room

- **WHEN** the user convenes a seven-seat council in a hosted room whose roster limit is six
- **THEN** the council refuses before seating anyone
- **AND** the refusal names the limit and offers the seats transport

#### Scenario: A quick council in a room

- **WHEN** a four-seat quick council runs in a room
- **THEN** the header states the room's round and message budget
- **AND** the council reports which seats spoke in each round
