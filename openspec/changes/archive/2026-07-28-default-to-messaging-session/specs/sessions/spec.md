## ADDED Requirements

### Requirement: Messaging-conversation default session

When opening an agent, the system SHALL prefer the external-messaging conversation the user holds with that agent (e.g. a Telegram DM) over the literal `"main"`/`MainKey` default. The conversation SHALL be identified by `backend.PickMessagingSessionKey`, which reads the structured `channel`, `kind`, `origin`, and `deliveryContext` fields returned by `sessions.list` — it SHALL NOT parse the opaque session key. A session qualifies when it is a one-to-one (`direct`) conversation that carries both a messaging channel and a peer identity. When several qualify, the most-recently-updated one SHALL be chosen. This rule SHALL apply at every open-agent site: the agent picker, the in-chat `/agent` switch, and one-shot `lucinate send`.

The lookup SHALL fail safe: on any error, or when no session qualifies, the system SHALL fall back to the existing `MainKey`/`"main"` default and SHALL NOT block opening the agent.

#### Scenario: Telegram DM preferred over a blank main session
- **GIVEN** an agent whose activity is a Telegram direct conversation
- **WHEN** the user opens the agent
- **THEN** `CreateSession` is called with the messaging conversation's key and that conversation is resumed

#### Scenario: Structured fields, not the opaque key
- **WHEN** a candidate session is evaluated for the messaging default
- **THEN** its channel, direct/group kind, and peer identity are read from the `channel`/`kind`/`origin`/`deliveryContext` fields
- **AND** the opaque session key is not parsed

#### Scenario: Most recent messaging conversation wins
- **GIVEN** an agent with more than one messaging direct conversation
- **WHEN** the default session is chosen
- **THEN** the most-recently-updated conversation is used

#### Scenario: Fall back when there is no messaging conversation
- **GIVEN** an agent with no qualifying messaging conversation, or a failing `sessions.list`
- **WHEN** the default session is chosen
- **THEN** the system falls back to `MainKey` for the default agent or the literal `"main"` otherwise
- **AND** opening the agent is not blocked

## MODIFIED Requirements

### Requirement: Session creation and deterministic keys

The system SHALL create a session when the user selects an agent in the agent picker (see the `agents` spec). It SHALL call `client.CreateSession(agentID, key)` and pass the returned `sessionKey` to `newChatModel()`.

When choosing the key, the system SHALL first prefer the agent's external-messaging conversation (see the `Messaging-conversation default session` requirement). When the agent has no such conversation, the key SHALL fall back to the deterministic default: `MainKey` for the connection's default agent, and the literal `"main"` (deterministic for non-default agents, based on agent ID) otherwise — so the same fallback session is restored on restart.

The same default-key rule (messaging preference, then the `MainKey`/`"main"` fallback) SHALL be reused by the one-shot CLI mode: `app.Send` (`lucinate send`), so a scripted dispatch lands on the same conversation as "open the picker, pick the agent, hit enter". See the `one-shot` spec for the full lifecycle.

#### Scenario: Picking an agent creates a session
- **WHEN** the user selects an agent in the agent picker
- **THEN** `client.CreateSession(agentID, key)` is called and the returned `sessionKey` is passed to `newChatModel()`

#### Scenario: Opening an agent prefers its messaging conversation
- **GIVEN** an agent with an external-messaging direct conversation
- **WHEN** the user opens the agent from the picker or the in-chat `/agent` switch
- **THEN** the session key is the messaging conversation's key, so the conversation and its history are resumed

#### Scenario: Deterministic restore on restart
- **GIVEN** an agent with no external-messaging conversation
- **WHEN** its session is created
- **THEN** the key falls back to `MainKey` for the default agent, or the literal `"main"` (derived from the agent ID) otherwise
- **AND** the same session is restored on restart

#### Scenario: One-shot dispatch lands on the same conversation
- **WHEN** `app.Send` (`lucinate send`) creates a session
- **THEN** it prefers the same messaging conversation, otherwise uses `MainKey` for the default agent and the literal `"main"` for any other agent
- **AND** the dispatch lands on the same conversation as picking the agent interactively

### Requirement: Session key override from the chat launcher

`lucinate chat --session <key>` SHALL override the default key at the picker's `CreateSession` site: `AppModel.initialSession` is consumed in the `viewSelect` block of `update`, beating the messaging-conversation default as well as the literal `"main"` and `MainKey`. When the override is present the messaging-conversation lookup SHALL be skipped entirely. The override SHALL be one-shot — cleared once consumed so a follow-up agent pick on the same picker does not keep landing on the original key. See the `chat-launch` spec.

#### Scenario: Explicit session key beats the defaults
- **GIVEN** `lucinate chat --session <key>`
- **WHEN** the `viewSelect` block of `update` consumes `AppModel.initialSession`
- **THEN** the supplied key is used instead of the messaging-conversation default, the literal `"main"`, or `MainKey`

#### Scenario: Override is cleared after use
- **GIVEN** a session-key override has been consumed
- **WHEN** the user picks another agent on the same picker
- **THEN** the override is not reused and the normal default (messaging conversation, else `"main"`/`MainKey`) applies
