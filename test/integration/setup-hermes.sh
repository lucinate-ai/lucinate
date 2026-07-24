#!/usr/bin/env bash
#
# Sets up the local integration test environment for the Hermes
# backend. Pulls the upstream nousresearch/hermes-agent image from
# Docker Hub and brings up the `hermes dashboard` gateway container
# (tui_gateway JSON-RPC over WebSocket at /api/ws), with inference
# routed at a host-side model server via host.docker.internal so the
# harness stays fully local.
#
# Two inference legs:
#   default   — host-side Ollama (realistic streaming latency)
#   --echo    — host-side echomodel stub (zero cost, deterministic;
#               also serves the scripted tool-call tests)
#
# The Docker Hub tag (HERMES_TAG) pins the Hermes version. Bump the
# default below deliberately when tracking a new upstream release —
# it is one of two synced pins (this script + docker-compose.yml).
#
# Steps:
#   1. Check prerequisites (docker, jq, go, curl; ollama on that leg).
#   2. Start the chosen model server (echomodel or ollama) and warm it.
#   3. Seed state/config.yaml from profile.yaml.tmpl with the chosen
#      model + base URL so the Hermes entrypoint adopts it on first boot.
#   4. docker compose up -d (pulls the published image on first run).
#   5. Wait for the dashboard gateway to respond on the published port.
#   6. Probe the backend wiring end-to-end via the Go probe (WS
#      handshake + session round-trip).
#   7. Write .env.hermes with the env vars the integration tests read.
#
# Usage:
#   ./test/integration/setup-hermes.sh [--model MODEL] [--tag HERMES_TAG] [--echo]
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
HERMES_DIR="$SCRIPT_DIR/hermes"
COMPOSE_FILE="$HERMES_DIR/docker-compose.yml"
ECHO_PID_FILE="$HERMES_DIR/echomodel.pid"
ECHO_PORT="${ECHO_PORT:-18080}"

MODEL="${MODEL:-qwen2.5:0.5b}"
HERMES_TAG="${HERMES_TAG:-v2026.7.7.2}"
BASE_URL="http://localhost:19119"
TOKEN="lucinate"
OLLAMA_HEALTH_URL="http://localhost:11434/api/tags"
OLLAMA_GEN_URL="http://localhost:11434/api/generate"
OLLAMA_BOOT_TIMEOUT=30
HERMES_BOOT_TIMEOUT=180
ECHO_LEG=0

# --- Parse args -----------------------------------------------------------

while [[ $# -gt 0 ]]; do
    case "$1" in
        --model) MODEL="$2"; shift 2 ;;
        --tag)   HERMES_TAG="$2"; shift 2 ;;
        --echo)  ECHO_LEG=1; shift ;;
        *) echo "Unknown option: $1"; exit 1 ;;
    esac
done

# --- Helpers --------------------------------------------------------------

info()  { printf "\033[1;34m==>\033[0m %s\n" "$*"; }
ok()    { printf "\033[1;32m  ✓\033[0m %s\n" "$*"; }
warn()  { printf "\033[1;33m  !\033[0m %s\n" "$*"; }
fail()  { printf "\033[1;31m  ✗\033[0m %s\n" "$*" >&2; exit 1; }

check_prereq() {
    command -v "$1" &>/dev/null || fail "$1 is not installed. $2"
}

# --- Prerequisites --------------------------------------------------------

info "Checking prerequisites"
check_prereq docker "Install Docker Desktop: https://docker.com/products/docker-desktop/"
check_prereq jq     "Install jq: brew install jq"
check_prereq go     "Install Go: https://go.dev/dl/"
check_prereq curl   "curl is part of macOS — check your PATH"
if [ "$ECHO_LEG" -eq 0 ]; then
    check_prereq ollama "Install Ollama: brew install ollama (or use --echo for the stub leg)"
fi
ok "All prerequisites found"

# --- Model server ---------------------------------------------------------

if [ "$ECHO_LEG" -eq 1 ]; then
    MODEL="echo"
    MODEL_BASE_URL="http://host.docker.internal:${ECHO_PORT}/v1"

    info "Starting echomodel on :$ECHO_PORT"
    if [ -f "$ECHO_PID_FILE" ] && kill -0 "$(cat "$ECHO_PID_FILE")" 2>/dev/null; then
        kill "$(cat "$ECHO_PID_FILE")" 2>/dev/null || true
    fi
    ECHO_BIN="$HERMES_DIR/echomodel.bin"
    go build -o "$ECHO_BIN" "$SCRIPT_DIR/echomodel/"
    ECHO_ADDR=":$ECHO_PORT" nohup "$ECHO_BIN" >"$HERMES_DIR/echomodel.log" 2>&1 &
    echo $! > "$ECHO_PID_FILE"
    for i in $(seq 1 10); do
        if curl -fsS "http://localhost:$ECHO_PORT/v1/models" &>/dev/null; then break; fi
        [ "$i" -eq 10 ] && fail "echomodel did not become ready"
        sleep 1
    done
    ok "echomodel ready (pid $(cat "$ECHO_PID_FILE"))"
