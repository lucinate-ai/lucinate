# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.22.0] - 2026-07-13
### Added
- feat(tui): add filtering to the agent picker (#179)
- feat(tui): bare `/model` now reports current model instead of error (#175)
- feat(tui): rename Config screen to Settings, rebind shortcut to "s" (#180)
- feat(tui): split notifications - info at top, errors at the bottom (#176)

### Fixed
- fix(tui): send qualified provider/model ref when switching models (#178)
- fix(tui): stop streamed text repeating after each tool call (#174)
- fix: freeze form inputs while create/auth mutations are in flight (#173)

### Other
- change(tui): default delete-agent files toggle to "keep files" (#177)

## [1.21.0] - 2026-07-13
### Added
- feat: default mouse capture on with in-app transcript selection (#171)

### Changed
- build(deps): bump charm.land/bubbles/v2 from 2.1.0 to 2.1.1
- build(deps): bump charm.land/bubbletea/v2 from 2.0.7 to 2.0.8
- build(deps): bump charm.land/lipgloss/v2 from 2.0.4 to 2.0.5
- build(deps): bump golang.org/x/mod from 0.37.0 to 0.38.0
- build(deps): bump golang.org/x/net from 0.39.0 to 0.55.0

### Fixed
- fix: re-anchor chat viewport when the completion menu opens or closes (#168)

## [1.20.0] - 2026-06-30
### Added
- feat: add OnExit callback for host-driven quit

### Changed
- ci: pin v3 integration leg to 2026.5.7 for admin pairing
- ci: run integration tests on push to main
- docs(ci): document gateway version pins in integration config
- test(integration): pair openclaw with admin scopes and add openrouter variant

## [1.19.0] - 2026-06-25
### Added
- feat: add ask subcommand with configurable defaults

### Fixed
- fix(tui): restore originating chat on esc from crons list

## [1.18.2] - 2026-06-25
### Fixed
- fix(client): guard disconnected-gateway sends against panic and hang
- fix(openclaw): keep a stray .env from blocking agent creation

## [1.18.1] - 2026-06-22
### Fixed
- fix(tui): accept string content in chat history turns (#159)

## [1.18.0] - 2026-06-21
### Added
- feat(tui): broaden /status to all backends with versions and local stats (#156)

### Changed
- build(deps): bump actions/checkout from 6 to 7 in the actions group

### Fixed
- fix(tui): surface gateway chat errors instead of hanging

## [1.17.4] - 2026-06-16
### Changed
- build: narrow release commit pattern
- build: release against the tested go.mod instead of an upstream SDK checkout (#154)

## [1.17.3] - 2026-06-16
### Changed
- test(integration): smoke-test gateway connections across versions in CI

### Fixed
- fix(openclaw): support gateway protocol v4 and setup-code onboarding

## [1.17.2] - 2026-06-16
### Changed
- chore(deps): bump charm.land/bubbletea/v2 from 2.0.6 to 2.0.7
- chore(deps): bump charm.land/glamour/v2 from 2.0.0 to 2.0.1
- chore(deps): bump github.com/sahilm/fuzzy from 0.1.2 to 0.1.3
- chore(deps): bump golang.org/x/mod from 0.36.0 to 0.37.0
- docs: add brew trust step to Homebrew installation instructions (#146)

### Fixed
- fix(tui): stop capturing mouse so terminal selection works (#14)

## [1.17.1] - 2026-05-25
### Fixed
- fix(tui): stop forms clipping fields on short terminals

## [1.17.0] - 2026-05-16
### Added
- feat(logging): add configurable slog logger (#137)
- feat(tui): add /header command to customise chat header colour (#135)

## [1.16.2] - 2026-05-16
### Fixed
- fix(tui): stop scrolling chat on up/down, add bash-style history recall (#141)

## [1.16.1] - 2026-05-12
### Fixed
- fix(tui): scroll to bottom on input submit (#134)

## [1.16.0] - 2026-05-10
### Added
- feat(routines): add Duplicate action with collision-aware naming
- feat(tui): add transcript record/export and improve routine form
- feat(tui): cue manual-mode routines with call-to-action in the status row
- feat(tui): cycle routine mode with shift+tab and surface ctrl+s to save

### Changed
- style(tui): colour the routine status row by mode
- test: split bedrock and ollama setup
- test: substitute model name in integration test config
- test: use same model placeholder as hermes config

### Fixed
- fix(tui): end manual routines on their final reply

## [1.15.0] - 2026-05-10
### Added
- feat: add multi-step routines (#128)

### Fixed
- fix(tui): always resync after final via a generation merge boundary

## [1.14.1] - 2026-05-09
### Changed
- ci: clear deprecation warnings in release workflows (#126)

## [1.14.0] - 2026-05-09
### Added
- feat(tui): show slash-command menu and extend Tab to longest common prefix

## [1.13.0] - 2026-05-09
### Added
- feat(cli): add help command for lucinate and subcommands
- feat(tui): freeze list pickers during gateway round-trip

### Changed
- build(deps): bump actions/create-github-app-token from 2 to 3
- build(deps): bump github.com/sahilm/fuzzy from 0.1.1 to 0.1.2
- build(deps): bump golang.org/x/mod from 0.35.0 to 0.36.0

## [1.12.0] - 2026-05-09
### Added
- feat(crons): add duplicate action to cron job list
- feat(openai): implement local session compaction via summarisation

### Changed
- docs: refresh cron detail keybindings and transcript esc note

### Fixed
- fix(crons): send agentTurn payload as message and wrap form errors
- fix(tui): rebind cron run-now to ! and show triggering ack
- fix(tui): return to cron detail on esc from transcript view

## [1.11.0] - 2026-05-06
### Added
- feat(cli): add short flag aliases for chat and send subcommands

## [1.10.0] - 2026-05-06
### Added
- feat: allow embedders to enable bright cursor

### Changed
- test: shorten integration test token

### Fixed
- fix(test): wipe gateway state before integration setup

## [1.9.0] - 2026-05-06
### Added
- feat(tui): surface delivery errors in cron run transcripts

### Fixed
- fix(tui): rebuild cron transcripts from the run log

## [1.8.0] - 2026-05-04
### Added
- feat: add 'lucinate chat' subcommand for pre-navigated TUI launch

## [1.7.0] - 2026-05-03
### Added
- feat: add one-shot CLI mode via `lucinate send` (#108)

## [1.6.0] - 2026-05-03
### Added
- feat(tui): add /agent and /models commands with autocomplete
- feat(tui): show per-session context usage in chat header

### Fixed
- fix(tui): drop tool events scoped to other sessions (#105)

## [1.5.0] - 2026-05-03
### Added
- feat(tui): support mid-message skill autocomplete and envelope format (#104)

## [1.4.0] - 2026-05-03
### Added
- feat(tui): add fuzzy-filter model picker to /model

### Fixed
- fix(tui): correct inverted Files mode hint on delete-agent screen

## [1.3.1] - 2026-05-03
### Changed
- docs: align pairing and auth-recovery docs with TUI modal flow
- test: use a smaller model for integration testing

### Fixed
- fix(client): re-dial after first-time pairing and time-bound session creation

## [1.3.0] - 2026-05-03
### Added
- feat: check for updates on startup (#98)

## [1.2.0] - 2026-05-02
### Added
- feat(app): add DisableExitKeys for embedded hosts (#99)

## [1.1.0] - 2026-05-02
### Added
- feat(install): add PowerShell install script for Windows
- feat(tui): auto-suggest localhost OpenClaw gateway URL in connection form
- feat(tui): show placeholder while conversation history loads

### Changed
- docs: add PowerShell install one-liner to README

## [1.0.1] - 2026-05-02
### Changed
- build: publish Homebrew formula to lucinate-ai/homebrew-tap on release

## [1.0.0] - 2026-05-02
### Changed
- build: mark stable release

### Fixed
- fix(tui): wrap pairing-required error to terminal width
- feat(tui): show pairing instructions on NOT_PAIRED connect

## [0.28.0] - 2026-05-02
### Added
- feat(tui): display tool call status as inline cards

## [0.27.0] - 2026-05-01
### Added
- feat: delete agents from picker with type-to-confirm

### Changed
- build: replace deprecated goreleaser archive format fields
- docs: improve OpenAI support description
- test: cover factory dispatch, hermes prompt log, and client helpers

### Fixed
- fix(tui): preserve chat scroll during streaming and gate bell on focus

## [0.26.0] - 2026-04-30
### Added
- feat(app): add SetDataDir programmatic seam for embedder data root (#85)
- feat: add Hermes Agent backend over /v1/responses

### Changed
- refactor: surface a connection-managed embedder API for non-CLI hosts (#84)

## [0.25.0] - 2026-04-29
### Added
- feat(tui): show last-message timestamp on the history separator

## [0.24.0] - 2026-04-29
### Added
- feat(tui): list, inspect, and manage gateway cron jobs
- feat: make connect timeout configurable for slow backends

## [0.23.0] - 2026-04-28
### Added
- feat(tui): add Ollama preset to the connections form
- feat(tui): expose connections action from the agent picker
- feat(tui): show the active connection in the agent and session views
- feat: add OpenAI-compatible backend alongside OpenClaw

### Changed
- docs: document connections picker
- docs: document connections, backends, and OpenAI agent storage
- docs: simplify getting started
- docs: split per-backend docs and cover audit-flagged test gaps
- refactor: move skill catalog injection from chat layer into the backends
- test: add OpenAI integration test suite against host Ollama
- test: cover connections form and auth modals end-to-end

### Fixed
- fix(openai): seed IDENTITY.md with the agent's chosen name
- fix(tui): hide workspace field on the create-agent form for local backends
- fix: propagate focus changes when tabbing through the new-connection form
- fix: update startup smoke test for BackendFactory rename

## [0.22.0] - 2026-04-28
### Added
- feat: add connections picker for managing gateways

### Changed
- refactor: extract reusable run and embedder hooks for native clients (#73)

## [0.21.0] - 2026-04-27
### Added
- feat: reconnect to gateway after disconnection

## [0.20.0] - 2026-04-27
### Added
- feat(tui): stack message prefix above body in narrow terminals

## [0.19.0] - 2026-04-26
### Added
- feat: interactive auth recovery on gateway connect

### Changed
- refactor: isolate device identity per gateway endpoint (#64)

### Fixed
- fix: skip empty skills when building catalog block
- fix: update identity path in integration test scripts for per-endpoint isolation
- fix: update stale module path in integration pair test

## [0.18.0] - 2026-04-24
### Added
- feat: add /status command for gateway health view (#52)
- feat: add cancel turn via Escape key and /cancel command (#59)

### Changed
- refactor: remove --history-limit CLI flag (#61)
- test: add integration test stack with Docker, Ollama, and Bedrock

### Fixed
- fix: resolve session key via gateway for default agent (#62)

## [0.17.1] - 2026-04-24
### Changed
- docs: add logo
- docs: improve description

### Fixed
- fix: ignore chat events from other sessions (#55)

## [0.17.0] - 2026-04-24
### Added
- feat: style pending messages as dim italic shadows (#54)

### Changed
- chore: apply project name
- docs: add maintainer documentation for all major subsystems (#53)

### Fixed
- fix: prevent placeholder 'm' appearing in new agent name field (#57)
- fix: prevent queued messages being dropped on early gateway ack

## [0.16.0] - 2026-04-24
### Added
- feat: add thinking level controls via /think command
- feat: show thinking spinner immediately on message send

## [0.15.0] - 2026-04-21
### Added
- feat: add local shell commands with ! prefix, use !! for remote

## [0.14.0] - 2026-04-21
### Added
- feat: make message history depth configurable

### Changed
- docs: add missing slash commands to README
- docs: replace roadmap with issue tracker
- docs: split keybindings into separate agent and chat sections
- test: improve TUI test coverage for config, sessions, events, and commands

## [0.13.0] - 2026-04-20
### Added
- feat: add /compact, /reset commands with confirmation and config preferences view

## [0.12.2] - 2026-04-20
### Fixed
- fix: correct long input text wrapping in chat textarea

## [0.12.1] - 2026-04-20
### Fixed
- fix: enable mouse wheel scrolling in chat history (#44)

## [0.12.0] - 2026-04-20
### Added
- feat: add session browser via /sessions command (#9)

### Changed
- test: add rendering tests using teatest/v2 (#43)

### Fixed
- fix: return to correct parent view when pressing escape from chat

## [0.11.1] - 2026-04-20
### Changed
- refactor: use gateway agents.create API instead of config patching

## [0.11.0] - 2026-04-20
### Added
- feat: animate streaming response placeholder (#40)
- feat: recall and edit queued messages with up-arrow (#39)
- feat: use /agents to return to agent picker (#41)

### Changed
- build: consume openclaw-go as a normal module dependency (#37)
- docs: add terminal chat highlight to README
- docs: improve readability of highlights
- docs: link to openclaw/openclaw instead of nerve project

### Fixed
- fix: disable ReportAllKeysAsEscapeCodes to fix shifted punctuation input
- fix: drop shift+enter references and use alt+enter only for newline

## [0.10.3] - 2026-04-20
### Fixed
- fix: use KeyPressMsg for escape key handling in bubbletea v2

## [0.10.2] - 2026-04-20
### Changed
- docs: add UX gap items to roadmap
- docs: tighten README positioning

### Fixed
- fix: drop bootstrap token, use device pairing only for setup
- fix: support alt+enter for newline via bubbletea v2 upgrade

## [0.10.0] - 2026-04-20
### Added
- feat: add agent creation from the agent selector

### Changed
- docs: update roadmap

### Fixed
- fix: use deterministic session key for non-default agents

## [0.9.1] - 2026-04-19
### Fixed
- fix: align chat message columns
- fix: normalise chat message spacing

## [0.9.0] - 2026-04-19
### Added
- feat: add local agent skills support (agentskills.io spec)

### Changed
- docs: update roadmap
- refactor: shorten help text

### Fixed
- fix: strip gateway-rewritten System prefixes and align message prefixes

## [0.8.1] - 2026-04-19
### Fixed
- fix: preserve command execution messages after queue drain

## [0.8.0] - 2026-04-19
### Added
- feat: add message queueing to allow sending while awaiting a response

### Fixed
- fix: use two-phase exec approval and handle gateway-resolved denials

## [0.7.0] - 2026-04-19
### Added
- feat: add markdown rendering for assistant messages

## [0.6.0] - 2026-04-19
### Added
- feat: add remote command execution with !prefix

### Changed
- docs: update AGENTS.md to be tool-agnostic and use new project name
- refactor: align test files with source modules
- refactor: split chat.go into focused modules

## [0.5.0] - 2026-04-19
### Added
- feat: add /model command, /stats table, and show model in header
- feat: show session token usage and cost in header bar

### Changed
- docs: add commands section and update key bindings in readme

## [0.4.0] - 2026-04-18
### Added
- feat: add --version flag
- feat: add tab autocompletion for slash commands

## [0.3.0] - 2026-04-18
### Added
- feat: add slash commands, viewport scrolling, and long message wrapping

### Fixed
- fix: render slash command output as system messages, not agent replies

## [0.2.0] - 2026-04-18
### Added
- feat: add ctrl+w word deletion and simplify ESC handling
- feat: auto-select agent when only one is configured

### Changed
- docs: add getting started section with gateway setup instructions

## [0.1.2] - 2026-04-18
### Changed
- ci: skip GoReleaser validation to allow dirty state from SDK checkout

## [0.1.1] - 2026-04-18
### Changed
- ci: allow dirty git state in GoReleaser for SDK replace directive

## [0.1.0] - 2026-04-18
### Added
- feat: initial commit

## [0.10.1] - 2026-04-20
### Fixed
- fix: support alt+enter for newline via bubbletea v2 upgrade

## [0.10.0] - 2026-04-20
### Added
- feat: add agent creation from the agent selector

### Changed
- docs: update roadmap

### Fixed
- fix: use deterministic session key for non-default agents

## [0.9.1] - 2026-04-19
### Fixed
- fix: align chat message columns
- fix: normalise chat message spacing

## [0.9.0] - 2026-04-19
### Added
- feat: add local agent skills support (agentskills.io spec)

### Changed
- docs: update roadmap
- refactor: shorten help text

### Fixed
- fix: strip gateway-rewritten System prefixes and align message prefixes

## [0.8.1] - 2026-04-19
### Fixed
- fix: preserve command execution messages after queue drain

## [0.8.0] - 2026-04-19
### Added
- feat: add message queueing to allow sending while awaiting a response

### Fixed
- fix: use two-phase exec approval and handle gateway-resolved denials

## [0.7.0] - 2026-04-19
### Added
- feat: add markdown rendering for assistant messages

## [0.6.0] - 2026-04-19
### Added
- feat: add remote command execution with !prefix

### Changed
- docs: update AGENTS.md to be tool-agnostic and use new project name
- refactor: align test files with source modules
- refactor: split chat.go into focused modules

## [0.5.0] - 2026-04-19
### Added
- feat: add /model command, /stats table, and show model in header
- feat: show session token usage and cost in header bar

### Changed
- docs: add commands section and update key bindings in readme

## [0.4.0] - 2026-04-18
### Added
- feat: add --version flag
- feat: add tab autocompletion for slash commands

## [0.3.0] - 2026-04-18
### Added
- feat: add slash commands, viewport scrolling, and long message wrapping

### Fixed
- fix: render slash command output as system messages, not agent replies

## [0.2.0] - 2026-04-18
### Added
- feat: add ctrl+w word deletion and simplify ESC handling
- feat: auto-select agent when only one is configured

### Changed
- docs: add getting started section with gateway setup instructions

## [0.1.2] - 2026-04-18
### Changed
- ci: skip GoReleaser validation to allow dirty state from SDK checkout

## [0.1.1] - 2026-04-18
### Changed
- ci: allow dirty git state in GoReleaser for SDK replace directive

## [0.1.0] - 2026-04-18
### Other
- initial commit
