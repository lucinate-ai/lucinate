# one-shot — delta for honest-stream-failures

## ADDED Requirements

### Requirement: The reply wait is bounded

`send` and `ask` SHALL bound how long they wait for the assistant's reply, since
a scripted caller cannot recover from a process that never returns. The bound
SHALL be settable with `--timeout`, default to ten minutes, and treat `0` as
"wait forever". Exceeding it SHALL fail with an error naming the bound, and SHALL
NOT write a partial reply to the output stream.

`SIGINT` and `SIGTERM` SHALL cancel the in-flight run through the backend's
context rather than killing the process mid-write.

#### Scenario: A silent backend under the default bound

- **WHEN** the run is accepted and no reply event arrives within `--timeout`
- **THEN** `Send` returns an error naming the timeout
- **AND** nothing is written to the output stream

#### Scenario: The user interrupts

- **WHEN** the user sends `SIGINT` while `Send` waits for a reply
- **THEN** the backend's run is cancelled
- **AND** `Send` returns the cancellation error rather than the process dying
  mid-write

#### Scenario: Unbounded when asked

- **WHEN** `--timeout 0` is supplied
- **THEN** no deadline is applied to the reply wait
