## ADDED Requirements

### Requirement: Profile selection on a multiplexed gateway

A Hermes connection SHALL address every profile the gateway serves, not one profile fixed at
connect time. `ListAgents` SHALL report the gateway's own profile roster (`profiles.list`),
using the profile name as the agent identifier, and SHALL fall back to the single synthetic
agent only when the gateway does not answer `profiles.list`.

`CreateSession` SHALL send the selected profile as `profile` on `session.create` and
`session.resume`; `SessionsList` SHALL pass it on `session.list`. A session's identity in the
client SHALL be profile-qualified, so two sessions with the same stored id in different
profiles cannot collide.

The profile list SHALL be read from the gateway, never from the local filesystem: a profile the
gateway does not serve SHALL NOT appear as an agent, because the gateway would reject it.

#### Scenario: The agent picker lists the gateway's profiles
- **GIVEN** a connected gateway serving profiles `default`, `kowal` and `librarian`
- **WHEN** the TUI lists agents
- **THEN** three agents are reported, one per profile, each named by the profile and carrying its model
- **AND** the agent identifier is the profile name

#### Scenario: A new session runs as the selected profile
- **GIVEN** the user selects the `librarian` profile
- **WHEN** the backend creates a session
- **THEN** `session.create` carries `profile: "librarian"`
- **AND** the session's reported profile is `librarian`

#### Scenario: Sessions are listed per profile
- **GIVEN** the `librarian` profile is selected
- **WHEN** the session browser loads
- **THEN** `session.list` carries `profile: "librarian"`
- **AND** only that profile's sessions are shown

#### Scenario: A gateway without a profile roster keeps the synthetic agent
- **GIVEN** a gateway that answers `-32601` to `profiles.list`
- **WHEN** the backend lists agents
- **THEN** the single synthetic `hermes` agent is reported, exactly as before

### Requirement: Session model switching

`SessionPatchModel` SHALL switch the session model through the gateway's own command
dispatcher (`slash.exec`, command `/model <reference>`), because the gateway has no dedicated
model-patch RPC. The reference SHALL be the qualified `<provider>/<id>` form the model picker
produced. The gateway's answer SHALL be surfaced verbatim when it refuses, and a successful
switch SHALL update the session's reported model.

#### Scenario: A model switch reaches the gateway
- **GIVEN** a live session on model `deepseek/deepseek-v4.1-flash`
- **WHEN** the user selects `xiaomi/mimo-v2.6-flash`
- **THEN** `slash.exec` is called with `/model xiaomi/mimo-v2.6-flash`
- **AND** the header model becomes `xiaomi/mimo-v2.6-flash`

#### Scenario: The gateway's refusal is shown
- **GIVEN** a model reference the gateway rejects
- **WHEN** the switch is attempted
- **THEN** the gateway's message is returned to the user unchanged
- **AND** the previously reported model stays in place

## MODIFIED Requirements

### Requirement: Capabilities reporting

`Backend.Capabilities()` SHALL report the following capability set:

- `AuthRecovery: AuthRecoveryAPIKey` — the API-key auth-recovery modal, relabelled for Hermes
  as the "gateway token" prompt. The stored secret slot holds the gateway session token
  (`HERMES_DASHBOARD_SESSION_TOKEN`).
- `AgentManagement: false` — profiles are configured server-side; the TUI's "new agent" and
  "delete agent" affordances stay hidden, and `CreateAgent`/`DeleteAgent` return clear errors.
  Listing profiles as agents SHALL NOT imply they can be created or deleted from the client.
- `GatewayStatus: true` — `/status` reports the gateway endpoint, auth mode, model, and active
  session.

The backend SHALL implement the `StatusBackend`, `UsageBackend`, and `CompactBackend`
sub-interfaces. It SHALL NOT implement `ExecBackend`, `ThinkingBackend`, or `CronBackend`, so
`!!`, `/think`, and `/crons` render a "not available on this connection" system message.

#### Scenario: Agent management stays disabled while profiles are listed
- **GIVEN** a gateway serving five profiles
- **WHEN** the TUI reads `Capabilities()`
- **THEN** `AgentManagement` is `false`
- **AND** the five profiles are still listed as agents

#### Scenario: Usage and compact are supported
- **WHEN** the user invokes `/stats` or `/compact` on a Hermes connection
- **THEN** the command succeeds because the backend implements `UsageBackend` and `CompactBackend`

#### Scenario: Out-of-phase commands are rejected with a message
- **WHEN** the user invokes `/think`, `/crons`, or `!!` on a Hermes connection
- **THEN** a "not available on this connection" system message is rendered
