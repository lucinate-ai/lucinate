package tui

import (
	"context"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/lucinate-ai/lucinate/internal/config"
	"github.com/lucinate-ai/lucinate/internal/update"
	"github.com/lucinate-ai/lucinate/internal/version"
)

// updateCheckURLEnvVar lets developers point the check at a local
// httptest server while iterating. Unset in production — the default
// manifest URL is the only thing real users hit.
const updateCheckURLEnvVar = "LUCINATE_UPDATE_MANIFEST_URL"

// updateCheckInterval is the minimum gap between successive checks.
// Lucinate is a CLI; users may launch it dozens of times per day.
const updateCheckInterval = 24 * time.Hour

// updateCheckCmd returns a bubbletea Cmd that performs a single,
// non-blocking update check. It returns nil when the check should be
// skipped — env-var opt-out, the user's preference, the daily
// rate-limit, or a non-stable build.
//
// The Cmd emits a single updateCheckDoneMsg with Newer=true only when
// a newer release exists *and* the user has not already seen it on a
// previous launch (suppressed via prefs.LatestSeenVersion).
func updateCheckCmd(prefs config.Preferences) tea.Cmd {
	if update.Disabled() {
		return nil
	}
	if !prefs.UpdateChecksEnabled() {
		return nil
	}
	if time.Now().Unix()-prefs.LastUpdateCheck < int64(updateCheckInterval.Seconds()) {
		return nil
	}
	manifestURL := os.Getenv(updateCheckURLEnvVar)
	if manifestURL == "" {
		manifestURL = update.DefaultManifestURL
	}
	current := version.Version
	lastSeen := prefs.LatestSeenVersion

	return func() tea.Msg {
		res, _ := update.Check(context.Background(), manifestURL, current)
		done := updateCheckDoneMsg{At: time.Now().Unix()}
		if res == nil {
			return done
		}
		done.LatestSeen = res.Latest
		done.URL = res.URL
		// Suppress the badge if the user already noticed this version
		// last time the binary launched.
		done.Newer = res.Newer && res.Latest != lastSeen
		return done
	}
}
