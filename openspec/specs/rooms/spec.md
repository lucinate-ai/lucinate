# Rooms Specification

## Purpose

Rooms are Hermes hosted "Bot Mode" group chats: a durable, append-only transcript hosted
by one gateway, with a frozen roster of 2–6 Hermes profiles local to that gateway. Each
member is a bot that answers when the room receives a user message. `internal/rooms` is the
transport-thin client (JSON-RPC over the gateway WebSocket, `groups.*` methods) and
`internal/tui/rooms.go` + `internal/tui/rooms_ux.go` are the TUI over it.

Mention routing is **server-side**: the gateway parses `@handle`, `@all`/`@everyone` and
"no mention" itself, so every client-side routing decision this spec describes is expressed
by the client adding a mention to the text it sends.

This spec covers the room menu's transcript surface: message routing, streaming replies,
reconnect/resume, transcript export, long-conversation compaction, per-member colours, cost
statistics and search.

## Requirements

### Requirement: Mention routing

The system SHALL parse `@handle` mentions out of an outgoing message using the gateway's own
mention grammar (`@([A-Za-z0-9][A-Za-z0-9._:-]*)`) and SHALL resolve them against the room's
roster, matching either the member's handle or its profile name.

An `@` preceded by a handle character SHALL NOT start a mention, so an email address in a
message is not mistaken for one. A mention that matches no roster member SHALL NOT stop the
message: it SHALL be sent as written and reported to the user, because the gateway remains
the authority on which profiles a room contains.

`@all` and `@everyone` SHALL address the whole roster and SHALL NOT be rewritten.

#### Scenario: A mention addresses one member

- **GIVEN** a room whose roster is `matt`, `kowal`
- **WHEN** the user posts `@kowal status?`
- **THEN** the message is sent unchanged
- **AND** the decision names `@kowal` as the only recipient
- **AND** the routing mode is not applied

#### Scenario: An email address is not a mention

- **GIVEN** a room with a member whose handle is `example.com`
- **WHEN** the user posts `napisz na kowal@example.com`
- **THEN** no mention is parsed
- **AND** the message is routed by the room's mode as if it had no mention

#### Scenario: An unknown handle is reported, not swallowed

- **GIVEN** a room whose roster has no `ghost` member
- **WHEN** the user posts `@ghost hej`
- **THEN** the message is sent as written
- **AND** the status line names `@ghost` as an unknown handle

### Requirement: Routing modes

The system SHALL support three routing modes per room: `broadcast` (the default — the whole
roster answers), `moderator` (one nominated member answers) and `round-robin` (the roster
takes turns). The mode SHALL be selectable with `/mode broadcast|moderator|round-robin`, and
the moderator with `/moderator [@handle]`.

A mode SHALL steer only messages that carry no mention of their own: an explicit `@handle`
SHALL always win, because the user typed it.

Moderator and round-robin routing SHALL append the chosen member's mention to the outgoing
text, since mention routing is server-side.

The round-robin cursor SHALL advance once per routed message and SHALL be persisted, so the
rotation survives a restart.

A moderator that matches no roster member SHALL fall back to the roster's first member and
SHALL say so.

#### Scenario: Round-robin walks the roster in order

- **GIVEN** a room in `round-robin` mode with roster `matt`, `kowal`, `olesno-lead-ops`
- **WHEN** three messages are posted
- **THEN** they are sent as `@matt …`, `@kowal …`, `@olesno-lead-ops …`
- **AND** the fourth message is sent as `@matt …` again

#### Scenario: The mode cannot swallow an explicit mention

- **GIVEN** a room in `moderator` mode with moderator `matt`
- **WHEN** the user posts `@kowal tylko ty`
- **THEN** the message reaches the gateway as `@kowal tylko ty`

#### Scenario: A room reports its routing

- **GIVEN** a room in `moderator` mode with moderator `kowal`
- **WHEN** the transcript is rendered
- **THEN** the header states `moderator: @kowal`

#### Scenario: An unknown mode is refused

- **GIVEN** the transcript composer
- **WHEN** the user types `/mode dyktator`
- **THEN** the command is refused with the accepted modes named
- **AND** nothing is sent to the room

### Requirement: Streaming member replies

While a member is answering, the system SHALL render that member's reply as a live row: the
text produced so far, prefixed by a braille spinner that advances every 120 ms.

Partial replies SHALL be read from the room log, accepting both a dedicated delta kind
(`message.member.delta`) and a `message.member` event marked partial in its payload. Deltas
SHALL be treated as cumulative — a newer delta replaces the text — unless the payload asks to
append.

