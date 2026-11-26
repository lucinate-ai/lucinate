# chat-ux — delta for asks-phase-2

## ADDED Requirements

### Requirement: A pending ask is visible above the composer

While a turn is blocked on an ask, the chat view SHALL show what is being asked — the kind, the
question, and for an approval the command and directory — in a panel above the composer, without
removing the transcript, the composer or a typed draft.

#### Scenario: An approval is pending

- **WHEN** an `ask` event of kind `approval` arrives
- **THEN** the panel names the command and the working directory and lists the keys that answer it
- **AND** the composer is still visible with whatever the user had typed

#### Scenario: A clarify is pending

- **WHEN** an `ask` event of kind `clarify` arrives with choices
- **THEN** the panel shows the question and the choices numbered from 1
- **AND** when it carries no choices the panel asks for free text

### Requirement: The ask is answered with one key, and the draft survives

The panel SHALL own only its own keys, and only while the composer is empty, so a typed message is
never consumed by an answer.

#### Scenario: Allowing an approval

- **WHEN** the panel shows an approval and the composer is empty and the user presses `y`
- **THEN** the answer `allow-once` is sent, the panel closes, and the transcript records
  `answered: allow once`
- **AND** pressing `a` is available only when the payload offers an always option

#### Scenario: Denying

- **WHEN** the user presses `n` or `esc` on an approval
- **THEN** the answer `deny` is sent and the panel closes

#### Scenario: A draft is not eaten

- **WHEN** the composer holds text and the user presses `y`, `n` or `a`
- **THEN** the draft is unchanged and no answer is sent until the composer is empty

#### Scenario: Answering a clarify

- **WHEN** the panel shows a clarify with choices and the user presses `2`
- **THEN** the second choice is sent as the answer
- **AND** with no choices, typed text plus `enter` is the answer, and `esc` sends an empty answer

### Requirement: The transcript records what was answered

Every answered ask SHALL leave a single line in the transcript naming the answer, so a reader can
tell what the agent was allowed to do without replaying the panel.

#### Scenario: After the answer

- **WHEN** the user answers an ask
- **THEN** one line records it (`answered: allow once`, `answered: deny`, or the clarify answer)
- **AND** the panel is gone and the spinner state is unchanged
