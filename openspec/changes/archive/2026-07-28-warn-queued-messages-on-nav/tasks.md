## 1. Gate logic

- [x] 1.1 Add a `replacesChat bool` parameter to `gateNavigation()` and build the confirm prompt from whichever effects apply — active routine, queued messages, or both joined with "and" (`internal/tui/routines_chat.go`)
- [x] 1.2 Return the navigation command unchanged when neither a routine nor (for a chat-replacing nav) a queued message is at risk
- [x] 1.3 Pass `replacesChat=true` at the chat-replacing call sites (`/agents`, `/agent`, `/agent <name>`, `/connections`) and `false` at the overlay ones (`/crons`, `/sessions`, `/routines`, `/routine <name>`, export) in `internal/tui/commands.go`

## 2. Prompt placement

- [x] 2.1 Add `navConfirmStyle` (accent, bold) in `internal/tui/styles.go`
- [x] 2.2 Add `renderNavConfirm()` and `navConfirmHeight()` in `internal/tui/render.go`, rendering off `pendingNavConfirm`
- [x] 2.3 Insert the nav-confirm band directly above the input in `chatModel.View()` (both `hideInput` and normal branches) in `internal/tui/chat.go`
- [x] 2.4 Reserve the band's height in `applyLayout` (`internal/tui/completion.go`) and reflow (`applyLayout`) both when the prompt is set (`gateNavigation`) and when it is resolved

## 3. Resolution

- [x] 3.1 On `y`, abort any in-flight turn and clear the queue (`cancelTurn`), end the routine if active (`endRoutine`), then dispatch the navigation
- [x] 3.2 On `n`/Esc, dismiss the prompt and report whether a routine or the queue was kept

## 4. Tests and docs

- [x] 4.1 Slash-command tests: warns with queued messages (plural + singular wording), no warning with an empty queue, overlay navigation stays silent (`internal/tui/commands_test.go`)
- [x] 4.2 View placement test: the prompt is the bottommost content region above the input (`internal/tui/render_test.go`)
- [x] 4.3 Update docs — README queueing bullet, navigation-gate rationale in `docs/commands.md`, gate note in `docs/routines.md`
- [x] 4.4 `make build` / `go vet` / `go test ./...` all green
