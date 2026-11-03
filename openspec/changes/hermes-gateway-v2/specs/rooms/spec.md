## RENAMED Requirements

- FROM: `### Requirement: Streaming member replies`
- TO: `### Requirement: Live member activity`

- FROM: `### Requirement: Per-member model selection`
- TO: `### Requirement: Member model follows the profile`

## ADDED Requirements

### Requirement: Member approvals, retries, and cancellation

The system SHALL render the gateway's `pending_actions` and `blocked` state, and SHALL let the
user resolve them without leaving the transcript:

- `/approve @handle once|deny` SHALL resolve the single pending approval for that member,
  sending the exact `member_id`, `task_id`, `execution_generation`, and `request_id` the state
  reported.
- `/retry` SHALL retry the room's indeterminate task when the state offers one.
- `/stop` SHALL cancel queued and running member turns.

An approval with no pending action, an ambiguous one (more than one pending action for the
member), or a stale one SHALL be refused with the reason named, and nothing SHALL be sent.
The CLI SHALL expose the same three verbs as `rooms approve`, `rooms retry`, and `rooms rename`.

#### Scenario: A blocked member is visible
- **GIVEN** a room whose state reports one pending approval for `@kowal`
- **WHEN** the transcript renders
- **THEN** a blocked banner names `@kowal`
- **AND** the hint names `/approve @kowal once|deny`

#### Scenario: An approval is resolved exactly
- **GIVEN** a pending approval with `task_id` and `execution_generation`
- **WHEN** the user runs `/approve @kowal once`
- **THEN** `groups.approve` is called with those coordinates and `choice: "once"`
- **AND** the banner clears once the next state poll reports no pending action

#### Scenario: An approval with nothing pending is refused
- **GIVEN** a room with no pending actions
- **WHEN** the user runs `/approve @kowal once`
- **THEN** the command is refused with an explanation
- **AND** nothing is sent to the gateway

#### Scenario: A turn set is cancelled from the transcript
- **GIVEN** a room with member turns in flight
- **WHEN** the user runs `/stop`
- **THEN** `groups.stop` is called
- **AND** the status line reports how many turns were cancelled
- **AND** the composer is usable again

### Requirement: Project context path normalisation

The project path a room is seated for SHALL be normalised to the local filesystem before it is
used or shown. A Windows path produced by a WSL-hosted Orca terminal (`\\wsl.localhost\<distro>\…`,
`\\wsl$\<distro>\…`) SHALL become the equivalent POSIX path; a Windows drive path SHALL resolve
to `/mnt/<drive>/…` only when that directory exists; any other backslash path SHALL be treated
as unknown. When the resolved path is empty or is not an existing directory, the room's shared
directory SHALL fall back to the process working directory rather than creating a directory
from the raw string.

The `worktree Orca:` selector SHALL be passed through unchanged — it is an Orca argument, not a
filesystem path.

#### Scenario: A WSL worktree path becomes POSIX
- **GIVEN** `ORCA_WORKTREE_ID` ends with `\\wsl.localhost\Ubuntu\home\hermes\projects\app`
- **WHEN** a room is seated
- **THEN** the project path is `/home/hermes/projects/app`
- **AND** the shared directory is `/home/hermes/projects/app/.rooms/<room-id>`

#### Scenario: An unresolvable Windows path falls back
- **GIVEN** `ORCA_WORKTREE_ID` names `C:\Users\me\project` on a Linux host without `/mnt/c`
- **WHEN** a room is seated
- **THEN** the project path is empty
- **AND** the shared directory is created under the working directory

#### Scenario: No directory is created from a backslash path
- **GIVEN** a project path containing `\`
- **WHEN** the room directory is ensured
- **THEN** no directory whose name contains a backslash is created

## MODIFIED Requirements

### Requirement: Live member activity

While a member is answering, the system SHALL render that member's live row from the gateway's
own activity surface: the room's state poll reports `member_activity` (member id and task
status) and the renderer SHALL show one working row per active member, prefixed by a braille
spinner that advances every 120 ms. Text deltas SHALL be used when a gateway emits them, and
its absence SHALL NOT be presented as a fault or as an empty answer.

The spinner SHALL run only while a member is active, and the row SHALL be removed the moment
the state poll no longer reports that member — including when the turn ends with no text,
because an empty answer is a real outcome and a spinner left against a silent member reads as a
hung room.

#### Scenario: An answering member shows a working row
- **GIVEN** a state poll reporting `member_activity` for `@matt`
- **WHEN** the transcript is rendered
- **THEN** a row names `@matt`
- **AND** it carries a braille spinner glyph

#### Scenario: The spinner advances between frames
- **GIVEN** a member mid-answer
- **WHEN** the spinner ticks
- **THEN** the rendered glyph differs from the previous frame
- **AND** a further tick is scheduled

#### Scenario: A member that stops answering loses its row
- **GIVEN** a member whose `member_activity` entry is gone from the next state poll
- **WHEN** the transcript is rendered
- **THEN** no working row for that member is shown

#### Scenario: A gateway that emits no deltas still shows the finished answer
- **GIVEN** a gateway that writes only `message.member` to the room log
- **WHEN** the turn settles
- **THEN** the finished text is rendered in the transcript
- **AND** the missing deltas are not reported as an error

### Requirement: Member model follows the profile

A room member SHALL run the model its Hermes profile is configured with. The system SHALL NOT
offer a per-member model seat unless the gateway advertises the method that implements it; a
gateway without it SHALL be reported plainly, and nothing SHALL be sent.

When the gateway does advertise the method, `/model @handle <name>` SHALL behave as before:
session-scoped, capability-probed, never persisted by the client and never written to the
profile.

#### Scenario: A gateway without the seat says so
- **GIVEN** a gateway whose `groups.capabilities` do not list `groups.member_model`
- **WHEN** the user runs `/model @matt`
- **THEN** the view states that the gateway cannot switch a member's model
- **AND** it names the profile as the place the model comes from
- **AND** no request is sent

#### Scenario: The roster shows the profile's model
- **GIVEN** a room whose member `@matt` runs the profile model `deepseek/deepseek-v4.1-flash`
- **WHEN** the roster line renders
- **THEN** it shows that model

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
- **THEN** the notice lists `/mode`, `/moderator`, `/export`, `/compact`, `/find`, `/cost`,
  `/header`, `/model`, `/restart`, `/approve`, `/retry` and `/stop`

#### Scenario: An escaped slash is a message
- **GIVEN** the transcript composer
- **WHEN** the user submits `\/mode is a command`
- **THEN** `//mode is a command` is posted to the room
