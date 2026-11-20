package hermes

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// stringField pulls one string out of recorded JSON-RPC params.
func stringField(t *testing.T, raw json.RawMessage, key string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("params %s: %v", raw, err)
	}
	s, _ := m[key].(string)
	return s
}

// The gateway serves every profile on the machine; the picker must list them
// rather than a single synthetic "hermes" agent.
func TestListAgents_ListsTheGatewaysProfiles(t *testing.T) {
	g := newFakeGateway(t, "tok")
	g.on("profiles.list", func(id uint64, _ json.RawMessage) {
		g.respond(id, map[string]any{"profiles": []map[string]any{
			{"name": "default", "model": "deepseek/deepseek-v4.1-flash", "is_default": true},
			{"name": "kowal", "model": "xiaomi/mimo-v2.6-flash"},
			{"name": "librarian", "model": "xiaomi/mimo-v2.6-flash"},
		}})
	})
	b := newTestBackend(t, g, "tok")
	connectBackend(t, b)

	agents, err := b.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents.Agents) != 3 {
		t.Fatalf("agents = %+v, want one per profile", agents.Agents)
	}
	if agents.Agents[1].ID != "kowal" || agents.Agents[1].Name != "kowal" {
		t.Errorf("agent[1] = %+v, want id/name kowal", agents.Agents[1])
	}
	if agents.Agents[1].Model == nil || agents.Agents[1].Model.Primary != "xiaomi/mimo-v2.6-flash" {
		t.Errorf("agent[1] model = %+v", agents.Agents[1].Model)
	}
	if agents.DefaultID != "default" || agents.MainKey != "default" {
		t.Errorf("DefaultID/MainKey = %q/%q, want the default profile", agents.DefaultID, agents.MainKey)
	}
}

// An older gateway answers -32601; the connection must still work as the
// single-profile agent it used to be.
func TestListAgents_FallsBackToTheSyntheticAgent(t *testing.T) {
	g := newFakeGateway(t, "tok")
	g.on("profiles.list", func(id uint64, _ json.RawMessage) {
		g.write(map[string]any{"jsonrpc": "2.0", "id": id,
			"error": map[string]any{"code": -32601, "message": "unknown method: profiles.list"}})
	})
	b := newTestBackend(t, g, "tok")
	connectBackend(t, b)

	agents, err := b.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("ListAgents: %v", err)
	}
	if len(agents.Agents) != 1 || agents.Agents[0].ID != syntheticAgentID {
		t.Fatalf("agents = %+v, want the synthetic fallback", agents.Agents)
	}
}