else
    MODEL_BASE_URL="http://host.docker.internal:11434/v1"

    info "Checking Ollama"
    if ! curl -fsS "$OLLAMA_HEALTH_URL" &>/dev/null; then
        warn "Ollama is not running — starting it"
        ollama serve &>/dev/null &
        for i in $(seq 1 "$OLLAMA_BOOT_TIMEOUT"); do
            if curl -fsS "$OLLAMA_HEALTH_URL" &>/dev/null; then
                break
            fi
            if [ "$i" -eq "$OLLAMA_BOOT_TIMEOUT" ]; then
                fail "Ollama failed to start within ${OLLAMA_BOOT_TIMEOUT}s"
            fi
            sleep 1
        done
        ok "Ollama started"
    else
        ok "Ollama is running"
    fi

    info "Pulling model: $MODEL"
    ollama pull "$MODEL"
    ok "Model ready"

    info "Warming model"
    curl -fsS "$OLLAMA_GEN_URL" \
        -H "Content-Type: application/json" \
        -d "{\"model\":\"$MODEL\",\"prompt\":\"hi\",\"stream\":false}" \
        >/dev/null
    ok "Model warm"
fi

# --- Seed Hermes state ----------------------------------------------------
#
# The Hermes entrypoint copies cli-config.yaml.example to
# /opt/data/config.yaml only if no config exists. By materialising
# state/config.yaml before `compose up` we control which provider
# Hermes uses without needing to run `hermes setup` interactively.

info "Seeding Hermes profile config (state/config.yaml)"
mkdir -p "$HERMES_DIR/state"
sed -e "s|__MODEL__|$MODEL|g" -e "s|__BASE_URL__|$MODEL_BASE_URL|g" \
    "$HERMES_DIR/profile.yaml.tmpl" > "$HERMES_DIR/state/config.yaml"
ok "Wrote state/config.yaml (model=$MODEL, base_url=$MODEL_BASE_URL)"

# --- Bring up the Hermes container ---------------------------------------

info "Pulling and starting Hermes container (HERMES_TAG=$HERMES_TAG)"
HERMES_TAG="$HERMES_TAG" \
    docker compose -f "$COMPOSE_FILE" up -d --pull missing

# The upstream Hermes image doesn't ship curl/wget, so we can't run an
# in-container healthcheck. Poll the published dashboard port instead —
# the SPA answering means the gateway (same process) is up.
info "Waiting for the Hermes gateway to respond (up to ${HERMES_BOOT_TIMEOUT}s)"
for i in $(seq 1 "$HERMES_BOOT_TIMEOUT"); do
    if curl -fsS "$BASE_URL/" >/dev/null 2>&1; then
        ok "Hermes gateway is responding"
        break
    fi
    if [ "$i" -eq "$HERMES_BOOT_TIMEOUT" ]; then
        warn "Hermes did not respond on $BASE_URL in ${HERMES_BOOT_TIMEOUT}s — recent logs:"
        docker compose -f "$COMPOSE_FILE" logs --tail=80 hermes >&2
        fail "Hermes gateway is not responding"
    fi
    sleep 1
done

# --- Probe ---------------------------------------------------------------

info "Probing Hermes backend wiring (WS handshake + session round-trip)"
LUCINATE_HERMES_BASE_URL="$BASE_URL" \
LUCINATE_HERMES_TOKEN="$TOKEN" \
    go run "$SCRIPT_DIR/hermes/probe/main.go" 2>&1 | sed 's/^/    /'
ok "Backend probe succeeded"

# --- Write .env.hermes ---------------------------------------------------

info "Writing test .env.hermes"
cat > "$PROJECT_ROOT/.env.hermes" <<ENVEOF
LUCINATE_HERMES_BASE_URL=$BASE_URL
LUCINATE_HERMES_TOKEN=$TOKEN
LUCINATE_HERMES_SCRIPTED=$ECHO_LEG
ENVEOF
ok "Wrote .env.hermes"

# --- Done ----------------------------------------------------------------

echo ""
info "Hermes integration test environment is ready"
echo ""
echo "  Backend:    Hermes gateway (pinned $HERMES_TAG)"
echo "  Base URL:   $BASE_URL"
if [ "$ECHO_LEG" -eq 1 ]; then
    echo "  Model:      echomodel stub (no API charge; scripted tool-calls active)"
else
    echo "  Model:      $MODEL (via host-side Ollama)"
fi
echo ""
echo "  Run tests:  make test-integration-hermes"
echo "  Tear down:  make test-integration-hermes-teardown"
echo ""
