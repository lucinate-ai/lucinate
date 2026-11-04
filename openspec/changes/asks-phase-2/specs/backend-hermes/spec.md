# backend-hermes — delta for asks-phase-2

## ADDED Requirements

### Requirement: The client advertises that it can answer server requests

The Hermes client SHALL call `client.capabilities {server_requests: true}` after connecting, so the
gateway sends approval and clarify requests to it instead of withdrawing them as unanswerable.

#### Scenario: Connecting

- **WHEN** the client has completed its handshake with the gateway
- **THEN** it has called `client.capabilities` with `{"server_requests": true}` and recorded the
  capabilities the gateway returned

#### Scenario: The advertisement is not sent twice

- **WHEN** the connection is already advertising server requests and a second connect happens on the
  same client
- **THEN** the capability call is made once per connection, not per turn

### Requirement: An approval request waits for the user instead of being withdrawn

When the gateway sends the `approval` server request for an interactive session, the backend SHALL
surface it to the client as an agent event with `stream: "ask"` carrying the JSON-RPC id, the
command, the description and the choices the request offers, and SHALL answer that request with the
user's choice.

#### Scenario: The request reaches the client

- **WHEN** the gateway sends `{"id": 7, "method": "approval", "params": {request_id, command,
  description, choices}}`
- **THEN** the backend emits an agent event with `stream: "ask"`, kind `approval`, the JSON-RPC id,
  the command, the description and the choices
- **AND** no response frame is sent before the user answers

#### Scenario: The user allows once

- **WHEN** the client answers that ask with `once` and the request offered `once`
- **THEN** the backend sends `{"id": 7, "result": {"choice": "once"}}` on the same connection

#### Scenario: The user denies

- **WHEN** the client answers with `deny`
- **THEN** the backend sends `{"id": 7, "result": {"choice": "deny"}}`

#### Scenario: A choice the request did not offer

- **WHEN** the answer names a choice outside the request's `choices`
- **THEN** the backend sends the request's `deny` choice instead of an invalid value

### Requirement: A clarify ask returns the user's answer

When the gateway sends `clarify.request`, the backend SHALL surface it as an `ask` event and SHALL
send the user's answer back through `clarify.respond`.

#### Scenario: Choices are offered

- **WHEN** the payload carries `choices`
- **THEN** the event carries them unchanged so the client can number them
- **AND** the answer the client sends back is the chosen text

#### Scenario: No choices

- **WHEN** the payload carries no `choices`
- **THEN** the client's free text is sent as the answer, and an empty answer is a valid skip

### Requirement: An unanswered ask still cannot hang a turn

An ask that no client answers SHALL be denied after a bounded wait, so a turn never blocks
indefinitely on a question nobody saw.

#### Scenario: The wait expires

- **WHEN** an ask is neither answered nor cancelled within the configured wait
- **THEN** the backend denies it with the same respond RPC and a notice in the transcript
- **AND** an answer that arrives afterwards is dropped and logged rather than sent twice

### Requirement: sudo and secret requests are still declined

`sudo.request` and `secret.request` SHALL keep the automatic decline path, with a notice naming the
kind, because the client does not own a password or secret-input surface.

#### Scenario: A sudo request

- **WHEN** the gateway sends `sudo.request`
- **THEN** the backend answers it with the empty password path and writes the existing notice
- **AND** no `ask` event is emitted for it
