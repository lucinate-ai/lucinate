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

#### Scenario: Two handles address both members

- **GIVEN** a room whose roster is `matt`, `kowal`, `olesno`
- **WHEN** the user posts `@matt @kowal raport?`
- **THEN** both members are the recipients, in the order written
- **AND** the message is sent unchanged
- **AND** the routing mode is not applied
- **AND** the round-robin cursor does not advance

Two handles in one message are a deliberate "both of you" rather than an
ambiguity to resolve: the gateway routes to every member mentioned.

#### Scenario: A handle glued to a longer word is not that member

- **GIVEN** a room with a member whose handle is `matt`
- **WHEN** the user posts `@mattxyz hej`
- **THEN** the parsed handle is `mattxyz`
- **AND** the message is reported as carrying an unknown handle
- **AND** it is not routed to `matt`

#### Scenario: A typo never broadens the message

- **GIVEN** a room whose roster is `matt`, `kowal`
- **WHEN** the user posts `@mat hej`
- **THEN** nothing is resolved to a member
- **AND** the room's routing mode is NOT applied
- **AND** the message is sent as written with the unknown handle reported

#### Scenario: A unicode handle is truncated by the grammar

- **GIVEN** a room with a member whose profile is `młody`
- **WHEN** the user posts `@młody hej`
- **THEN** the parsed handle is `m`
- **AND** no member is resolved, because `HandleFor` never derives a bare ASCII
  prefix as a handle
- **AND** the truncation is reported as an unknown handle rather than silently
  addressing another member

The gateway's mention grammar is ASCII (`@([A-Za-z0-9][A-Za-z0-9._:-]*)`), so a
non-ASCII character ends the handle. The client cannot fix that — the same
grammar decides routing on the gateway — so it mirrors it and makes the
consequence visible.

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

#### Scenario: A gateway that never comes back is not a silent spin

- **GIVEN** a gateway that refuses every redial
- **WHEN** the redials keep failing
- **THEN** the delay grows to its ceiling and stays there, never past it
- **AND** the status line reports the attempt number and the next delay, so a
  permanently unreachable room is visible rather than indistinguishable from a
  working one

#### Scenario: A dead client fails fast instead of hanging

- **GIVEN** a client whose socket was dropped
- **WHEN** a call is made on it
- **THEN** the call returns an error
- **AND** `Client.Err()` reports why the socket died
- **AND** a deliberate `Close()` leaves `Err()` nil, so teardown is not mistaken
  for a fault

### Requirement: Interrupted replies

When the connection drops while a member is answering, the system SHALL keep
what already arrived and mark that reply as interrupted, because the gateway log
never records the failure: no `turn.settled` event is coming, so the transcript
alone would show a reply that is still being written forever.

The interrupted row SHALL show the partial text that did arrive, or state that
no output arrived. The room SHALL return to a ready state — the composer usable,
queued messages drained — and the spinner SHALL stop.

The marker SHALL be cleared once that member answers again, where "answers
again" means an event newer than the interruption. A reconnect's replay of
history the client had already seen SHALL NOT clear it.

#### Scenario: A half-written reply stays visible and marked

- **GIVEN** a member mid-answer with partial text on screen
- **WHEN** the socket drops
- **THEN** the partial text remains on screen
- **AND** the row is marked as interrupted and names the connection loss
- **AND** the spinner stops and the room is ready for input

#### Scenario: An interruption before any output says so

- **GIVEN** a member whose turn started but produced no text
- **WHEN** the socket drops
- **THEN** the marker states that no output arrived before the connection dropped

#### Scenario: A member answering again clears the marker

- **GIVEN** an interrupted reply
- **WHEN** that member posts a new message or starts a new turn
- **THEN** the interrupted marker is cleared

#### Scenario: The reconnect's own replay does not clear the marker

- **GIVEN** an interrupted reply recorded after the newest event the client held
- **WHEN** a successful reconnect refetches that same history
- **THEN** the marker stays, because nothing new was proven about the turn

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
- **AND** no brief is requested from the room

#### Scenario: A window equal to the transcript compacts nothing

- **GIVEN** a transcript of exactly N messages
- **WHEN** the user runs `/compact N`
- **THEN** every message stays verbatim
- **AND** the status line says there is nothing to compact

