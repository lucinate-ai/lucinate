#!/usr/bin/env bash
#
# Shared OpenClaw device-pairing helper for the integration setup scripts.
#
# pair_device() pairs the local device with the test gateway via the same flow
# a real user follows from lucinate's "Pairing required" screen: the client
# connects requesting its default operator scopes (including operator.admin),
# the gateway records a pending request, and the gateway owner approves it with
# `openclaw devices approve`. The approved device is issued a durable token
# carrying the full operator scope set — so the tests exercise the same scopes
# as a real install, including operator.admin (which the gateway requires for
# agent create/delete).
#
# This replaces the earlier setup-code bootstrap flow, which could only obtain
# a bounded read/write/approvals token and so forced tests to override
# OPENCLAW_OPERATOR_SCOPES below admin.
#
# The sourcing script must define these variables and the info/ok/warn/fail
# logging helpers before calling pair_device:
#   SCRIPT_DIR COMPOSE_FILE IDENTITY_DIR BACKUP_FILE GATEWAY_URL GATEWAY_TOKEN

# Runs pair/main.go with the default operator scopes. OPENCLAW_OPERATOR_SCOPES
# is explicitly cleared so a stray value in the caller's environment can't bound
# the requested scopes below admin.
_pair_connect() {
    env -u OPENCLAW_OPERATOR_SCOPES OPENCLAW_GATEWAY_URL="$GATEWAY_URL" \
        go run "$SCRIPT_DIR/pair/main.go"
}

pair_device() {
    info "Pairing device with test gateway (register + owner approval)"
    mkdir -p "$IDENTITY_DIR"

    # Back up any existing device token (e.g. a real production credential) so
    # teardown can restore it, then seed the shared gateway token as the device
    # token. The seed authorises the first connect so the gateway records a
    # pending pairing request — an empty token is rejected as "token missing".
    if [ -f "$IDENTITY_DIR/device-token" ]; then
        cp "$IDENTITY_DIR/device-token" "$BACKUP_FILE"
        ok "Backed up existing device token"
    fi
    printf '%s' "$GATEWAY_TOKEN" > "$IDENTITY_DIR/device-token"

    # Register: connect requesting the default operator scopes (incl. admin).
    # The device is not approved yet, so the gateway rejects the connect with
    # NOT_PAIRED while recording a pending request carrying those scopes. The
    # rejection is expected; we drive the approval next.
    info "Registering device (requesting default operator scopes incl. admin)"
    if _pair_connect >/dev/null 2>&1; then
        ok "Device already approved — no pending request to confirm"
    else
        # Exactly one pending request exists right after a fresh register.
        # Approve it as the gateway owner: `openclaw devices approve` with no
        # --url/--token uses the gateway's local owner authority — the same a
        # self-hoster has on the gateway host — which can grant operator.admin.
        # (Passing --url/--token instead connects as an unprivileged WS client
        # and deadlocks: an unapproved operator cannot approve itself.)
        local req
        req="$(docker compose -f "$COMPOSE_FILE" exec -T gateway \
            openclaw devices list --json 2>/dev/null | jq -r '.pending[0].requestId // empty')"
        [ -n "$req" ] || fail "No pending pairing request to approve. Logs: docker compose -f $COMPOSE_FILE logs gateway"
        info "Approving device as gateway owner ($req)"
        # The CLI prints a WS-connect failure then approves via a local
        # fallback; its exit code is unreliable, so success is verified by the
        # reconnect below rather than by this command's status.
        docker compose -f "$COMPOSE_FILE" exec -T gateway \
            openclaw devices approve "$req" 2>&1 | sed 's/^/    /' || true
    fi

    # Verify: the approved device must now connect on its own and persist a
    # durable operator token (replacing the seeded gateway token).
    info "Verifying connection"
    if _pair_connect 2>&1 | sed 's/^/    /'; then
        ok "Device paired and verified (operator scopes incl. admin)"
    else
        fail "Connection failed after approval. Logs: docker compose -f $COMPOSE_FILE logs gateway"
    fi
    [ -s "$IDENTITY_DIR/device-token" ] || fail "No device token persisted after pairing."
}

# Writes integration.env for the test runs. No OPENCLAW_OPERATOR_SCOPES override
# is needed now that the device is paired with the full default scope set.
write_integration_env() {
    cat > "$SCRIPT_DIR/integration.env" <<EOF
OPENCLAW_GATEWAY_URL=$GATEWAY_URL
EOF
    ok "Wrote test/integration/integration.env with OPENCLAW_GATEWAY_URL=$GATEWAY_URL"
}
