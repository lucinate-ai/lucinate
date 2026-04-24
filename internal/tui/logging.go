package tui

import (
	"log"
	"os"
)

var debugLog *log.Logger

func init() {
	f, err := os.OpenFile("/tmp/lucinate-events.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err == nil {
		debugLog = log.New(f, "", log.LstdFlags|log.Lmicroseconds)
	}
}

func logEvent(format string, args ...any) {
	if debugLog != nil {
		debugLog.Printf(format, args...)
	}
}
