# backend-openai — delta for honest-stream-failures

## ADDED Requirements

### Requirement: A successful status is not a successful reply

A `200` response whose body is not an event stream SHALL fail the run with an
error event naming the content type and the first bytes of the body, instead of
emitting an empty final. A response that claims to be an event stream but yields
no parseable data frame at all SHALL likewise fail, naming that fact.

An empty answer that does speak the protocol (a finish-only chunk, a `[DONE]`)
SHALL remain a success: emptiness is a legitimate outcome, silence is not.

#### Scenario: An HTML page instead of a stream

- **WHEN** the endpoint replies `200` with `Content-Type: text/html`
- **THEN** the run emits a `state="error"` chat event naming `text/html`
- **AND** no assistant message is appended to history
- **AND** no `final` event is emitted

#### Scenario: An empty event stream

- **WHEN** the endpoint replies `200` with `Content-Type: text/event-stream` and
  the body carries no parseable data frame
- **THEN** the run emits a `state="error"` chat event stating that no data frames
  arrived

#### Scenario: A protocol-speaking empty answer

- **WHEN** the stream carries a finish-only chunk (valid JSON, empty delta) and
  `[DONE]`
- **THEN** the run completes with a `final` event whose text is empty
- **AND** no error is raised

#### Scenario: `/compact` against a non-streaming endpoint

- **WHEN** the summarisation request receives `200` with a non-event-stream
  content type
- **THEN** the compaction fails with an error naming the content type
- **AND** the previous summary and transcript are left unchanged

### Requirement: A silent stream is bounded

The backend SHALL bound the wait for response headers and the silence inside an
event stream, because a server that accepts the connection and then says nothing
is indistinguishable from a slow answer without a clock.

The header bound SHALL be `httpcommon.ResponseHeaderTimeout` (60 s). The idle
bound SHALL be `streamIdleTimeout` (5 minutes), reset by every frame, and its
expiry SHALL emit an error event that names the silence rather than the raw
read error.

#### Scenario: Headers never arrive

- **WHEN** the server accepts the request and does not send response headers
  within the header bound
- **THEN** the request fails
- **AND** the run emits a `state="error"` chat event

#### Scenario: The stream goes quiet mid-answer

- **WHEN** at least one frame has arrived and then nothing arrives for the idle bound
- **THEN** the run is cancelled
- **AND** the error event states that the endpoint sent nothing for the idle bound
- **AND** no `final` event is emitted