func TestCreateSession_CarriesTheSelectedProfile(t *testing.T) {
	g := newFakeGateway(t, "tok")
	onCreateSession(g)
	b := newTestBackend(t, g, "tok")
	connectBackend(t, b)

	ctx := context.Background()
	if _, err := b.CreateSession(ctx, "librarian", "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	call := g.waitCall("session.create")
	if got := stringField(t, call.Params, "profile"); got != "librarian" {
		t.Errorf("session.create profile = %q, want librarian (params %s)", got, call.Params)
	}
}

func TestSessionsList_IsScopedToTheProfileAndQualifiesKeys(t *testing.T) {
	g := newFakeGateway(t, "tok")
	g.on("session.list", func(id uint64, _ json.RawMessage) {
		g.respond(id, map[string]any{"sessions": []map[string]any{
			{"id": "20261103_120000_abc", "title": "Plany", "preview": "…", "started_at": 1.0, "message_count": 2},
		}})
	})
	b := newTestBackend(t, g, "tok")
	connectBackend(t, b)

	raw, err := b.SessionsList(context.Background(), "librarian")
	if err != nil {
		t.Fatalf("SessionsList: %v", err)
	}
	call := g.waitCall("session.list")
	if got := stringField(t, call.Params, "profile"); got != "librarian" {
		t.Errorf("session.list profile = %q, want librarian", got)
	}
	var out struct {
		Sessions []struct {
			Key string `json:"key"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Sessions) != 1 || out.Sessions[0].Key != "librarian::20261103_120000_abc" {
		t.Errorf("keys = %+v, want a profile-qualified key", out.Sessions)
	}
}

// Selecting a stored session from the browser yields its qualified key; the
// resume that follows must carry the same profile, or the gateway looks in
// the launch profile's state.db and reports "session not found".
func TestResolve_CarriesTheProfileOfAQualifiedKey(t *testing.T) {
	g := newFakeGateway(t, "tok")
	g.on("session.resume", func(id uint64, _ json.RawMessage) {
		g.respond(id, json.RawMessage(testSessionResult))
	})
	g.on("session.history", func(id uint64, _ json.RawMessage) {
		g.respond(id, map[string]any{"count": 0, "messages": []any{}})
	})
	b := newTestBackend(t, g, "tok")
	connectBackend(t, b)

	if _, err := b.ChatHistory(context.Background(), "librarian::20261103_120000_abc", 0); err != nil {
		t.Fatalf("ChatHistory: %v", err)
	}
	call := g.waitCall("session.resume")
	if got := stringField(t, call.Params, "profile"); got != "librarian" {
		t.Errorf("session.resume profile = %q, want librarian (params %s)", got, call.Params)
	}
	if got := stringField(t, call.Params, "session_id"); got != "20261103_120000_abc" {
		t.Errorf("session.resume session_id = %q, want the stored id", got)
	}
}

func TestSessionPatchModel_DispatchesThroughSlashExec(t *testing.T) {
	g := newFakeGateway(t, "tok")
	onCreateSession(g)
	g.on("slash.exec", func(id uint64, _ json.RawMessage) {
		g.respond(id, map[string]any{"output": "  ✓ Model switched: xiaomi/mimo-v2.6-flash\n    (session only)"})
	})
	b := newTestBackend(t, g, "tok")
	connectBackend(t, b)

	key, err := b.CreateSession(context.Background(), "default", "main")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := b.SessionPatchModel(context.Background(), key, "xiaomi/mimo-v2.6-flash"); err != nil {
		t.Fatalf("SessionPatchModel: %v", err)
	}
	call := g.waitCall("slash.exec")
	if got := stringField(t, call.Params, "command"); got != "/model xiaomi/mimo-v2.6-flash" {
		t.Errorf("slash.exec command = %q", got)
	}
	if got := stringField(t, call.Params, "session_id"); got != "live1" {
		t.Errorf("slash.exec session_id = %q, want the live handle", got)
	}

	b.mu.Lock()
	model := b.model
	b.mu.Unlock()
	if model != "xiaomi/mimo-v2.6-flash" {
		t.Errorf("model = %q, want the switched model", model)
	}
}

func TestSessionPatchModel_SurfacesTheGatewaysRefusal(t *testing.T) {
	g := newFakeGateway(t, "tok")
	onCreateSession(g)
	g.on("slash.exec", func(id uint64, _ json.RawMessage) {
		g.respond(id, map[string]any{"output": "✗ unknown model: nope/nope"})
	})
	b := newTestBackend(t, g, "tok")
	connectBackend(t, b)

	key, err := b.CreateSession(context.Background(), "default", "main")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	b.mu.Lock()
	before := b.model
	b.mu.Unlock()

	err = b.SessionPatchModel(context.Background(), key, "nope/nope")
	if err == nil || !strings.Contains(err.Error(), "unknown model") {
		t.Fatalf("SessionPatchModel = %v, want the gateway's refusal", err)
	}
	b.mu.Lock()
	after := b.model
	b.mu.Unlock()
	if after != before {
		t.Errorf("model changed on a refused switch: %q → %q", before, after)
	}
}

// The synthetic agent (an older gateway) must keep behaving exactly as before:
// no profile on the wire, so the gateway uses its launch profile.
func TestCreateSession_SyntheticAgentSendsNoProfile(t *testing.T) {
	g := newFakeGateway(t, "tok")
	onCreateSession(g)
	b := newTestBackend(t, g, "tok")
	connectBackend(t, b)

	if _, err := b.CreateSession(context.Background(), syntheticAgentID, "main"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	call := g.waitCall("session.create")
	if got := stringField(t, call.Params, "profile"); got != "" {
		t.Errorf("session.create profile = %q, want none for the synthetic agent", got)
	}
}
