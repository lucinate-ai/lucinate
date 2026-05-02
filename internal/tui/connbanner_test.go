package tui

import (
	"strings"
	"testing"

	"github.com/a3tai/openclaw-go/protocol"

	"github.com/lucinate-ai/lucinate/internal/config"
)

func TestRenderConnectionBanner(t *testing.T) {
	t.Run("nil renders nothing", func(t *testing.T) {
		if got := renderConnectionBanner(nil); got != "" {
			t.Errorf("expected empty banner for nil conn, got %q", got)
		}
	})

	t.Run("named connection includes name and type", func(t *testing.T) {
		conn := &config.Connection{Name: "home", Type: config.ConnTypeOpenClaw}
		got := renderConnectionBanner(conn)
		if !strings.Contains(got, "home") || !strings.Contains(got, "OpenClaw") {
			t.Errorf("banner missing name/type: %q", got)
		}
	})

	t.Run("nameless connection falls back to URL", func(t *testing.T) {
		conn := &config.Connection{URL: "https://gw.example.com", Type: config.ConnTypeOpenAI}
		got := renderConnectionBanner(conn)
		if !strings.Contains(got, "gw.example.com") {
			t.Errorf("banner missing URL fallback: %q", got)
		}
	})
}

func TestSelectModel_RendersConnectionBanner(t *testing.T) {
	conn := &config.Connection{Name: "home", Type: config.ConnTypeOpenClaw}
	m := newSelectModel(newFakeBackend(), false, false, conn, false)
	m.setSize(120, 40)
	// Move out of the loading state so View() renders the list +
	// banner rather than the loading placeholder.
	m, _ = m.Update(agentsLoadedMsg{result: &protocol.AgentsListResult{
		DefaultID: "main",
		MainKey:   "main",
	}})
	if got := m.View(); !strings.Contains(got, "home") || !strings.Contains(got, "OpenClaw") {
		t.Errorf("agent picker did not surface active connection in banner:\n%s", got)
	}
}
