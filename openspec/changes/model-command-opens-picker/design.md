## Context

See proposal.md — Why.

The relevant code today:

- `handleSlashCommand()` (`internal/tui/commands.go`) matches `/models` in the leading
  `switch` and returns a `showModelPickerMsg{sessionKey, currentModelID}`. `/model` is matched
  *after* the switch by the argument-bearing prefix check (`command == "/model" ||
  strings.HasPrefix(command, "/model ")`) and delegates to `handleModelCommand()`.
- `handleModelCommand()` splits on the first space; the no-argument branch appends a system
  message and returns `(true, nil)`. The named branch is asynchronous — `ModelsList`,
  fuzzy-match, `SessionPatchModel`.
- `AppModel.Update` turns `showModelPickerMsg` into `newModelPickerModel(...)`.
- `modelPickerModel` stores `currentModelID` and, on `modelsLoadedMsg`, pre-selects the
  matching row (`qualifiedModelRef(mc) == currentModelID || mc.ID == currentModelID`), then
  immediately calls `list.SetFilterState(list.Filtering)`.
- `modelDelegate.Render` is a plain `list.ItemDelegate` over `modelItem{model}` — a two-line
  row (name, then `provider · id`) with a highlighted and a non-highlighted branch.

Two constraints shape the approach. First, `modelDelegate` is a zero-field struct and
`modelItem` carries only the `protocol.ModelChoice`, so nothing in the render path currently
knows the current model. Second, the picker enters filter mode on load, so pre-selection is a
weak signal — it is gone as soon as the user types, which is immediately.

## Goals / Non-Goals

**Goals:**

- Bare `/model` reaches the picker through the *same* message and the same `AppModel` branch
  as `/models`, so there is one code path and no second way for the picker to be constructed.
- The picker states which model is in use in a way that survives filtering and cursor movement.

**Non-Goals:**

- Redesigning the picker's layout, filtering, or key bindings.
- Changing `/model <name>`, `qualifiedModelRef`, or anything on the patch path.
- Retiring `/models`. It stays an alias indefinitely; there is no deprecation warning and no
  removal scheduled.
- Adding a `(current)` marker to other pickers (agents, connections, sessions). Consistency
  there is worth having, but it is a separate change with its own specs.

## Decisions

**Route bare `/model` through the existing `/models` switch case rather than duplicating the
message construction in `handleModelCommand()`.** Add `"/model"` to the existing
`case "/models":` label. The switch runs before the prefix check and matches the whole token,
so `/model` is caught there while `/model sonnet` still falls through to the prefix check and
`handleModelCommand()`. The no-argument branch of `handleModelCommand()` is then deleted
outright, not left unreachable.

*Alternative considered:* keep the dispatch as-is and have `handleModelCommand()`'s
no-argument branch return the `showModelPickerMsg`. Rejected — it leaves two constructors for
the same message, and the next person to change the picker's seed data has to find both.

*Consequence worth noting:* `/model` inherits `/models`' navigation gating, which is to say
none. The picker is an overlay that returns to the same chat model, so it neither strands an
active routine nor drops the send queue, and `gateNavigation` is correctly not involved. Bare
`/model` becoming a navigation therefore does not need a new gate.

**Trailing whitespace must reach the picker too.** `handleSlashCommand` lowercases and
trims the input before matching, so `/model` followed by spaces already normalises to
`/model` and hits the switch. This is worth an explicit test because the old code path
handled it in a different place (`SplitN` plus a `TrimSpace` on the second part).

**Carry the current-model reference into the delegate, not into each item.** Give
`modelDelegate` a `currentModelID` field and construct it as
`modelDelegate{currentModelID: currentModelID}` where the picker is built. `modelItem` stays
a pure wrapper over `protocol.ModelChoice`, and the marker decision reuses the same
`qualifiedModelRef(mc) == currentModelID || mc.ID == currentModelID` comparison the
pre-selection uses — one predicate, extracted to a small helper so the two can never drift.

*Alternative considered:* a `current bool` on `modelItem`, set when items are built. Rejected —
`modelItem.FilterValue()` is the filter haystack and the items slice is what the fuzzy filter
reorders; keeping per-render presentation state out of it avoids the marker leaking into
filtering. The delegate is already the place that owns row presentation.

**Render the marker as a dim `(current)` suffix on the title line, in both style branches.**
Appending to the title keeps it visible regardless of highlight state and regardless of how
the list is filtered — the two things pre-selection fails at. Styling it with the existing
`subtle` colour keeps it subordinate to the model name and does not compete with the `>`
cursor or the accent colour on the highlighted row.

*Alternative considered:* a static "Current: X" line in the picker header. Rejected — it
duplicates the header bar, which already shows the active model, and it does not tell the user
*which row* to press Enter on.

**Keep the picker's pre-selection behaviour exactly as it is.** The marker supplements
pre-selection; it does not replace it. Enter on an untouched picker should still pick the
model you already have.

## Risks / Trade-offs

- **[Users who typed bare `/model` for a quick read-out now get a full-screen view they must
  Esc out of.]** → Esc returns to the same chat with nothing lost (the picker is an overlay,
  not a chat rebuild), and the header bar already shows the active model for a genuinely
  glanceable read-out. This is the deliberate trade the proposal makes; the `(current)` marker
  is what keeps the information available rather than merely inferable.
- **[A scripted or documented workflow that greps the chat transcript for the `Model: <id>`
  line breaks.]** → The line was a TUI system message, never a machine-readable interface, and
  `/status` still reports the model in its header block. Nothing is exported or recorded that
  depended on it.
- **[The marker and the pre-selection could drift if someone changes one comparison and not the
  other.]** → They share one helper, and the spec's "matches the marker exactly when it matches
  the pre-selection" wording makes the invariant testable rather than incidental.
- **[Marker text widens the title line and could wrap on a narrow terminal.]** → It is a short
  suffix on a line that already holds a free-form model name, so the failure mode is the
  pre-existing one for long names, not a new one.

## Migration Plan

None needed — no persisted state, no config, no protocol surface. The behaviour change lands
in a single release; rollback is reverting the commit.