The spinner SHALL run only while a reply is in flight, and the row SHALL be removed the moment
the turn ends — including when it ends with no text, because an empty answer is a real outcome
and a spinner left against a silent member reads as a hung room.

#### Scenario: A reply is rendered as it is written

- **GIVEN** a member whose turn has started and whose first delta is `Pracuje nad tym`
- **WHEN** the transcript is rendered
- **THEN** the row shows `@matt` and `Pracuje nad tym`
- **AND** it carries a braille spinner glyph

#### Scenario: The spinner advances between frames

- **GIVEN** a member mid-answer
- **WHEN** the spinner ticks
- **THEN** the rendered glyph differs from the previous frame
- **AND** a further tick is scheduled

#### Scenario: An empty answer removes the placeholder

- **GIVEN** a member whose `turn.started` was followed by `turn.settled` with no text
- **WHEN** the transcript is rendered
- **THEN** no row for that member is shown

#### Scenario: The finished message replaces the live row

- **GIVEN** a member whose reply was streaming
- **WHEN** the final `message.member` event arrives
- **THEN** the streaming row disappears
- **AND** the finished text is rendered in the transcript

### Requirement: Reconnect and resume

When the gateway socket is lost, the system SHALL NOT discard the transcript on screen. It
SHALL drop the dead connection, report the loss, and redial on an exponential backoff (500 ms
doubling to a 30 s ceiling), then resume by refetching the log from the cursor the view already
holds, so no event is lost or repeated.

A failure the socket cannot fix — a JSON-RPC error, or a deliberate local close — SHALL NOT be
treated as a disconnect.

The backoff SHALL reset after a successful redial, and a scheduled redial SHALL be ignored if
a later attempt has superseded it.

#### Scenario: A dropped socket arms the backoff and keeps the transcript

- **GIVEN** a room transcript with events loaded and a live gateway connection
- **WHEN** the socket drops and the next poll fails
- **THEN** the events already loaded stay on screen
- **AND** the status line reports the redial and its delay
- **AND** the first backoff delay is used

#### Scenario: The backoff grows and resets

- **GIVEN** consecutive failed redials
- **WHEN** each redial fails
- **THEN** the delay doubles each attempt up to the ceiling
- **AND** after a successful redial the next failure starts from the base delay again

#### Scenario: History is resumed, not restarted

- **GIVEN** a room whose socket was dropped and redialed successfully
- **WHEN** the refetched page is applied
- **THEN** the transcript contains every event it showed before the drop, once
- **AND** the cursor is unchanged by the failure

#### Scenario: A gateway rejection is not a disconnect

- **GIVEN** a poll that fails with a JSON-RPC error
- **WHEN** the failure is classified
- **THEN** it is reported as an error
- **AND** no redial is scheduled

### Requirement: Transcript export

The system SHALL export the room transcript on `/export [md|json|both]`, writing into
`<lucinate data dir>/exports`. The markdown document SHALL name the room, its roster, the
generation time and every event in order with its speaker and time; the JSON document SHALL
carry the room, the roster and the raw events. Exports SHALL be written 0600 inside a 0700
directory.

#### Scenario: Export writes markdown and JSON

- **GIVEN** a room transcript with at least one user message and one member reply
- **WHEN** the user runs `/export both`
- **THEN** two files exist under `<lucinate data dir>/exports`
- **AND** the status line names both paths

#### Scenario: An export format that does not exist is refused

- **GIVEN** the transcript composer
- **WHEN** the user types `/export csv`
- **THEN** the command is refused with the supported formats named
- **AND** no file is written

#### Scenario: An empty room has nothing to export

- **GIVEN** a room with no events loaded
- **WHEN** the user runs `/export`
- **THEN** the command is refused with an explanation

### Requirement: Long-conversation compaction

`/compact [N]` SHALL summarise the older part of the transcript — everything except the last
`N` message events, which stay verbatim — and SHALL leave that summary in place of the
summarised messages. `N` SHALL default to 6 and SHALL be bounded at 100.

