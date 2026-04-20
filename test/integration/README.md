# Integration Testing

End-to-end integration tests that run repclaw against a real OpenClaw gateway
backed by a local Ollama model. The LLM runs on the host (Metal-accelerated),
while the gateway runs in Docker.

```
┌──────────────── macOS host ────────────────┐
│                                            │
│  repclaw (go test -tags integration)       │
│      │                                     │
│      ▼ ws://localhost:18789                │
│  ┌──────────────────────┐                  │
│  │ OpenClaw gateway     │ ← Docker         │
│  │ (ghcr.io/openclaw/…) │                  │
│  └──────────┬───────────┘                  │
│             │ http://host.docker.internal  │
│             ▼                              │
│  Ollama (Metal-accelerated on host)        │
│  Model: qwen2.5:3b                         │
└────────────────────────────────────────────┘
```

## Prerequisites

| Requirement       | Install                          |
|-------------------|----------------------------------|
| Docker Desktop    | https://docker.com/products/docker-desktop/ |
| Ollama            | `brew install ollama`            |
| jq                | `brew install jq`                |
| Go 1.22+          | https://go.dev/dl/               |

## Quick start

```bash
# 1. Set up the environment (pulls model, starts gateway, pairs device)
make test-integration-setup

# 2. Run integration tests
make test-integration

# 3. Tear down when done
make test-integration-teardown
```

## What `setup.sh` does

1. **Checks prerequisites** — Docker, Ollama, jq, Go.
2. **Starts Ollama** if it isn't already running.
3. **Pulls the test model** (`qwen2.5:3b` by default — fast on Apple Silicon).
4. **Starts the OpenClaw gateway** in Docker via `docker-compose.yml`.
5. **Pairs the local device** — runs a small Go helper that connects to the
   gateway, triggering a device pairing request, then auto-approves it.
6. **Writes `.env`** with `OPENCLAW_GATEWAY_URL=http://localhost:18789`.

After setup, the device identity at `~/.openclaw-go/identity/` is paired with
the test gateway. If you had an existing device token (from a production
gateway), it is backed up to `device-token.backup` and restored on teardown.

## What `teardown.sh` does

1. Stops and removes the gateway container.
2. Restores any backed-up device token.

## Choosing a different model

```bash
MODEL=llama3.2:3b make test-integration-setup
```

Stick to 3B models on a MacBook Air. The `qwen2.5:3b` default has decent
tool-calling support; `llama3.2:3b` is slightly faster if you don't need that.

## Running specific tests

```bash
# Run a single integration test
go test -tags integration -run TestQueueOrdering ./internal/tui/ -v -count=1
```

## Troubleshooting

### Gateway won't start

```bash
docker compose -f test/integration/docker-compose.yml logs gateway
```

### Device pairing fails

The setup script auto-approves the first pending device. If it times out:

```bash
# Check pending devices manually
docker compose -f test/integration/docker-compose.yml exec gateway \
    openclaw device list --pending

# Approve manually
docker compose -f test/integration/docker-compose.yml exec gateway \
    openclaw device approve <device-id>
```

### Ollama not reachable from Docker

Ensure Docker Desktop has "Allow the default Docker socket to be used" enabled
and that `host.docker.internal` resolves correctly:

```bash
docker run --rm --add-host=host.docker.internal:host-gateway alpine \
    wget -qO- http://host.docker.internal:11434/api/tags
```

### Tests are slow or non-deterministic

This is expected with a real LLM. Integration tests should assert on protocol
structure (message ordering, event types, session lifecycle) rather than on
response content. Keep LLM-dependent assertions to smoke-test level.
