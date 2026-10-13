package rooms

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// ── fake gateway ─────────────────────────────────────────────────────────────

// fakeServer is an in-process WS endpoint; the handler owns the upgraded
// connection. Mirrors the pattern the rpc package's tests use.
func fakeServer(t *testing.T, handler func(conn *websocket.Conn)) (wsURL string) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		handler(conn)
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func readReq(t *testing.T, conn *websocket.Conn) (id uint64, method string, params json.RawMessage) {
	t.Helper()
	var req struct {
		ID     uint64          `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := conn.ReadJSON(&req); err != nil {
		t.Fatalf("server read: %v", err)
	}
	return req.ID, req.Method, req.Params
}

func respond(t *testing.T, conn *websocket.Conn, id uint64, result any) {
	t.Helper()
	if err := conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": id, "result": result}); err != nil {
		t.Fatalf("server write: %v", err)
	}
}

// ── roster validation (server rules: 2-6 local profiles, unique handles) ──────

func member(id, profile, handle string) Member {
	return Member{MemberID: id, Profile: profile, Handle: handle}
}

func TestValidateRoster_AcceptsTwoToSixUniqueProfiles(t *testing.T) {
	cases := map[string][]Member{
		"two": {member("m1", "matt", "matt"), member("m2", "deepsh", "deepsh")},
		"six": {
			member("m1", "a", "a"), member("m2", "b", "b"), member("m3", "c", "c"),
			member("m4", "d", "d"), member("m5", "e", "e"), member("m6", "f", "f"),
		},
	}
	for name, roster := range cases {
		t.Run(name, func(t *testing.T) {
			if err := ValidateRoster(roster); err != nil {
				t.Fatalf("ValidateRoster(%s) = %v, want nil", name, err)
			}
		})
	}
}

func TestValidateRoster_Rejects(t *testing.T) {
	cases := map[string]struct {
		roster []Member
		want   string
	}{
		"one member": {
			[]Member{member("m1", "matt", "matt")},
			"between 2 and 6",
		},
		"seven members": {
			[]Member{
				member("m1", "a", "a"), member("m2", "b", "b"), member("m3", "c", "c"),
				member("m4", "d", "d"), member("m5", "e", "e"), member("m6", "f", "f"),
				member("m7", "g", "g"),
			},
			"between 2 and 6",
		},
		"duplicate profile": {
			[]Member{member("m1", "matt", "matt"), member("m2", "matt", "matt2")},
			"profiles must be unique",
		},
		"duplicate handle": {
			[]Member{member("m1", "matt", "matt"), member("m2", "deepsh", "matt")},
			"handles must be unique",
		},
		"duplicate member id": {
			[]Member{member("m1", "matt", "matt"), member("m1", "deepsh", "deepsh")},
			"member ids must be unique",
		},
		"reserved handle all": {
			[]Member{member("m1", "matt", "all"), member("m2", "deepsh", "deepsh")},
			"reserved",
		},
		"reserved handle everyone": {
			[]Member{member("m1", "matt", "everyone"), member("m2", "deepsh", "deepsh")},
			"reserved",
		},
		"empty profile": {
			[]Member{member("m1", "", "matt"), member("m2", "deepsh", "deepsh")},
			"profile is required",
		},
		"empty handle": {
			[]Member{member("m1", "matt", ""), member("m2", "deepsh", "deepsh")},
			"handle is required",
		},
		"empty member id": {
			[]Member{member("", "matt", "matt"), member("m2", "deepsh", "deepsh")},
			"member id is required",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateRoster(tc.roster)
			if err == nil {
				t.Fatalf("ValidateRoster = nil, want error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// ── ws URL derivation (mirrors the Hermes backend's contract) ────────────────

func TestGatewayWSURL(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
		token   string
		want    string
	}{
		{"http to ws", "http://localhost:9119", "", "ws://localhost:9119/api/ws"},
		{"https to wss", "https://box.example.com", "", "wss://box.example.com/api/ws"},
		{"token as query", "http://localhost:9119", "sekret", "ws://localhost:9119/api/ws?token=sekret"},
		{"existing path replaced", "http://localhost:9119/dashboard", "", "ws://localhost:9119/api/ws"},
		{"ws scheme kept", "ws://127.0.0.1:9119", "", "ws://127.0.0.1:9119/api/ws"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := GatewayWSURL(tc.baseURL, tc.token)
			if err != nil {
				t.Fatalf("GatewayWSURL: %v", err)
			}
			if got != tc.want {
				t.Fatalf("GatewayWSURL = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestGatewayWSURL_RejectsUnsupportedScheme(t *testing.T) {
	if _, err := GatewayWSURL("ftp://localhost:9119", ""); err == nil {
		t.Fatal("GatewayWSURL(ftp://) = nil error, want unsupported scheme")
	}
}

// ── client round-trips against a fake gateway ────────────────────────────────

func TestClient_Capabilities(t *testing.T) {
	wsURL := fakeServer(t, func(conn *websocket.Conn) {
		id, method, _ := readReq(t, conn)
		if method != "groups.capabilities" {
			t.Errorf("method = %q, want groups.capabilities", method)
		}
		respond(t, conn, id, map[string]any{
			"protocol_version": 2, "driver": true, "persistent_process": true,
			"authority_gateway_id": "install:abc",
			"room_link":            map[string]any{"enabled": false, "reason": "not_configured"},
			"features":             []string{"room_identity"},
			"methods":              []string{"groups.create", "groups.send"},
			"max_log_limit":        500,
		})
	})
	c := dialTestClient(t, wsURL)
	caps, err := c.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities: %v", err)
	}
	if caps.ProtocolVersion != 2 || !caps.Driver || caps.MaxLogLimit != 500 {
		t.Fatalf("Capabilities = %+v", caps)
	}
	if caps.RoomLink.Enabled {
		t.Fatalf("RoomLink.Enabled = true, want false")
	}
}

func TestClient_List(t *testing.T) {
	wsURL := fakeServer(t, func(conn *websocket.Conn) {
		id, method, params := readReq(t, conn)
		if method != "groups.list" {
			t.Errorf("method = %q, want groups.list", method)
		}
		if !strings.Contains(string(params), `"include_disbanded":true`) {
			t.Errorf("params = %s, want include_disbanded true", params)
		}
		respond(t, conn, id, map[string]any{"rooms": []map[string]any{{
			"room_id": "r1", "name": "Sztab", "revision": 3,
			"authority_gateway_id": "install:abc", "authority_epoch": 1,
			"created_at": 1.0, "updated_at": 2.0,
			"members": []map[string]any{
				{"member_id": "m1", "profile": "matt", "handle": "matt", "display_name": "Matt"},
				{"member_id": "m2", "profile": "kowal", "handle": "kowal"},
			},
		}}, "next_offset": nil})
	})
	c := dialTestClient(t, wsURL)
	got, err := c.List(context.Background(), true)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].RoomID != "r1" || got[0].Name != "Sztab" {
		t.Fatalf("List = %+v", got)
	}
	if len(got[0].Members) != 2 || got[0].Members[1].Handle != "kowal" {
		t.Fatalf("members = %+v", got[0].Members)
	}
}

func TestClient_CreateSendsRoster(t *testing.T) {
	roster := []Member{
		{MemberID: "m1", Profile: "matt", Handle: "matt", DisplayName: "Matt"},
		{MemberID: "m2", Profile: "kowal", Handle: "kowal"},
	}
	wsURL := fakeServer(t, func(conn *websocket.Conn) {
		id, method, params := readReq(t, conn)
		if method != "groups.create" {
			t.Errorf("method = %q, want groups.create", method)
		}
		var p struct {
			RoomID  string   `json:"room_id"`
			Name    string   `json:"name"`
			Members []Member `json:"members"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			t.Fatalf("params decode: %v", err)
		}
		if p.RoomID != "r9" || p.Name != "Sztab" {
			t.Errorf("params = %+v", p)
		}
		if len(p.Members) != 2 || p.Members[1].Profile != "kowal" {
			t.Errorf("members = %+v", p.Members)
		}
		respond(t, conn, id, map[string]any{"room": map[string]any{
			"room_id": "r9", "name": "Sztab", "revision": 1,
			"authority_gateway_id": "install:abc", "authority_epoch": 1,
			"created_at": 1.0, "updated_at": 1.0, "members": p.Members,
		}})
	})
	c := dialTestClient(t, wsURL)
	room, err := c.Create(context.Background(), "r9", "Sztab", roster)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if room.RoomID != "r9" || len(room.Members) != 2 {
		t.Fatalf("Create = %+v", room)
	}
}

