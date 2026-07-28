package backend

import (
	"context"
	"encoding/json"
	"log/slog"
)

// PickMessagingSessionKey returns the key of the external-messaging
// conversation — e.g. a Telegram DM — that the user actually talks to
// the given agent on, or "" when the agent has no such session.
//
// When an agent is reached through a messaging channel, the useful
// session to open is that conversation, not a blank "main" bucket. The
// gateway mints those session keys itself (they look like
// agent:main:telegram:direct:123456789) and they are opaque to us, so
// we do not parse them. Instead we read the structured channel/kind/
// origin fields the gateway returns from sessions.list — the same
// fields its own conversation views key off. When several messaging
// conversations exist for one agent we cannot tell which human is "the
// user", so the most-recently-updated one wins.
//
// Any error — a transport failure, a backend that has no messaging
// sessions, an unexpected shape — yields "" so the caller falls back to
// its existing default. Opening an agent must never be blocked by this
// lookup.
func PickMessagingSessionKey(ctx context.Context, b Backend, agentID string) string {
	raw, err := b.SessionsList(ctx, agentID)
	if err != nil {
		slog.Debug("messaging session pick: sessions.list failed", "agent", agentID, "err", err)
		return ""
	}
	return pickMessagingKeyFromRaw(raw)
}

// pickMessagingKeyFromRaw is the pure selection over a sessions.list
// response, split out so it can be tested without a live backend.
func pickMessagingKeyFromRaw(raw []byte) string {
	var resp messagingSessionsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		slog.Debug("messaging session pick: parse failed", "err", err)
		return ""
	}
	best := ""
	var bestUpdated int64 = -1
	for _, row := range resp.Sessions {
		if !row.isMessagingConversation() {
			continue
		}
		if row.UpdatedAt > bestUpdated {
			bestUpdated = row.UpdatedAt
			best = row.Key
		}
	}
	return best
}

// messagingSessionsResponse is the sessions.list envelope. The gateway
// returns many more fields per session than we read here.
type messagingSessionsResponse struct {
	Sessions []messagingSessionRow `json:"sessions"`
}

// messagingSessionRow mirrors the subset of a gateway session row that
// identifies a messaging conversation without decoding the opaque
// session key. The channel, peer, and direct/group signals each appear
// in a few places depending on how the gateway recorded the session;
// the accessor methods below apply the same precedence the gateway uses
// (deliveryContext, then the last-* fields, then origin).
type messagingSessionRow struct {
	Key         string `json:"key"`
	Kind        string `json:"kind"`
	Channel     string `json:"channel"`
	LastChannel string `json:"lastChannel"`
	LastTo      string `json:"lastTo"`
	UpdatedAt   int64  `json:"updatedAt"`
	Origin      struct {
		Provider           string `json:"provider"`
		ChatType           string `json:"chatType"`
		From               string `json:"from"`
		NativeDirectUserID string `json:"nativeDirectUserId"`
	} `json:"origin"`
	DeliveryContext struct {
		Channel string `json:"channel"`
		To      string `json:"to"`
	} `json:"deliveryContext"`
}

// channelName is the messaging channel the session belongs to (e.g.
// "telegram"), or "" for a local/main session that never arrived over a
// channel.
func (r messagingSessionRow) channelName() string {
	return firstNonEmpty(r.DeliveryContext.Channel, r.LastChannel, r.Channel, r.Origin.Provider)
}

// peer is the channel-local identity on the other end of the
// conversation (e.g. the Telegram user id). A local/main session has no
// peer.
func (r messagingSessionRow) peer() string {
	return firstNonEmpty(r.DeliveryContext.To, r.LastTo, r.Origin.From, r.Origin.NativeDirectUserID)
}

// isDirect reports whether the session is a one-to-one conversation
// rather than a group or the agent's global bucket.
func (r messagingSessionRow) isDirect() bool {
	return r.Kind == "direct" || r.Origin.ChatType == "direct"
}

// isMessagingConversation reports whether this row is a direct
// conversation the user holds with the agent over a messaging channel.
// It requires all three signals so a plain "main" session (no channel,
// no peer) and group chats are excluded.
func (r messagingSessionRow) isMessagingConversation() bool {
	return r.isDirect() && r.channelName() != "" && r.peer() != ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
