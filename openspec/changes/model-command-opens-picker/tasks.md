## 1. Route bare `/model` to the picker

- [ ] 1.1 In `handleSlashCommand()` (`internal/tui/commands.go`), add `"/model"` to the existing `case "/models":` label so both tokens build the same `showModelPickerMsg{sessionKey, currentModelID}`. Update the case comment to name `/models` as the alias.
- [ ] 1.2 Delete the no-argument branch of `handleModelCommand()` — the `len(parts) == 1 || strings.TrimSpace(parts[1]) == ""` block, including the `gateway default` fallback. Leave the named-switch path untouched. Reword the function doc comment: it now handles `/model <name>` only, with bare `/model` handled by the switch.
- [ ] 1.3 Confirm `/model sonnet` still falls through the switch to the `strings.HasPrefix(command, "/model ")` prefix check, and that the now-shorter `handleModelCommand()` compiles without the unused `parts` guard.

## 2. Mark the current model in the picker

- [ ] 2.1 In `internal/tui/models.go`, extract the current-model comparison used by the `modelsLoadedMsg` pre-selection into a helper (e.g. `isCurrentModel(mc protocol.ModelChoice, currentModelID string) bool`) preserving the existing `qualifiedModelRef(mc) == currentModelID || mc.ID == currentModelID` semantics, and an empty `currentModelID` matching nothing.
- [ ] 2.2 Repoint the `modelsLoadedMsg` pre-selection loop at the helper so pre-selection and the marker share one predicate.
- [ ] 2.3 Give `modelDelegate` a `currentModelID` field and pass it in where the delegate is constructed in `newModelPickerModel`.
- [ ] 2.4 In `modelDelegate.Render`, append a `subtle`-styled ` (current)` suffix to the title when the helper matches, in **both** the highlighted and non-highlighted branches.

## 3. Update user-facing text

- [ ] 3.1 In `helpBody` (`internal/tui/commands.go`), replace the three model lines with: `/model — open model picker (filter as you type)`, `/model <name> — switch model directly`, and `/models — alias for /model`. Drop the "show the model in use" line.
- [ ] 3.2 Leave `slashCommands` ordering as-is — `/model` before `/models` still surfaces the shorter form in the ghost hint, which is now the canonical command.
- [ ] 3.3 Update the command table in `README.md` (lines ~171-172) so `/model` is "Open the model picker (filter as you type)" and `/models` is listed as its alias.
- [ ] 3.4 Update `docs/commands.md` where it describes the two switch paths as "`/model <name>` command and the `/models` picker" — the picker is now reached by either token.

## 4. Tests

- [ ] 4.1 Replace `TestSlashCommand_Model_BareReportsCurrentModel` with a test asserting bare `/model` returns a non-nil cmd yielding `showModelPickerMsg`, carrying the model's `sessionKey` and `currentModelID`, and appends no system message.
- [ ] 4.2 Delete `TestSlashCommand_Model_BareWithoutModelReportsDefault` — the `gateway default` text no longer exists. Cover the empty-model case instead by asserting the emitted `showModelPickerMsg` has an empty `currentModelID`.
- [ ] 4.3 Add a test that `/model` and `/models` emit equivalent `showModelPickerMsg` values.
- [ ] 4.4 Add a test that `/model` with trailing whitespace (`"/model   "`) also emits `showModelPickerMsg`.
- [ ] 4.5 Keep `TestSlashCommand_Model_SwitchReturnsCmd` green — `/model sonnet` still returns the async switch cmd.
- [ ] 4.6 In `internal/tui/models_test.go`, add table-driven tests for the marker predicate: qualified match, bare-id match (provider empty), no match, and empty `currentModelID`.
- [ ] 4.7 Add a render test over `modelDelegate.Render` asserting `(current)` appears on the matching row for both the highlighted and non-highlighted branches, and on no row when `currentModelID` is empty or absent from the list.

## 5. Verify

- [ ] 5.1 Run `make fmt` and `make test`.
- [ ] 5.2 Run `openspec validate model-command-opens-picker`.
- [ ] 5.3 Manually drive the TUI: `/model` opens the picker with the active model marked and pre-selected; typing a filter keeps the marker visible; Esc returns to chat with the send queue and any active routine intact.
