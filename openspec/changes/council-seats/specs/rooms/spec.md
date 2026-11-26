# rooms — delta for council-seats

## ADDED Requirements

### Requirement: A room that carries a council states the council's budget

When a room runs a council, the room surface SHALL state the transport's limits alongside the
discussion's own budget: the roster limit of the hosted room, that member turns are sequential, and that
a council spends the room's message budget. The surface SHALL NOT present a council as parallel when the
transport schedules one member turn at a time.

#### Scenario: A council seated in a room

- **WHEN** a four-seat council starts in a hosted room
- **THEN** the header states the room's round and message budget
- **AND** it states that seats answer in turn

#### Scenario: A board the room cannot hold

- **WHEN** the requested board is wider than the room's roster limit
- **THEN** the room surface reports the limit and does not create the room
- **AND** no seat is dropped to fit
