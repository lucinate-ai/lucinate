package hermes

import (
	"context"
	"encoding/json"

	"github.com/lucinate-ai/lucinate/internal/backend"
)

// This file holds the optional sub-interface implementations the
// tui_gateway protocol unlocked: StatusBackend (/status), UsageBackend
// (/stats and the header counters), and CompactBackend (/compact).
//
// Per-turn usage does not come from here — message.complete carries it
// inline and the Translator attaches it to the final chat event. The
// RPCs below serve the on-demand commands.

// Status composes the /status payload from structured state: the
// gateway endpoint, auth mode, active model, and — when a session is
// open — its stored id as the active server-side thread. It does not
// call session.status, whose result is a pre-rendered text blob, not
// fields.
func (b *Backend) Status(ctx context.Context, agentID, sessionKey string) (*backend.BackendStatus, error) {
	b.mu.Lock()
	auth := "anonymous"
	if b.token != "" {
		auth = "gateway token"
	}
	model := b.model
	var stored string
	if sess, ok := b.sessions[b.active]; ok {
		stored = sess.stored
	}
	b.mu.Unlock()

	status := &backend.BackendStatus{
		Type:         "hermes",
		Endpoint:     b.opts.BaseURL,
		Auth:         auth,
		DefaultModel: model,
	}
	if stored != "" {
		status.Thread = &backend.ThreadStatus{Active: true, LastResponseID: stored}
	}
	return status, nil
}

// SessionUsage returns the gateway's usage counters for the session —
// the /stats surface. The result is the raw session.usage payload
// ({calls, input, output, total}); the TUI renders what it finds.
func (b *Backend) SessionUsage(ctx context.Context, sessionKey string) (json.RawMessage, error) {
	sess, err := b.resolve(ctx, sessionKey)
	if err != nil {
		return nil, err
	}
	cli, err := b.client()
	if err != nil {
		return nil, err
	}
	var res json.RawMessage
	if err := cli.Call(ctx, "session.usage", map[string]string{"session_id": sess.live}, &res); err != nil {
		return nil, err
	}
	return res, nil
}

// SessionCompact compresses the session's context server-side — the
// /compact surface. History stays coherent afterwards; nothing is
// tracked client-side.
func (b *Backend) SessionCompact(ctx context.Context, sessionKey string) error {
	sess, err := b.resolve(ctx, sessionKey)
	if err != nil {
		return err
	}
	cli, err := b.client()
	if err != nil {
		return err
	}
	return cli.Call(ctx, "session.compress", map[string]string{"session_id": sess.live}, nil)
}
