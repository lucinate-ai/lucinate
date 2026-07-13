# Bootstrap — rapid evaluation of agent orchestration platforms

The bootstrap harness stands an agent orchestration platform up locally, in
Docker, and drops you straight into lucinate chatting with it — for rapid,
hands-on evaluation. The first (and currently only) supported platform is
[OpenClaw](backend_openclaw.md).

It is deliberately **not** a new stack. It builds directly on the
integration-test tooling in [`test/integration/`](../test/integration/README.md):
the gateway standup, provider configuration, and device pairing are the exact
flow the CI-covered `setup-openclaw-<provider>.sh` scripts perform, so a
bootstrapped gateway is byte-for-byte the same environment the integration
tests run against. The bootstrap script only layers an evaluation-oriented
front end on top — one entry point with provider selection, plus a launch of
the interactive TUI against the fresh gateway.

## Quick start

```bash
# Stand up OpenClaw with the zero-cost echo model (no API key, no downloads)
make bootstrap-openclaw-up

# Chat with it from lucinate (interactive TUI)
make bootstrap-openclaw-run

# Tear it all down
make bootstrap-openclaw-down
```

`bootstrap-openclaw-up` prints the launch command when it finishes, so you can
also start the TUI yourself:

```bash
OPENCLAW_GATEWAY_URL=http://localhost:18789 lucinate
```

Setting `OPENCLAW_GATEWAY_URL` routes lucinate at the bootstrapped gateway and
persists it as a saved connection (see the startup decision tree in
[connections.md](connections.md)), so subsequent plain `lucinate` runs offer it
in `/connections`.

## Providers

The default provider is **echo** — a zero-cost canned-reply model that needs no
API key, no model download, and no external service. It is the fastest way to
watch the platform stand up, pair, create agents, and chat end to end when
you're evaluating the *orchestration* rather than a model's output.

For evaluating real model behaviour, pick another provider. They map one-to-one
onto the integration-test provider setups, so the prerequisites and tunables are
identical:

| Provider | Needs | Notes |
|---|---|---|
| `echo` | nothing | **Default.** Instant canned replies. |
| `ollama` | Ollama on the host | Local, Metal-accelerated on macOS. |
| `openrouter` | `OPENROUTER_API_KEY` | Cloud inference; key is prompted if unset. |
| `bedrock` | AWS credentials | Cloud inference via AWS Bedrock. |

Select the provider (and optionally the model) via Make variables:

```bash
make bootstrap-openclaw-up PROVIDER=ollama MODEL=qwen2.5:1.5b
make bootstrap-openclaw-up PROVIDER=openrouter
```

Or call the script directly for the same flags plus `--run` (stand up **and**
launch the TUI in one step):

```bash
./test/integration/bootstrap-openclaw.sh --provider echo --run
./test/integration/bootstrap-openclaw.sh --help
```

See the provider tables in
[`test/integration/README.md`](../test/integration/README.md) for the full model
lists and per-provider prerequisites.

## Status and teardown

```bash
make bootstrap-openclaw-status   # gateway container + /healthz
make bootstrap-openclaw-down     # stop the gateway, clean state, restore identity
```

Teardown is the shared `teardown-openclaw.sh`: it stops the gateway container,
removes the state directory, and restores any backed-up device token. Because
the gateway container, state directory, and device identity are shared with the
integration-test setups, one teardown cleans up whichever brought the gateway
up.

## Relationship to integration tests

Bootstrap and the OpenClaw integration tests share the same gateway container,
host port (`18789`), state directory (`test/integration/state/`), and device
identity (`~/.lucinate/identity/localhost_18789/`). They are therefore
**mutually exclusive** — bring one down before standing the other up. A fresh
`bootstrap-openclaw-up` (like every `setup-openclaw-*` script) wipes the state
directory and re-pairs, so it is safe to run repeatedly, but it will clobber a
running integration-test environment.

The distinction is intent, not mechanism:

- **Integration tests** (`make test-integration-openclaw*`) stand the gateway up
  to run `go test` against it and write `integration.env` to bound the run.
- **Bootstrap** (`make bootstrap-openclaw-*`) stands the same gateway up for a
  human to poke at through the interactive TUI.

## Adding another platform

The harness is named for the platform it targets (`bootstrap-openclaw-*`) so a
second orchestration platform can be added as a sibling — a
`bootstrap-<platform>.sh` that composes that platform's own setup/teardown
scripts (Hermes, for instance, already has `setup-hermes.sh` /
`teardown-hermes.sh`) behind the matching `bootstrap-<platform>-{up,run,down,status}`
Make targets.
