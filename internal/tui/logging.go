package tui

import (
	"fmt"
	"log/slog"
)

// logEvent records a TUI lifecycle event at debug level on the
// process-wide slog.Default logger configured by internal/logging.
//
// Call sites pass a printf-style format and args; that pattern predates
// the move to slog and is preserved here so they don't have to migrate
// in lockstep. New code should prefer slog.Debug directly with
// structured key=value attrs.
func logEvent(format string, args ...any) {
	if !slog.Default().Enabled(nil, slog.LevelDebug) {
		return
	}
	slog.Debug(fmt.Sprintf(format, args...))
}
