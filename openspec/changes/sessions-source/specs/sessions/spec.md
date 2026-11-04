# sessions — delta for sessions-source

## ADDED Requirements

### Requirement: A session row says where the session came from

The session browser SHALL show the source the gateway reports for a session, without inventing one
when the gateway reports none.

#### Scenario: A row with a source

- **WHEN** the gateway lists a session with `source: "bot_room"`
- **THEN** the session's row shows `bot_room · 2h ago` (the source as a dim tag before the relative
  time)

#### Scenario: A row with no source

- **WHEN** the gateway lists a session whose `source` is absent or empty
- **THEN** the row shows only the relative time, with no separator or placeholder tag

#### Scenario: The source survives the mapping

- **WHEN** `session.list` returns rows carrying `source`
- **THEN** `SessionsList` returns that value unchanged in the session-browser row

### Requirement: The session filter matches a source

The session browser's `/` filter SHALL match a session's source as well as its title, preview and
model, so a user can narrow the list to one kind of session without a new key binding. The match is
the browser's existing fuzzy match, not an exclusive filter: a query naming a source surfaces that
source's sessions at the top of the list.

#### Scenario: Filtering by source

- **WHEN** the user types `/cli` in the session browser
- **THEN** sessions whose source is `cli` are matched and rank at the top of the list
- **AND** the query is matched the same fuzzy way as a title, so a row whose other text happens to
  contain the letters is not dropped from the list

#### Scenario: Filtering by title still works

- **WHEN** the user types a fragment of a session's title
- **THEN** that session is listed, whether or not its source matches the query
