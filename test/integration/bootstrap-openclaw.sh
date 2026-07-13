#!/usr/bin/env bash
#
# Bootstrap a local OpenClaw agent-orchestration platform for rapid,
# interactive evaluation — stand the gateway up in Docker, pair this
# machine's device identity, and (optionally) drop straight into lucinate
# chatting with it.
#
# This is the eval counterpart to the integration-test setup scripts. It
# *composes* them rather than reimplementing anything: the gateway standup
# and device pairing are exactly the flow the CI-covered
# `setup-openclaw-<provider>.sh` scripts perform, so a bootstrapped gateway
# is byte-for-byte the same environment the tests run against. The only
# thing layered on top is the evaluation convenience — one entry point with
# provider selection, and a launch of the TUI against the fresh gateway.
#
# Tear down with `make bootstrap-openclaw-down` (an alias for the shared
# `teardown-openclaw.sh`): the gateway container, state directory and device
# identity are shared with the integration-test setups, so one teardown
# script cleans up whichever brought the gateway up.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

GATEWAY_URL="http://localhost:18789"

PROVIDER="echo"
RUN_AFTER=0

# --- Helpers ---------------------------------------------------------------

info()  { printf "\033[1;34m==>\033[0m %s\n" "$*"; }
ok()    { printf "\033[1;32m  ✓\033[0m %s\n" "$*"; }
warn()  { printf "\033[1;33m  !\033[0m %s\n" "$*"; }
fail()  { printf "\033[1;31m  ✗\033[0m %s\n" "$*" >&2; exit 1; }

usage() {
    cat <<'EOF'
Bootstrap a local OpenClaw platform for rapid evaluation.

Usage:
  ./test/integration/bootstrap-openclaw.sh [--provider P] [--model M] [--run]

Options:
  --provider P   Inference provider to seed. One of:
                   echo        Zero-cost canned-reply model. No API key, no
                               model download, no external service — the
                               fastest way to see the platform stand up,
                               pair, create agents and chat. (default)
                   ollama      Local Ollama model (host-side).
                   openrouter  Cloud inference via OpenRouter
                               (needs OPENROUTER_API_KEY).
                   bedrock     Cloud inference via AWS Bedrock
                               (needs AWS credentials).
  --model M      Model to seed (provider-specific; echo ignores it).
                 Forwarded to the provider setup script via $MODEL.
  --run          After the gateway is up, launch lucinate against it
                 (interactive TUI). Omit to just print the launch command.
  -h, --help     Show this help and exit.

Environment passthrough (same variables the setup scripts honour):
  OPENCLAW_IMAGE      Gateway image tag (default in docker-compose.yml).
  MODEL               Provider model (overridden by --model).
  OPENROUTER_API_KEY  Required for --provider openrouter (or prompted).
  AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY / AWS_REGION  For bedrock.

Tear down:
  make bootstrap-openclaw-down
EOF
}

# --- Parse args ------------------------------------------------------------

while [[ $# -gt 0 ]]; do
    case "$1" in
        --provider) PROVIDER="${2:-}"; shift 2 ;;
        --model)    export MODEL="${2:-}"; shift 2 ;;
        --run)      RUN_AFTER=1; shift ;;
        -h|--help)  usage; exit 0 ;;
        *) echo "Unknown option: $1" >&2; usage >&2; exit 1 ;;
    esac
done

case "$PROVIDER" in
    echo|ollama|openrouter|bedrock) ;;
    *) fail "Unknown provider '$PROVIDER'. Choose one of: echo, ollama, openrouter, bedrock." ;;
esac
SETUP_SCRIPT="$SCRIPT_DIR/setup-openclaw-$PROVIDER.sh"
[ -x "$SETUP_SCRIPT" ] || fail "Setup script not found or not executable: $SETUP_SCRIPT"

# --- Stand up the gateway (reuse the tested setup flow) --------------------

info "Bootstrapping OpenClaw for evaluation (provider: $PROVIDER)"
"$SETUP_SCRIPT"

# --- Evaluation layer ------------------------------------------------------

echo ""
info "Ready to evaluate — OpenClaw is up at $GATEWAY_URL"
echo ""
echo "  Launch lucinate against it (interactive):"
echo "      make bootstrap-openclaw-run"
echo "  which is shorthand for:"
echo "      OPENCLAW_GATEWAY_URL=$GATEWAY_URL go run ."
echo "  or, from anywhere lucinate is installed:"
echo "      OPENCLAW_GATEWAY_URL=$GATEWAY_URL lucinate"
echo ""
echo "  Setting OPENCLAW_GATEWAY_URL makes lucinate connect to the"
echo "  bootstrapped gateway directly and persist it as a saved connection,"
echo "  so later plain 'lucinate' runs offer it in /connections."
echo ""
echo "  Tear down when done:"
echo "      make bootstrap-openclaw-down"
echo ""

if [ "$RUN_AFTER" -eq 1 ]; then
    info "Launching lucinate against $GATEWAY_URL"
    cd "$REPO_ROOT"
    exec env OPENCLAW_GATEWAY_URL="$GATEWAY_URL" go run .
fi
