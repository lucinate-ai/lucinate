package backend

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestPickMessagingKeyFromRaw(t *testing.T) {
	const telegramDM = `{
		"key": "agent:main:telegram:direct:123456789",
		"kind": "direct",
		"channel": "telegram",
		"lastChannel": "telegram",
		"lastTo": "123456789",
		"updatedAt": 1000,
		"origin": {"provider":"telegram","chatType":"direct","from":"123456789","nativeDirectUserId":"123456789"},
		"deliveryContext": {"channel":"telegram","to":"123456789"}
	}`
	// A plain main session: no channel, no peer. Newer than the DM so
	// "most recent wins" alone would pick it — it must still be skipped.
	const plainMain = `{"key":"agent:main:main","kind":"global","updatedAt":9000}`
	const cron = `{"key":"agent:main:cron:daily","kind":"unknown","updatedAt":500}`
	// A group chat over telegram: has a channel and a peer but is not a
	// one-to-one conversation. Newest of all — must still be excluded.
	const telegramGroup = `{
		"key":"agent:main:telegram:group:-100123",
		"kind":"group","channel":"telegram","lastTo":"-100123","updatedAt":99999,
		"origin":{"provider":"telegram","chatType":"group"}
	}`

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "telegram DM chosen over a newer plain main session",
			raw:  `{"sessions":[` + plainMain + `,` + telegramDM + `]}`,
			want: "agent:main:telegram:direct:123456789",
		},
		{
			name: "most recently updated messaging conversation wins",
			raw: `{"sessions":[
				{"key":"old","kind":"direct","channel":"telegram","lastTo":"1","updatedAt":100},
				{"key":"new","kind":"direct","channel":"telegram","lastTo":"2","updatedAt":200}
			]}`,
			want: "new",
		},
		{
			name: "group and cron and main only yields no messaging session",
			raw:  `{"sessions":[` + plainMain + `,` + cron + `,` + telegramGroup + `]}`,
			want: "",
		},
		{
			name: "direct signalled only via origin.chatType and origin fields",
			raw: `{"sessions":[{
				"key":"origin-only","updatedAt":10,
				"origin":{"provider":"telegram","chatType":"direct","from":"42"}
			}]}`,
			want: "origin-only",
		},
		{
			name: "direct signalled only via deliveryContext",
			raw: `{"sessions":[{
				"key":"delivery-only","kind":"direct","updatedAt":10,
				"deliveryContext":{"channel":"whatsapp","to":"+15551234"}
			}]}`,
			want: "delivery-only",
		},
		{
			name: "channel present but no peer is not a conversation",
			raw:  `{"sessions":[{"key":"no-peer","kind":"direct","channel":"telegram","updatedAt":10}]}`,
			want: "",
		},
		{
			name: "empty session list",
			raw:  `{"sessions":[]}`,
			want: "",
		},
		{
			name: "malformed json",
			raw:  `not json`,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pickMessagingKeyFromRaw([]byte(tt.raw))
			if got != tt.want {
				t.Errorf("pickMessagingKeyFromRaw() = %q, want %q", got, tt.want)
			}
		})
	}
}

// stubBackend embeds the Backend interface (nil) so only the one method
// we exercise needs a body; any other call would panic, which is what we
// want if the helper ever reaches for more than sessions.list.
type stubBackend struct {
	Backend
	raw []byte
	err error
}

func (s stubBackend) SessionsList(context.Context, string) (json.RawMessage, error) {
	return s.raw, s.err
}

func TestPickMessagingSessionKey_SessionsListError(t *testing.T) {
	got := PickMessagingSessionKey(context.Background(), stubBackend{err: errors.New("boom")}, "main")
	if got != "" {
		t.Errorf("on sessions.list error want %q, got %q", "", got)
	}
}

func TestPickMessagingSessionKey_HappyPath(t *testing.T) {
	raw := []byte(`{"sessions":[{
		"key":"agent:main:telegram:direct:123456789","kind":"direct",
		"deliveryContext":{"channel":"telegram","to":"123456789"},"updatedAt":5
	}]}`)
	got := PickMessagingSessionKey(context.Background(), stubBackend{raw: raw}, "main")
	if got != "agent:main:telegram:direct:123456789" {
		t.Errorf("got %q", got)
	}
}
