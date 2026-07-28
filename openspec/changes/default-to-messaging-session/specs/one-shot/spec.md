## MODIFIED Requirements

### Requirement: Default session selection

The default-key rule SHALL be shared with the TUI agent picker so `lucinate send` and "select agent → open" land on the same gateway-side session. When `--session` is unset, `app.Send` SHALL first prefer the external-messaging conversation the user holds with the agent (via `backend.PickMessagingSessionKey`; see the `sessions` spec, `Messaging-conversation default session`), and only when there is none SHALL it fall back to the shared default:

| Agent | `--session` unset, no messaging conversation → key passed to `CreateSession` |
|-------|---------------------------------------------------|
| Default agent (`agent.ID == list.DefaultID`) | `list.MainKey` |
| Any other agent                              | `"main"` (literal)                                |

If the same key already exists on the gateway, `CreateSession` SHALL resume it; if not, the gateway SHALL provision one. From the script's point of view, `lucinate send --connection X --agent Y "hello"` repeats into the same conversation forever unless `--session` is supplied.

The literal-`"main"` fallback for non-default agents matches what the TUI passes when the user opens a non-default agent that has no messaging conversation. Backends that don't keep server-side session state (OpenAI, Hermes) SHALL ignore the key shape and route by `agentID` regardless, and expose no messaging conversation so the fallback always applies.

#### Scenario: Messaging conversation preferred when present
- **GIVEN** the chosen agent has an external-messaging direct conversation and `--session` is unset
- **WHEN** the session key is defaulted
- **THEN** the messaging conversation's key is passed to `CreateSession`

#### Scenario: Default agent uses the main-session key
- **GIVEN** the chosen agent is the default agent (`agent.ID == list.DefaultID`), has no messaging conversation, and `--session` is unset
- **WHEN** the session key is defaulted
- **THEN** `list.MainKey` is passed to `CreateSession`

#### Scenario: Non-default agent falls back to literal "main"
- **GIVEN** the chosen agent is not the default agent, has no messaging conversation, and `--session` is unset
- **WHEN** the session key is defaulted
- **THEN** the literal string `"main"` is passed to `CreateSession`, matching what the TUI passes when the user opens a non-default agent with no messaging conversation

#### Scenario: Repeated sends land in the same conversation
- **GIVEN** `--session` is not supplied
- **WHEN** `lucinate send --connection X --agent Y "hello"` is run repeatedly
- **THEN** `CreateSession` resumes the existing key if present or the gateway provisions one, so the turns repeat into the same conversation
