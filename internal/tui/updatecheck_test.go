package tui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lucinate-ai/lucinate/internal/config"
	"github.com/lucinate-ai/lucinate/internal/update"
)

func TestUpdateCheckCmd_NilWhenEnvVarDisables(t *testing.T) {
	t.Setenv(update.DisableEnvVar, "1")
	if updateCheckCmd(enabledPrefs()) != nil {
		t.Error("expected nil cmd when env var disables update check")
	}
}

func TestUpdateCheckCmd_NilWhenPrefDisabled(t *testing.T) {
	off := false
	prefs := enabledPrefs()
	prefs.CheckForUpdates = &off
	if updateCheckCmd(prefs) != nil {
		t.Error("expected nil cmd when pref disables update check")
	}
}

func TestUpdateCheckCmd_NilWhenWithinRateLimitWindow(t *testing.T) {
	prefs := enabledPrefs()
	prefs.LastUpdateCheck = time.Now().Unix() - 60 // 1 minute ago
	if updateCheckCmd(prefs) != nil {
		t.Error("expected nil cmd within the daily rate-limit window")
	}
}

func TestUpdateCheckCmd_RunsWhenStale(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"v9.9.9","url":"https://example/v9.9.9"}`))
	}))
	defer srv.Close()

	t.Setenv(updateCheckURLEnvVar, srv.URL)
	prefs := enabledPrefs()
	prefs.LastUpdateCheck = time.Now().Add(-48 * time.Hour).Unix()

	cmd := updateCheckCmd(prefs)
	if cmd == nil {
		t.Fatal("expected a cmd when last check is older than the rate limit")
	}
	msg := cmd()
	done, ok := msg.(updateCheckDoneMsg)
	if !ok {
		t.Fatalf("expected updateCheckDoneMsg, got %T", msg)
	}
	// Note: this test uses internal/version.Version which is "dev"
	// during go test, so update.Check short-circuits and returns nil.
	// We assert the timestamp gets recorded regardless — that's the
	// rate-limit invariant we care about.
	if done.At == 0 {
		t.Error("expected At to be a recent unix timestamp")
	}
}

func TestUpdateCheckDoneMsg_PersistsAndBadges(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	app := AppModel{prefs: config.DefaultPreferences()}
	msg := updateCheckDoneMsg{
		At:         1700000000,
		LatestSeen: "v2.0.0",
		Newer:      true,
		URL:        "https://example/v2.0.0",
	}

	next, cmd := app.update(msg)
	app = next
	if !app.updateAvailable || app.updateLatest != "v2.0.0" || app.updateURL != "https://example/v2.0.0" {
		t.Errorf("expected update fields populated, got %+v", app)
	}
	if app.chatModel.updateLatest != "v2.0.0" {
		t.Errorf("expected chatModel.updateLatest to be set, got %q", app.chatModel.updateLatest)
	}
	if app.prefs.LastUpdateCheck != 1700000000 || app.prefs.LatestSeenVersion != "v2.0.0" {
		t.Errorf("expected prefs to be persisted on the model, got %+v", app.prefs)
	}
	if cmd == nil {
		t.Fatal("expected a cmd to persist preferences")
	}
	cmd() // exercise the save path; SavePreferences ignores errors here

	loaded := config.LoadPreferences()
	if loaded.LatestSeenVersion != "v2.0.0" || loaded.LastUpdateCheck != 1700000000 {
		t.Errorf("expected SavePreferences to round-trip the new fields, got %+v", loaded)
	}
}

func TestUpdateCheckDoneMsg_NewerFalseSuppressesBadge(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)

	app := AppModel{prefs: config.DefaultPreferences()}
	msg := updateCheckDoneMsg{At: 1700000000, LatestSeen: "v1.0.0", Newer: false}
	next, _ := app.update(msg)
	if next.updateAvailable {
		t.Error("badge must not appear when Newer is false")
	}
	if next.chatModel.updateLatest != "" {
		t.Error("chatModel.updateLatest must remain empty when Newer is false")
	}
}

func enabledPrefs() config.Preferences {
	prefs := config.DefaultPreferences()
	return prefs
}
