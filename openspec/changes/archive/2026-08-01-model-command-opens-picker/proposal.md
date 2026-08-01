## Why

`/model` with no argument prints a one-line report and then tells the user to type a
*different* command (`/models`) to actually change anything. Every other picker-backed noun in
the app opens its picker from the singular bare command — `/agents`, `/connections`, `/crons`,
`/routines`, `/sessions`, `/skills` all take you somewhere — so `/model` is the odd one out,
and the near-identical `/model` / `/models` pair is a trap: users reach for the shorter one,
get a dead-end message, and have to retype. Opening the picker makes the bare command do the
thing the user wanted, and the picker already answers "which model am I on?" as a side effect.

## What Changes

- Bare `/model` (no argument) SHALL open the model picker, behaving identically to `/models`.
- **BREAKING** (user-visible behaviour, not API): bare `/model` no longer emits the
  `Model: <id>` system message. The `gateway default` fallback text disappears with it.
- `/model <name>` is untouched — it still fuzzy-matches and patches the session model directly.
- `/models` is kept as an alias so muscle memory and existing docs keep working.
- The model picker SHALL mark the model in use with a `(current)` label on its row, so the
  information the removed report carried survives the change. Today the picker only
  *pre-selects* the active model, which stops telling you anything the moment you type a filter
  or move the cursor.
- Help text (`/help`, `/commands`) and the command reference are reworded accordingly.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `commands`: "Requirement: /model reports and switches the session model" changes — the
  no-argument branch opens the picker instead of reporting, and the requirement is retitled to
  match. The command-reference table entry for `/model` changes with it.
- `chat-ux`: new requirement covering the model picker's `(current)` marker, which is the
  replacement channel for the information the bare-`/model` report used to provide.

## Impact

- `internal/tui/commands.go` — `handleModelCommand()` no-argument branch, `helpBody`.
- `internal/tui/models.go` — `modelDelegate.Render` gains the `(current)` marker;
  `modelPickerModel` needs the current-model reference available at render time (it already
  stores `currentModelID`).
- Tests: `internal/tui/commands_test.go` (the existing bare-`/model` report assertion inverts
  to a picker-message assertion), `internal/tui/models_test.go` (marker rendering).
- Docs: `README.md` / `docs/` command tables that list `/model`.
- No backend, protocol, or persisted-state changes. Behaviour is identical across all three
  backends because it routes through the existing `showModelPickerMsg` path.
