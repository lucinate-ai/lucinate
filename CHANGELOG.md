# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
