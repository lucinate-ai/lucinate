package tui

import (
	"fmt"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// connectionLabel returns a one-line string used in the chat
// header: "<name>" if set, falling back to the URL. Empty when the
// connection is nil so legacy embedders render unchanged.
func connectionLabel(conn *config.Connection) string {
	if conn == nil {
		return ""
	}
	if conn.Name != "" {
		return conn.Name
	}
	return conn.URL
}

// renderConnectionBanner returns the dim status row shown above the
// agent picker, session browser, and config view so the user always
// knows which connection is in scope. The chat view folds the same
// information into its own coloured header — it doesn't use this
// helper. The connections picker itself also skips the banner since
// the user is in the act of choosing a connection there.
//
// Returns the empty string when conn is nil so legacy embedders
// without a connections store render unchanged.
func renderConnectionBanner(conn *config.Connection) string {
	if conn == nil {
		return ""
	}
	name := conn.Name
	if name == "" {
		name = conn.URL
	}
	body := fmt.Sprintf("  Connection: %s · %s", name, conn.Type.Label())
	return connBannerStyle.Render(body) + "\n"
}