#### Scenario: A negative window is refused

- **GIVEN** the transcript composer
- **WHEN** the user types `/compact -1`
- **THEN** the command is refused with an error naming the command
- **AND** the room's stored window is unchanged
- **AND** nothing is compacted

`N` is validated rather than clamped: a stored preference is bounded silently
because a hand-edited file must not lock `/compact` out, but a window the user
typed is either honoured or refused — compacting with a different `N` than
asked would discard history the user meant to keep.

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

### Requirement: Per-member model selection

`/model` SHALL select the model one member answers with, in four shapes:
`/model` (pick a member, then a model), `/model @handle` (pick that member's
model, showing its current one), `/model @handle <name>` (switch directly) and
`/model @handle` reporting the current model. An unknown handle, a model name
without a handle, or an empty model name SHALL be refused with an explanation
that names what was wrong, and nothing SHALL be sent to the gateway.

The model list SHALL come from the gateway's own catalogue (the same call the
chat uses), and the picker SHALL filter as the user types.

**Scope:** the change is room-session state (`model_config.room_member_model` on
the member's own room session) and SHALL NOT alter the Hermes profile, its
config, or any file lucinate persists. The gateway is the authority: the client
SHALL show what the gateway's seat reports, and SHALL drop its local overlay when
the socket is re-established.

**How the seat works (verified against a real gateway):** a hosted room's member
runs its turns on a hidden `room_plumbing` session in that member's own profile,
and such a session deliberately rebuilds from the profile's current config on
resume — a model stored on it is ignored unless it is marked as a deliberate
per-room pick. `groups.member_model` writes that marker, and the gateway honours
it on the next turn. The seat therefore survives a client reconnect and dies with
the room session, exactly like the dashboard's own model change on a chat.

**Honesty about the protocol:** the method ships with the gateway, but a gateway
old enough not to have it answers -32601, and the roster's `model_config` is
frozen once a room exists (a re-create with a changed roster is a different
room). The client therefore SHALL probe `groups.capabilities` before offering the
action, SHALL refuse with a message naming the missing method when it is absent,
and SHALL NOT display a model change that did not reach the gateway. When the
gateway rejects the requested model, the previous model SHALL stay in place and
the gateway's own message SHALL be shown.

#### Scenario: A member's model is switched for the session

- **GIVEN** a gateway that advertises the member-model method
- **WHEN** the user runs `/model @matt gpt-5-mini`
- **THEN** the gateway is asked to run that member's turns with `gpt-5-mini`
- **AND** the seat it reports is recorded: which session the pick landed on, and
  whether that session was minted for the pick
- **AND** the roster line shows `@matt (gpt-5-mini)`
- **AND** the status names the session the model was seated on and says the
  member profile is untouched

#### Scenario: Nothing about the model is persisted

- **GIVEN** a successful model switch
- **WHEN** lucinate's stored room preferences are read
- **THEN** they contain no model

#### Scenario: A gateway without the method is told so

- **GIVEN** a gateway whose capabilities do not list the member-model method
- **WHEN** the user confirms a model in the picker
- **THEN** no request is sent
- **AND** the view states that the gateway cannot switch a member's model
- **AND** it names the missing method and the alternative

#### Scenario: A rejected model rolls back

- **GIVEN** a gateway that refuses the requested model
- **WHEN** the switch is attempted
- **THEN** the gateway's message is shown
- **AND** the member's previous model stays in place
- **AND** the roster does not display the refused model

#### Scenario: A reconnect drops the local overlay

- **GIVEN** a switch recorded for the session
- **WHEN** the socket is re-established
- **THEN** the local overlay is discarded
- **AND** what the view shows next is the gateway's roster
- **AND** the seat itself is unaffected, because it lives on the member's room
  session on the gateway side, not in this client

#### Scenario: The picker stays inside the terminal

- **GIVEN** a catalogue longer than the terminal
- **WHEN** the picker is open
- **THEN** the list is windowed with the hidden rows counted
- **AND** the filter line and the key hints stay on screen

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

#### Scenario: The report names the model each member is on

- **GIVEN** a member whose model is known to the session
- **WHEN** the user runs `/cost`
- **THEN** the table carries a MODEL column
- **AND** a member with no model set reads `profile default`

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
