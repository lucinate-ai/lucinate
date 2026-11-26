# rooms — delta for honest-stream-failures

## ADDED Requirements

### Requirement: A cancelled turn is terminal

A `turn.cancelled` event SHALL end the member's stream the same way
`turn.failed` and `turn.deferred` do: the row stops counting as an active answer,
and the discussion budget stops counting it as an open turn. A cancellation the
user requested with `/stop` is not a pending answer, and the spinner SHALL NOT
keep animating for it.

#### Scenario: `/stop` after the member started answering

- **WHEN** the gateway emits `turn.started` and then `turn.cancelled` for a member
- **THEN** that member's row is no longer an active stream
- **AND** the partial text that did arrive stays on screen
- **AND** the spinner stops

#### Scenario: `/stop` before any text

- **WHEN** the gateway emits `turn.started` and then `turn.cancelled` with no
  text in between
- **THEN** the row settles as an empty reply rather than spinning forever
