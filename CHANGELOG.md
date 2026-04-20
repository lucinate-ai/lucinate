# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).


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
