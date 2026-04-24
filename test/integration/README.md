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
5. **Pairs the local device** using this flow:
   - Seeds the gateway token as the device token for the first connect.
   - Connects once to register the device (rejected with `NOT_PAIRED` — expected).
   - Approves the pending device via `openclaw devices approve <requestId>`.
   - Rotates the device token via `openclaw devices rotate` to get a proper credential.
   - Verifies the connection with the new device token.
6. **Writes `.env`** with `OPENCLAW_GATEWAY_URL=http://localhost:18789`.

After setup, the device identity at `~/.openclaw-go/identity/` is paired with
the test gateway. If you had an existing device token (from a production
gateway), it is backed up to `device-token.backup` and restored on teardown.

## What `teardown.sh` does

1. Stops and removes the gateway container.
2. Removes the gateway state directory (`test/integration/state/`).
3. Restores any backed-up device token.

## Docker setup notes

The gateway container runs as the host user (`OPENCLAW_UID`/`OPENCLAW_GID`) so
that the bind-mounted `./state/` directory is writable. Two environment
variables are required for this to work:

- `HOME: /home/node` — the container image doesn't have a `/etc/passwd` entry
  for the host UID, so Docker defaults `$HOME` to `/`. Setting it explicitly
  points the gateway at its expected config directory.
- `npm_config_cache: /tmp/npm-cache` — the image ships with a root-owned
  `/home/node/.npm` cache from the build step. Running as a non-root user
  would make plugin dep installs fail with `EACCES`. Redirecting npm's cache
  to `/tmp` avoids this.

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

The setup script runs the full register → approve → rotate flow. If it fails,
you can step through it manually:

```bash
GW_URL="ws://127.0.0.1:18789/ws"
GW_TOKEN="repclaw-integration-test"
COMPOSE="test/integration/docker-compose.yml"

# 1. List pending devices (JSON structure: {"pending":[...], "paired":[...]})
docker compose -f "$COMPOSE" exec -T gateway \
    openclaw devices list --json --token "$GW_TOKEN" --url "$GW_URL"

# 2. Approve the pending device using its requestId
docker compose -f "$COMPOSE" exec -T gateway \
    openclaw devices approve <requestId> --token "$GW_TOKEN" --url "$GW_URL"

# 3. Rotate to get a proper device token (use the deviceId from step 1)
docker compose -f "$COMPOSE" exec -T gateway \
    openclaw devices rotate --device <deviceId> --role operator \
    --scope operator.read --scope operator.write \
    --scope operator.admin --scope operator.approvals \
    --json --token "$GW_TOKEN" --url "$GW_URL"

# 4. Save the returned .token value
echo -n "<token>" > ~/.openclaw-go/identity/device-token
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