func TestClient_CreateRejectsBadRosterBeforeDial(t *testing.T) {
	// A roster the gateway would refuse must never reach the wire. The
	// fake server signals on a *request*, not on the connection, because
	// Dial itself opens the socket.
	hit := make(chan struct{}, 1)
	wsURL := fakeServer(t, func(conn *websocket.Conn) {
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, _, err := conn.ReadMessage(); err == nil {
			select {
			case hit <- struct{}{}:
			default:
			}
		}
	})
	c := dialTestClient(t, wsURL)
	_, err := c.Create(context.Background(), "r1", "Zły", []Member{member("m1", "matt", "matt")})
	if err == nil {
		t.Fatal("Create with 1 member = nil error, want roster rejection")
	}
	select {
	case <-hit:
		t.Fatal("bad roster still hit the wire")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestClient_SendCarriesTextAndThread(t *testing.T) {
	wsURL := fakeServer(t, func(conn *websocket.Conn) {
		id, method, params := readReq(t, conn)
		if method != "groups.send" {
			t.Errorf("method = %q, want groups.send", method)
		}
		var p struct {
			RoomID  string         `json:"room_id"`
			EventID string         `json:"event_id"`
			Payload map[string]any `json:"payload"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			t.Fatalf("params decode: %v", err)
		}
		if p.RoomID != "r1" || p.Payload["text"] != "hej" || p.Payload["thread_id"] != "main" {
			t.Errorf("params = %+v", p)
		}
		if p.EventID == "" {
			t.Error("event_id must be sent so a retry is idempotent")
		}
		respond(t, conn, id, map[string]any{
			"accepted": true, "driver_started": true,
			"event": map[string]any{"room_id": "r1", "seq": 7, "event_id": "e7", "kind": "message.user",
				"actor":   map[string]any{"kind": "user", "id": "desktop"},
				"payload": map[string]any{"text": "hej", "thread_id": "main"}, "created_at": 3.0},
		})
	})
	c := dialTestClient(t, wsURL)
	ev, err := c.Send(context.Background(), "r1", "hej", "main")
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if ev.Seq != 7 || ev.Kind != "message.user" {
		t.Fatalf("Send = %+v", ev)
	}
}

func TestClient_LogDecodesTranscript(t *testing.T) {
	wsURL := fakeServer(t, func(conn *websocket.Conn) {
		id, method, params := readReq(t, conn)
		if method != "groups.log" {
			t.Errorf("method = %q, want groups.log", method)
		}
		if !strings.Contains(string(params), `"since_seq":4`) {
			t.Errorf("params = %s, want since_seq 4", params)
		}
		respond(t, conn, id, map[string]any{
			"cursor": 9, "latest_seq": 9, "has_more": false,
			"authority": map[string]any{"gateway_id": "install:abc", "epoch": 1},
			"events": []map[string]any{
				{"room_id": "r1", "seq": 5, "event_id": "e5", "kind": "message.member",
					"actor":      map[string]any{"kind": "member", "id": "m1", "display_name": "Matt", "profile": "matt"},
					"payload":    map[string]any{"text": "Orca to środowisko pracy agentów.", "member_id": "m1"},
					"created_at": 4.0},
				{"room_id": "r1", "seq": 6, "event_id": "e6", "kind": "turn.settled",
					"actor":      map[string]any{"kind": "gateway", "id": "install:abc"},
					"payload":    map[string]any{"member_id": "m1", "passed": false, "member_index": 0},
					"created_at": 4.1},
			},
		})
	})
	c := dialTestClient(t, wsURL)
	page, err := c.Log(context.Background(), "r1", 4, 100)
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if page.LatestSeq != 9 || len(page.Events) != 2 {
		t.Fatalf("Log = %+v", page)
	}
	if page.Events[0].Actor.DisplayName != "Matt" {
		t.Fatalf("actor = %+v", page.Events[0].Actor)
	}
	if text, _ := page.Events[0].Payload["text"].(string); !strings.HasPrefix(text, "Orca to") {
		t.Fatalf("payload text = %v", page.Events[0].Payload["text"])
	}
}

func TestClient_ErrorResponseSurfacesMessage(t *testing.T) {
	wsURL := fakeServer(t, func(conn *websocket.Conn) {
		id, _, _ := readReq(t, conn)
		if err := conn.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": id,
			"error": map[string]any{"code": 4001, "message": "room not found"}}); err != nil {
			t.Fatalf("write: %v", err)
		}
	})
	c := dialTestClient(t, wsURL)
	if _, err := c.List(context.Background(), false); err == nil ||
		!strings.Contains(err.Error(), "room not found") {
		t.Fatalf("List error = %v, want it to mention room not found", err)
	}
}

func dialTestClient(t *testing.T, wsURL string) *Client {
	t.Helper()
	c, err := Dial(context.Background(), wsURL)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// ── preset store ("predefiniowane pokoje") ───────────────────────────────────

func TestPresetStore_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rooms.json")

	store, err := LoadPresets(path)
	if err != nil {
		t.Fatalf("LoadPresets(empty): %v", err)
	}
	if len(store.Presets) != 0 {
		t.Fatalf("empty store = %+v", store)
	}

	p := Preset{Name: "Sztab", RoomID: "sztab", ThreadID: "main", Members: []Member{
		{MemberID: "m1", Profile: "matt", Handle: "matt"},
		{MemberID: "m2", Profile: "kowal", Handle: "kowal"},
	}}
	if err := store.Add(p); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := SavePresets(path, store); err != nil {
		t.Fatalf("SavePresets: %v", err)
	}
	again, err := LoadPresets(path)
	if err != nil {
		t.Fatalf("LoadPresets: %v", err)
	}
	got := again.Find("sztab")
	if got == nil {
		t.Fatalf("Find(sztab) = nil; store = %+v", again)
	}
	if got.Name != "Sztab" || len(got.Members) != 2 || got.Members[1].Profile != "kowal" {
		t.Fatalf("preset = %+v", got)
	}
}

func TestPresetStore_AddValidatesRosterAndDuplicates(t *testing.T) {
	store := PresetStore{}
	bad := Preset{Name: "Zły", Members: []Member{member("m1", "matt", "matt")}}
	if err := store.Add(bad); err == nil {
		t.Fatal("Add with a 1-member roster = nil error, want rejection")
	}
	good := Preset{Name: "Dobry", Members: []Member{
		member("m1", "matt", "matt"), member("m2", "kowal", "kowal")}}
	if err := store.Add(good); err != nil {
		t.Fatalf("Add(good): %v", err)
	}
	if err := store.Add(good); err == nil {
		t.Fatal("Add(duplicate name) = nil error, want rejection")
	}
	if !store.Remove("dobry") {
		t.Fatal("Remove(dobry) = false, want true (name match is case-insensitive)")
	}
	if store.Find("dobry") != nil {
		t.Fatal("Find after Remove = non-nil")
	}
}

func TestLoadPresets_MalformedFileIsEmptyNotFatal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rooms.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := LoadPresets(path)
	if err != nil {
		t.Fatalf("LoadPresets(malformed) = %v, want empty store", err)
	}
	if len(store.Presets) != 0 {
		t.Fatalf("store = %+v, want empty", store)
	}
}

// ── local profile discovery (the invite menu's source) ───────────────────────

func TestDiscoverProfiles(t *testing.T) {
	home := t.TempDir()
	// A real profile carries an identity marker; a bare directory is not
	// one (see TestDiscoverProfiles_MirrorsHermesProfileRules).
	for _, name := range []string{"agency", "kowal", "matt"} {
		dir := filepath.Join(home, "profiles", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// A stray file under profiles/ is not a profile.
	if err := os.WriteFile(filepath.Join(home, "profiles", "README.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := DiscoverProfiles(home)
	if err != nil {
		t.Fatalf("DiscoverProfiles: %v", err)
	}
	want := []string{"default", "agency", "kowal", "matt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("DiscoverProfiles = %v, want %v", got, want)
	}
}

func TestDiscoverProfiles_MissingProfilesDirStillOffersDefault(t *testing.T) {
	got, err := DiscoverProfiles(t.TempDir())
	if err != nil {
		t.Fatalf("DiscoverProfiles: %v", err)
	}
	if len(got) != 1 || got[0] != "default" {
		t.Fatalf("DiscoverProfiles = %v, want [default]", got)
	}
}

// A directory under profiles/ is not necessarily a profile: this host
// carries `daily2.bak-…` and `research.pre-research-zip`, plus cron/cache
// side-effect dirs. Listing one puts a name in the invite menu that the
// gateway refuses to seat, so the filter mirrors
// hermes_cli.profiles._iter_named_profile_dirs.
func TestDiscoverProfiles_MirrorsHermesProfileRules(t *testing.T) {
	home := t.TempDir()
	profiles := filepath.Join(home, "profiles")

	real := func(name string, marker string) {
		dir := filepath.Join(profiles, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if marker != "" {
			if err := os.WriteFile(filepath.Join(dir, marker), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	real("good", "config.yaml")       // ordinary profile
	real("dotenv-only", ".env")       // .env alone is an identity claim
	real("bad.id", "config.yaml")     // invalid id (dot)
	real("BADUPPER", "config.yaml")   // invalid id (uppercase)
	real("ghost", "")                 // no identity marker
	real("tombstoned", "config.yaml") // has identity but is tombstoned
	real("_archiwum", "config.yaml")  // infra dir
	real(".tsbuild", "config.yaml")   // dot dir
	if err := os.MkdirAll(filepath.Join(profiles, deletedProfilesDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(profiles, deletedProfilesDir, "tombstoned"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := DiscoverProfiles(home)
	if err != nil {
		t.Fatalf("DiscoverProfiles: %v", err)
	}
	want := "default,dotenv-only,good"
	if strings.Join(got, ",") != want {
		t.Fatalf("DiscoverProfiles = %v, want %s", got, want)
	}
}

func TestDiscoverProfiles_AcceptsASymlinkedIdentityMarker(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "profiles", "cloned")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A dangling symlinked marker is an identity claim left by a clone or
	// migration; Python's is_file()/is_symlink() pair counts it.
	if err := os.Symlink(filepath.Join(t.TempDir(), "nowhere.yaml"), filepath.Join(dir, "config.yaml")); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	got, err := DiscoverProfiles(home)
	if err != nil {
		t.Fatalf("DiscoverProfiles: %v", err)
	}
	if strings.Join(got, ",") != "default,cloned" {
		t.Fatalf("DiscoverProfiles = %v, want [default cloned]", got)
	}
}

func TestDiscoverProfiles_FollowsSymlinkedProfileDirs(t *testing.T) {
	// Profiles are not always real directories: a profile can be a
	// symlink into a project checkout (e.g. deepsh -> E:/orca/.../deepsh).
	// DirEntry.IsDir() is false for those, so the listing must stat.
	home := t.TempDir()
	target := filepath.Join(t.TempDir(), "deepsh")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "config.yaml"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "profiles"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, "profiles", "deepsh")); err != nil {
		t.Skipf("symlinks unavailable here: %v", err)
	}
	got, err := DiscoverProfiles(home)
	if err != nil {
		t.Fatalf("DiscoverProfiles: %v", err)
	}
	want := "default,deepsh"
	if strings.Join(got, ",") != want {
		t.Fatalf("DiscoverProfiles = %v, want %s", got, want)
	}
}

// HERMES_HOME points at the *active profile's* home when a profile is
// running (`<install>/profiles/<name>`), so sibling profiles are only
// reachable by walking up to the install home. Getting this wrong shows
// an empty invite list — the bug this test pins.
func TestResolveInstallHome(t *testing.T) {
	install := t.TempDir()
	for _, name := range []string{"matt", "kowal"} {
		if err := os.MkdirAll(filepath.Join(install, "profiles", name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name string
		env  string
	}{
		{"profile home walks up", filepath.Join(install, "profiles", "matt")},
		{"install home as-is", install},
		{"profiles dir itself", filepath.Join(install, "profiles")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HERMES_HOME", tc.env)
			if got := ResolveInstallHome(); got != install {
				t.Fatalf("ResolveInstallHome() = %q, want %q", got, install)
			}
		})
	}
}

func TestResolveInstallHome_UnsetFallsBackToPlatformDefault(t *testing.T) {
	t.Setenv("HERMES_HOME", "")
	t.Setenv("LOCALAPPDATA", "")
	got := ResolveInstallHome()
	if got == "" {
		t.Fatal("ResolveInstallHome() = empty, want a usable path")
	}
}

func TestDisplayNameFor(t *testing.T) {
	cases := []struct {
		profiles []string
		want     string
	}{
		{[]string{"matt", "kowal"}, "Matt+Kowal"},
		{[]string{"default"}, "Default"},
		{[]string{"matt", " ", "deepsh"}, "Matt+Deepsh"},
		{nil, "New room"},
		// A six-name roster is unreadable concatenated; it collapses.
		{[]string{"a", "b", "c", "d", "e", "f"}, "A+B+C+(+3)"},
		{[]string{"a", "b", "c"}, "A+B+C"},
	}
	for _, tc := range cases {
		if got := DisplayNameFor(tc.profiles); got != tc.want {
			t.Errorf("DisplayNameFor(%v) = %q, want %q", tc.profiles, got, tc.want)
		}
	}
}

func TestHandleFor_AvoidsReservedAndSanitises(t *testing.T) {
	cases := map[string]string{
		"matt":        "matt",
		"Deep Sh":     "deep-sh",
		"all":         "bot-all",
		"everyone":    "bot-everyone",
		"a@b.com":     "a-b.com",
		"":            "bot",
		"agent/oles":  "agent-oles",
		"__weird__":   "weird",
		"UPPER_case":  "upper_case",
		"trailing---": "trailing",
	}
	for in, want := range cases {
		if got := HandleFor(in); got != want {
			t.Errorf("HandleFor(%q) = %q, want %q", in, got, want)
		}
	}
}

// A handle the gateway's mention grammar cannot match is a member that
// can never be addressed, so every derived handle must satisfy
// @([A-Za-z0-9][A-Za-z0-9._:-]*).
func TestHandleFor_AlwaysMentionable(t *testing.T) {
	mentionable := regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)
	for _, in := range []string{
		"matt", "Deep Sh", "all", "everyone", "a@b.com", "", "agent/oles",
		"__weird__", "UPPER_case", "trailing---", "9lives", ".dot", "-dash",
	} {
		got := HandleFor(in)
		if !mentionable.MatchString(got) {
			t.Errorf("HandleFor(%q) = %q, which no @mention can reach", in, got)
		}
	}
}

func TestRosterFor_DerivesUniqueHandlesAndIds(t *testing.T) {
	roster := RosterFor([]string{"matt", "kowal"})
	if err := ValidateRoster(roster); err != nil {
		t.Fatalf("RosterFor produced an invalid roster: %v", err)
	}
	if roster[0].MemberID != "m1" || roster[1].MemberID != "m2" {
		t.Fatalf("member ids = %q,%q", roster[0].MemberID, roster[1].MemberID)
	}
	if roster[1].Handle != "kowal" || roster[1].DisplayName != "Kowal" {
		t.Fatalf("member = %+v", roster[1])
	}
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Sztab Orca": "sztab-orca",
		"  Team  ":   "team",
		"!!!":        "room",
		"a__b":       "a-b",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

// A room id is typed by hand in `rooms send <id>`, so a six-profile
// roster must not produce a 90-character slug.
func TestSlug_BoundsDerivedRoomIDs(t *testing.T) {
	long := Slug(DisplayNameFor([]string{
		"agent-lab", "default", "research-archivist", "research-miner",
		"research-publisher", "research-scout",
	}))
	if len(long) > 48 {
		t.Fatalf("Slug produced %d chars (%q), want <= 48", len(long), long)
	}
	if long == "" || strings.HasPrefix(long, "-") || strings.HasSuffix(long, "-") {
		t.Fatalf("Slug produced a malformed id: %q", long)
	}
	// The gateway's own ceiling is 128; stay well inside it.
	if len(long) > 128 {
		t.Fatalf("Slug exceeds the gateway's room-id limit: %d", len(long))
	}
}

// ── orientation (agents must know their project + the Orca browser flow) ─────

func TestOrientation_Empty(t *testing.T) {
	if !(Orientation{}).Empty() {
		t.Error("zero Orientation should be empty")
	}
	if (Orientation{Worktree: "active"}).Empty() {
		t.Error("a worktree selector is not empty")
	}
	if (Orientation{Path: "E:/x"}).Empty() {
		t.Error("a path is not empty")
	}
}

func TestOrientation_BlockNamesTheProjectAndTheBrowserFlow(t *testing.T) {
	o := Orientation{
		Project:  "HermesHarness",
		Path:     "E:/orca/workspaces/HermesHarness",
		Worktree: "id:1c0a9030::E:/orca/workspaces/HermesHarness",
	}
	block := o.Block()
	for _, want := range []string{
		OrientationMarker,
		"HermesHarness",
		"E:/orca/workspaces/HermesHarness",
		"id:1c0a9030::E:/orca/workspaces/HermesHarness",
		// The browser sequence has to be spelled out: an agent that does
		// not know to create a tab first reports "browser unavailable".
		"orca_tab_create",
		"browserPageId",
		"orca_browser_goto",
		"orca_browser_snapshot",
		"orca_file_open",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("orientation block missing %q:\n%s", want, block)
		}
	}
	if (Orientation{}).Block() != "" {
		t.Error("empty orientation should render nothing")
	}
}

func TestOrientationFromEnv(t *testing.T) {
	t.Setenv("ORCA_WORKTREE_ID", "1c0a9030-ef8a-473f-9531-b2803dd92bdf::E:/orca/workspaces/HermesHarness")
	o := OrientationFromEnv()
	if o.Worktree != "id:1c0a9030-ef8a-473f-9531-b2803dd92bdf::E:/orca/workspaces/HermesHarness" {
		t.Errorf("Worktree = %q", o.Worktree)
	}
	if o.Path != "E:/orca/workspaces/HermesHarness" || o.Project != "HermesHarness" {
		t.Errorf("Path/Project = %q / %q", o.Path, o.Project)
	}
	t.Setenv("ORCA_WORKTREE_ID", "")
	if !OrientationFromEnv().Empty() {
		t.Error("no ORCA_WORKTREE_ID should give an empty orientation")
	}
}

func TestOrientationFromWorktree(t *testing.T) {
	cases := []struct {
		sel         string
		wantPath    string
		wantProject string
	}{
		{"id:1c0a9030::E:/orca/workspaces/HermesHarness", "E:/orca/workspaces/HermesHarness", "HermesHarness"},
		{"path:E:/orca/ws/proj", "E:/orca/ws/proj", "proj"},
		{"name:flue-agents", "", "flue-agents"},
		{"active", "", ""},
		{"", "", ""},
	}
	for _, tc := range cases {
		got := OrientationFromWorktree(tc.sel)
		if got.Path != tc.wantPath || got.Project != tc.wantProject {
			t.Errorf("OrientationFromWorktree(%q) = path %q project %q, want %q / %q",
				tc.sel, got.Path, got.Project, tc.wantPath, tc.wantProject)
		}
		if tc.sel != "" && got.Worktree != tc.sel {
			t.Errorf("OrientationFromWorktree(%q) kept selector %q", tc.sel, got.Worktree)
		}
	}
	if !OrientationFromWorktree("").Empty() {
		t.Error("an empty selector should give an empty orientation")
	}
}

func TestClient_SendOpening_PrependsContextOnTheFirstMessage(t *testing.T) {
	var sendParams []string
	wsURL := fakeServer(t, func(conn *websocket.Conn) {
		for i := 0; i < 2; i++ {
			id, method, params := readReq(t, conn)
			switch method {
			case "groups.log":
				respond(t, conn, id, map[string]any{"events": []any{}, "cursor": 0, "latest_seq": 0, "has_more": false,
					"authority": map[string]any{"gateway_id": "g", "epoch": 1}})
			case "groups.send":
				sendParams = append(sendParams, string(params))
				respond(t, conn, id, map[string]any{"accepted": true, "driver_started": true,
					"event": map[string]any{"room_id": "r1", "seq": 1, "event_id": "e1", "kind": "message.user",
						"actor": map[string]any{"kind": "user", "id": "u"}, "payload": map[string]any{}, "created_at": 1.0}})
			default:
				t.Errorf("unexpected method %q", method)
				return
			}
		}
	})
	c := dialTestClient(t, wsURL)
	orient := Orientation{Project: "HermesHarness", Path: "E:/orca/ws/HermesHarness", Worktree: "active"}
	if _, err := c.SendOpening(context.Background(), "r1", "co robimy?", "main", orient); err != nil {
		t.Fatalf("SendOpening: %v", err)
	}
	if len(sendParams) != 1 {
		t.Fatalf("send calls = %d, want 1", len(sendParams))
	}
	if !strings.Contains(sendParams[0], "kontekst projektu") || !strings.Contains(sendParams[0], "orca_tab_create") {
		t.Fatalf("first message missing the project context: %s", sendParams[0])
	}
	if !strings.Contains(sendParams[0], "co robimy?") {
		t.Fatalf("first message lost the user text: %s", sendParams[0])
	}
}

func TestClient_SendOpening_LeavesLaterMessagesAlone(t *testing.T) {
	var sendParams []string
	wsURL := fakeServer(t, func(conn *websocket.Conn) {
		for i := 0; i < 2; i++ {
			id, method, params := readReq(t, conn)
			switch method {
			case "groups.log":
				respond(t, conn, id, map[string]any{
					"events": []map[string]any{{"room_id": "r1", "seq": 1, "event_id": "e1", "kind": "message.user",
						"actor": map[string]any{"kind": "user", "id": "u"}, "payload": map[string]any{}, "created_at": 1.0}},
					"cursor": 1, "latest_seq": 1, "has_more": false,
					"authority": map[string]any{"gateway_id": "g", "epoch": 1}})
			case "groups.send":
				sendParams = append(sendParams, string(params))
				respond(t, conn, id, map[string]any{"accepted": true, "driver_started": true,
					"event": map[string]any{"room_id": "r1", "seq": 2, "event_id": "e2", "kind": "message.user",
						"actor": map[string]any{"kind": "user", "id": "u"}, "payload": map[string]any{}, "created_at": 2.0}})
			default:
				t.Errorf("unexpected method %q", method)
				return
			}
		}
	})
	c := dialTestClient(t, wsURL)
	orient := Orientation{Project: "HermesHarness", Worktree: "active"}
	if _, err := c.SendOpening(context.Background(), "r1", "drugie pytanie", "main", orient); err != nil {
		t.Fatalf("SendOpening: %v", err)
	}
	if len(sendParams) != 1 {
		t.Fatalf("send calls = %d, want 1", len(sendParams))
	}
	if strings.Contains(sendParams[0], "kontekst projektu") {
		t.Fatalf("context repeated on a later message: %s", sendParams[0])
	}
	if !strings.Contains(sendParams[0], "drugie pytanie") {
		t.Fatalf("text missing: %s", sendParams[0])
	}
}

// ── timeout plumbing ─────────────────────────────────────────────────────────

func TestClient_CallTimeoutIsBounded(t *testing.T) {
	wsURL := fakeServer(t, func(conn *websocket.Conn) {
		_, _, _ = readReq(t, conn) // read but never answer
		time.Sleep(2 * time.Second)
	})
	c := dialTestClient(t, wsURL)
	c.SetTimeout(150 * time.Millisecond)
	start := time.Now()
	if _, err := c.List(context.Background(), false); err == nil {
		t.Fatal("List = nil error, want timeout")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("List took %v, want a bounded timeout", elapsed)
	}
}
