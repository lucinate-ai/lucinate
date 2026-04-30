#!/usr/bin/env bash
#
# Sets up the local integration test environment for the Hermes
# backend. Pulls the upstream nousresearch/hermes-agent image from
# Docker Hub and brings up the gateway container with inference
# routed at host-side Ollama via host.docker.internal, so the harness
# stays fully local.
#
# The Docker Hub tag (HERMES_TAG) pins the Hermes version. Bump the
# default below deliberately when tracking a new upstream release.
#
# Steps:
#   1. Check prerequisites (docker, jq, go, ollama, curl).
#   2. Start `ollama serve` if it isn't already running.
#   3. Pull the test model (default qwen2.5:0.5b).
#   4. Warm the model so the first chat turn isn't paying lazy-load
#      cost.
#   5. Seed state/config.yaml from profile.yaml.tmpl with the chosen
#      model so the Hermes entrypoint adopts it on first boot.
#   6. docker compose up -d (pulls the published image on first run).
#   7. Wait on the API-server healthcheck.
#   8. Probe the backend wiring end-to-end via the Go probe.
#   9. Write .env.hermes with the env vars the integration tests read.
#
# Usage:
#   ./test/integration/setup-hermes.sh [--model MODEL] [--tag HERMES_TAG]
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
HERMES_DIR="$SCRIPT_DIR/hermes"
COMPOSE_FILE="$HERMES_DIR/docker-compose.yml"

MODEL="${MODEL:-qwen2.5:0.5b}"
HERMES_TAG="${HERMES_TAG:-v2026.4.23}"
BASE_URL="http://localhost:8642/v1"
HEALTH_URL="http://localhost:8642/health"
API_KEY="lucinate-integration-test"
OLLAMA_HEALTH_URL="http://localhost:11434/api/tags"
OLLAMA_GEN_URL="http://localhost:11434/api/generate"
OLLAMA_BOOT_TIMEOUT=30
HERMES_BOOT_TIMEOUT=180

# --- Parse args -----------------------------------------------------------

while [[ $# -gt 0 ]]; do
    case "$1" in
        --model) MODEL="$2"; shift 2 ;;
        --tag)   HERMES_TAG="$2"; shift 2 ;;
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
check_prereq ollama "Install Ollama: brew install ollama"
check_prereq curl   "curl is part of macOS — check your PATH"
ok "All prerequisites found"

# --- Ollama ---------------------------------------------------------------

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

# --- Seed Hermes state ----------------------------------------------------
#
# The Hermes entrypoint copies cli-config.yaml.example to
# /opt/data/config.yaml only if no config exists. By materialising
# state/config.yaml before `compose up` we control which provider
# Hermes uses without needing to run `hermes setup` interactively.

info "Seeding Hermes profile config (state/config.yaml)"
mkdir -p "$HERMES_DIR/state"
sed "s|__MODEL__|$MODEL|g" "$HERMES_DIR/profile.yaml.tmpl" > "$HERMES_DIR/state/config.yaml"
ok "Wrote state/config.yaml (model=$MODEL)"

# --- Bring up the Hermes container ---------------------------------------

info "Pulling and starting Hermes container (HERMES_TAG=$HERMES_TAG)"
HERMES_UID="$(id -u)" \
HERMES_GID="$(id -g)" \
HERMES_TAG="$HERMES_TAG" \
    docker compose -f "$COMPOSE_FILE" up -d --pull missing

# The upstream Hermes image doesn't ship curl/wget, so we can't run an
# in-container healthcheck. Instead poll the mapped host port — the
# port mapping makes this equivalent for our purposes.
info "Waiting for Hermes API server to respond (up to ${HERMES_BOOT_TIMEOUT}s)"
for i in $(seq 1 "$HERMES_BOOT_TIMEOUT"); do
    if curl -fsS "$HEALTH_URL" >/dev/null 2>&1; then
        ok "Hermes API is responding"
        break
    fi
    if [ "$i" -eq "$HERMES_BOOT_TIMEOUT" ]; then
        warn "Hermes did not respond on $HEALTH_URL in ${HERMES_BOOT_TIMEOUT}s — recent logs:"
        docker compose -f "$COMPOSE_FILE" logs --tail=80 hermes >&2
        fail "Hermes API is not responding"
    fi
    sleep 1
done

# --- Probe ---------------------------------------------------------------

info "Probing Hermes backend wiring"
LUCINATE_HERMES_BASE_URL="$BASE_URL" \
LUCINATE_HERMES_API_KEY="$API_KEY" \
LUCINATE_HERMES_DEFAULT_MODEL="$MODEL" \
    go run "$SCRIPT_DIR/hermes/probe/main.go" 2>&1 | sed 's/^/    /'
ok "Backend probe succeeded"

# --- Write .env.hermes ---------------------------------------------------

info "Writing test .env.hermes"
cat > "$PROJECT_ROOT/.env.hermes" <<EOF
LUCINATE_HERMES_BASE_URL=$BASE_URL
LUCINATE_HERMES_API_KEY=$API_KEY
LUCINATE_HERMES_DEFAULT_MODEL=$MODEL
EOF
ok "Wrote .env.hermes"

# --- Done ----------------------------------------------------------------

echo ""
info "Hermes integration test environment is ready"
echo ""
echo "  Backend:    Hermes (pinned $HERMES_TAG)"
echo "  Base URL:   $BASE_URL"
echo "  Model:      $MODEL (via host-side Ollama)"
echo ""
echo "  Run tests:  make test-integration-hermes"
echo "  Tear down:  make test-integration-hermes-teardown"
echo ""
