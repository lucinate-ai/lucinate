# rooms — delta for room-rules

## ADDED Requirements

### Requirement: The room's discussion policy is visible in the app

A room SHALL expose the policy the gateway enforces — who may speak, when, and under which
budgets — from inside the room, without leaving it.

#### Scenario: `/rules` in a room

- **WHEN** the user opens a room and types `/rules`
- **THEN** the transcript shows the four member rules (reply only with something new, `(pass)`
  when there is nothing new, mention a peer to pull them into the next round, never reveal
  private content), the roster bound (2–6), the round cap (3), the message cap (10), and the
  pass token
- **AND** the overlay is capped to the pane height with `… N more`, the composer stays visible,
  and `esc` closes it without leaving the room

#### Scenario: `/help` points at the rules

- **WHEN** the user types `/help` in a room
- **THEN** `/rules` appears in the command list

### Requirement: The room reports its position against the budget

The room header SHALL show the live discussion position derived from the gateway's own turn
coordinates, and the reason a discussion ended.

#### Scenario: A discussion in progress

- **WHEN** the room log holds two rounds and seven member messages for the open discussion
- **THEN** the header shows `round 2/3 · 7/10 messages`
- **AND** the text degrades to `2/3 · 7/10` rather than overflowing at 80 columns

#### Scenario: A discussion that ended without a new round

- **WHEN** the gateway settles the discussion because no member had anything new to say
- **THEN** the header states `settled — silent round`

#### Scenario: A discussion that hit a cap

- **WHEN** the gateway bounds the discussion at three rounds or ten messages
- **THEN** the header states `bounded — max rounds` or `bounded — max messages`

### Requirement: A peer pull is shown where it happened

When a member's reply cites a peer and that citation opens the next round, the transcript SHALL
mark it at the citing message.

#### Scenario: A member pulls a peer in

- **WHEN** `@a`'s reply contains `@b` and the gateway schedules `@b` for round 2
- **THEN** the transcript annotates `@a`'s message with `@a pulled @b into round 2`

#### Scenario: A mention that pulls nobody

- **WHEN** a reply cites a handle that is not on the roster, or cites a peer already heard from
  in this discussion
- **THEN** no annotation is rendered

### Requirement: Routing modes describe what they do to the engine

The mode line SHALL state the effect of the active mode on the gateway's turn selection, and
SHALL NOT imply that the client decides who runs.

#### Scenario: Broadcast mode

- **WHEN** the room is in broadcast mode
- **THEN** the mode line reads that everyone on the roster answers the user's message, and that
  later rounds happen only when a member cites a peer

#### Scenario: Moderator mode

- **WHEN** the room is in moderator mode with `@mod` set
- **THEN** the mode line names `@mod` as the member every un-mentioned message goes to

#### Scenario: Round-robin mode

- **WHEN** the room is in round-robin mode
- **THEN** the mode line states that the client addresses the next member, and that later rounds
  still follow peer mentions and the gateway's rotation

### Requirement: A room preset can state its peer-discussion intent

A room preset MAY carry a `peer discussion` flag; the flag SHALL be carried into the room as an
instruction in the first message, never as a client-side block.

#### Scenario: Preset with peer discussion on

- **WHEN** a preset with `peer discussion: on` starts a room
- **THEN** the room's first message tells the members they may cite a peer to pull them into the
  next round

#### Scenario: Preset with peer discussion off

- **WHEN** a preset with `peer discussion: off` starts a room
- **THEN** the first message tells the members to answer only when addressed
- **AND** the client does not claim to block peer mentions, because it cannot
