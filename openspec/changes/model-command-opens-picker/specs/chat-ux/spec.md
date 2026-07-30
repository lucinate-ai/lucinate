## ADDED Requirements

### Requirement: Model picker marks the model in use

The model picker SHALL label the row of the model currently in use for the active session
with a `(current)` marker, rendered alongside the model's display name. The marker SHALL be
derived from the same reference used to pre-select that row — the qualified
`<provider>/<id>` form, falling back to the bare id for backends that leave `provider` empty
— so a model matches the marker exactly when it matches the pre-selection.

The marker is the picker's answer to "which model am I on?", the question bare `/model`
previously answered with a system message. Pre-selection alone is insufficient: it conveys
the answer only while the cursor has not moved and no filter has been typed, and the picker
opens straight into filter-typing mode. The marker SHALL therefore remain visible while the
list is filtered and while the cursor is on another row, and SHALL be rendered for both the
highlighted and non-highlighted row styles.

When the session has no model set — an empty current-model reference — no row SHALL be
marked. When the current model is not present in the list the gateway returned, no row SHALL
be marked; the picker SHALL NOT synthesise an entry for it.

#### Scenario: Current model is marked
- **GIVEN** the session is using a model that appears in the picker's list
- **WHEN** the picker is opened
- **THEN** that model's row shows a `(current)` marker

#### Scenario: Marker survives filtering and cursor movement
- **GIVEN** the picker is open with the current model marked
- **WHEN** the user types a filter that still matches the current model, or moves the cursor to another row
- **THEN** the current model's row still shows the `(current)` marker

#### Scenario: No model set
- **GIVEN** the session has no model set
- **WHEN** the picker is opened
- **THEN** no row shows a `(current)` marker

#### Scenario: Current model absent from the list
- **GIVEN** the session's model is not among the models the gateway returned
- **WHEN** the picker is opened
- **THEN** no row shows a `(current)` marker and no entry is synthesised for it
