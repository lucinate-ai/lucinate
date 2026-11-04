# rooms — delta for rooms-truthful-rows

## ADDED Requirements

### Requirement: A turn row names the member, not the gateway

Every transcript row that reports what a member did SHALL name that member by handle, resolving
the member from the event payload against the room's roster. A row SHALL NOT show the gateway's
own installation identifier as if it were a member.

#### Scenario: A settled turn

- **WHEN** the gateway emits `turn.settled` with `actor {kind: gateway, id: "install:…"}` and
  `payload {member_id: "m1"}`, and `m1` is `alojz` on the roster
- **THEN** the transcript row reads `@alojz finished`
- **AND** no row in the transcript shows `install:<hash>` as a member name

#### Scenario: A cancelled turn

- **WHEN** the user runs `/stop` and the gateway emits `turn.cancelled` for `m1` with the reason
  `member turn cancelled`
- **THEN** the transcript reads `@alojz — przerwano: member turn cancelled`

#### Scenario: A member the roster cannot place

- **WHEN** a turn event names a `member_id` and `member_index` that the current roster does not
  contain
- **THEN** the row falls back to the actor's own label rather than inventing a handle

### Requirement: The evidence row appears only when a write was claimed

The client SHALL mark a member reply as a write claim without a receipt only when that reply both
uses a write marker and names a deliverable — a file with a deliverable extension, or an explicit
first-person delivery. A reply that merely mentions a path SHALL be treated as discussion, with no
evidence row. A reply that denies writing SHALL NOT be marked.

#### Scenario: A reply that only quotes a path

- **WHEN** a member answers `… widzę ją na head 9e534c81ba1a z jednym nieśledzonym plikiem
  docs/rooms-live-check.md` and no `HANDOFF` receipt
- **THEN** the reply carries no evidence row

#### Scenario: A real claim with no receipt

- **WHEN** a member writes `zapisałem raport w report.md` and provides no `HANDOFF` receipt
- **THEN** the reply is marked `deklaracja zapisu bez potwierdzenia — brak HANDOFF`

#### Scenario: A denial is not a claim

- **WHEN** a member writes `nie zapisałem pliku report.md` and provides no receipt
- **THEN** the reply carries no evidence row

#### Scenario: Receipts keep their verdicts

- **WHEN** a reply carries `HANDOFF <path> bytes=<n>` and the file on disk matches
- **THEN** the reply is marked verified (unchanged), and a wrong byte count stays failed (unchanged)