The brief SHALL be produced by the room: the system SHALL post a summarisation request to the
room (routed by the room's mode) and store the first member reply that follows it as the brief.
`/compact local [N]` SHALL instead build a deterministic local brief, so compaction still works
with no reachable roster.

The request that produced a brief SHALL be hidden with the messages it summarises, and the
compaction SHALL be persisted per room.

The gateway transcript is append-only and shared, so compaction SHALL be a client-side view: it
SHALL never modify the room's log.

#### Scenario: The head is replaced and the tail kept

- **GIVEN** a transcript of 24 messages and a compaction that keeps 4
- **WHEN** the transcript is rendered
- **THEN** the brief is rendered where the summarised messages were
- **AND** the last four messages are rendered in full
- **AND** the summarised messages are not rendered

#### Scenario: The brief comes from the room

- **GIVEN** a `/compact` request that reached the room at sequence 12
- **WHEN** a member replies at sequence 13
- **THEN** that reply is stored as the room's brief
- **AND** sequences 1–12 are replaced by it

#### Scenario: Compaction works without a roster

- **GIVEN** a room whose gateway cannot answer
- **WHEN** the user runs `/compact local 4`
- **THEN** a brief is stored immediately
- **AND** the last four messages stay verbatim

#### Scenario: Nothing to compact is reported

- **GIVEN** a transcript with fewer messages than the keep window
- **WHEN** the user runs `/compact`
- **THEN** the status line says there is nothing to compact

### Requirement: Per-member header colours

The system SHALL colour each member's transcript label from a palette, chosen deterministically
from the member's handle so colours do not change between sessions. `/header @handle #RRGGBB`
SHALL override the colour for one member and `/header @handle default` SHALL clear the override.
Colours SHALL be validated and normalised, persisted per room, and an invalid colour SHALL be
refused.

#### Scenario: A colour is validated, normalised and persisted

- **GIVEN** the transcript composer
- **WHEN** the user types `/header @matt #ff8800`
- **THEN** the member's colour is stored as `#FF8800`
- **AND** it survives a restart

#### Scenario: The default colour is deterministic

- **GIVEN** a room with no colour overrides
- **WHEN** the same roster is rendered twice
- **THEN** each member gets the same colour both times

#### Scenario: An invalid colour is refused

- **GIVEN** the transcript composer
- **WHEN** the user types `/header @matt nie-kolor`
- **THEN** the command is refused
- **AND** the stored colour is unchanged

### Requirement: Cost and token statistics

`/cost` SHALL report, per member, the turns answered, the messages posted and the input,
output and total tokens the gateway reported, plus cost when the gateway provides it. Payload
shapes that carry usage in different keys SHALL be accepted. A gateway that reports no tokens
SHALL produce an explicit "nothing reported yet" notice rather than a table of zeroes, and a
gateway that reports tokens but no cost SHALL say so.

#### Scenario: Usage is attributed to the member that produced it

- **GIVEN** a member reply whose payload carries `{input_tokens, output_tokens, total_tokens, cost_usd}`
- **WHEN** the user runs `/cost`
- **THEN** the report names that member
- **AND** it shows the token totals and the cost

#### Scenario: No usage data is reported honestly

- **GIVEN** a room whose transcripts carry no usage numbers
- **WHEN** the user runs `/cost`
- **THEN** the notice says no usage has been reported yet

### Requirement: Transcript search

`/find <phrase>` SHALL search the rendered transcript, case-insensitively, and SHALL list the
hits with their time, speaker and a snippet around the match. Matching lines SHALL be highlighted
in the transcript. `/find` with no phrase SHALL clear the search.

#### Scenario: Hits are listed and highlighted

- **GIVEN** a transcript containing `raport` in two messages
- **WHEN** the user runs `/find raport`
- **THEN** the notice reports two matches
- **AND** each hit is listed with a snippet
- **AND** the matching lines are highlighted

#### Scenario: No match is reported

- **GIVEN** a transcript that does not contain the phrase
- **WHEN** the user runs `/find nie-ma-takiego`
- **THEN** the notice says there were no matches

#### Scenario: The search is cleared

- **GIVEN** an active search
- **WHEN** the user runs `/find`
- **THEN** the query and the highlight are cleared

### Requirement: Room transcript command surface

The transcript SHALL intercept `/`-prefixed input and SHALL refuse an unrecognised command
rather than posting it to the room, because an unrecognised command posted as a message starts
the whole roster answering. `\`-prefixed input SHALL be posted as a literal message.

`/help` SHALL list the room command surface.

#### Scenario: An unknown command is refused

- **GIVEN** the transcript composer
- **WHEN** the user types `/straszna-komenda`
- **THEN** nothing is sent to the room
- **AND** the error names `/help`

#### Scenario: Help lists the surface

- **GIVEN** the transcript composer
- **WHEN** the user types `/help`
- **THEN** the notice lists `/mode`, `/moderator`, `/export`, `/compact`, `/find`, `/cost` and `/header`

#### Scenario: An escaped slash is a message

- **GIVEN** the transcript composer
- **WHEN** the user submits `\/mode is a command`
- **THEN** `//mode is a command` is posted to the room
