package tui

import (
	"encoding/json"
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	"github.com/charmbracelet/x/ansi"
)

func TestHasStreamingMessage(t *testing.T) {
	tests := []struct {
		name     string
		messages []chatMessage
		want     bool
	}{
		{"empty", nil, false},
		{"no streaming", []chatMessage{
			{role: "user", content: "hi"},
			{role: "assistant", content: "done", streaming: false},
		}, false},
		{"streaming assistant", []chatMessage{
			{role: "user", content: "hi"},
			{role: "assistant", content: "…", streaming: true},
		}, true},
		{"streaming in middle", []chatMessage{
			{role: "assistant", content: "a", streaming: true},
			{role: "assistant", content: "b", streaming: false},
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestChatModel()
			m.messages = tt.messages
			if got := m.hasStreamingMessage(); got != tt.want {
				t.Errorf("hasStreamingMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEnsureSpinnerTicking_StartsOnFirstCall(t *testing.T) {
	m := newTestChatModel()

	cmd := m.ensureSpinnerTicking()

	if cmd == nil {
		t.Fatal("expected non-nil tick cmd on first call")
	}
	if !m.spinnerTicking {
		t.Error("expected spinnerTicking = true after first call")
	}
}

func TestEnsureSpinnerTicking_NoOpWhenAlreadyTicking(t *testing.T) {
	m := newTestChatModel()
	m.spinnerTicking = true

	cmd := m.ensureSpinnerTicking()

	if cmd != nil {
		t.Error("expected nil cmd when a tick is already scheduled")
	}
	if !m.spinnerTicking {
		t.Error("spinnerTicking should remain true")
	}
}

func TestHandleEvent_DeltaStartsSpinner(t *testing.T) {
	m := newTestChatModel()
	m.messages = []chatMessage{{role: "user", content: "hello"}}

	cmd := m.handleEvent(makeChatEvent("delta", "run1", 1, json.RawMessage(`"First"`)))

	if cmd == nil {
		t.Fatal("expected spinner tick cmd when new streaming assistant message is created")
	}
	if !m.spinnerTicking {
		t.Error("expected spinnerTicking = true after first delta")
	}
}

func TestHandleEvent_DeltaDoesNotRestartSpinnerForExistingStream(t *testing.T) {
	m := newTestChatModel()
	m.spinnerTicking = true
	m.messages = []chatMessage{
		{role: "user", content: "hello"},
		{role: "assistant", content: "par", streaming: true},
	}

	cmd := m.handleEvent(makeChatEvent("delta", "run1", 2, json.RawMessage(`"partial"`)))

	if cmd != nil {
		t.Error("expected nil cmd for subsequent delta on an existing streaming message")
	}
	if !m.spinnerTicking {
		t.Error("spinnerTicking should remain true while the existing tick chain runs")
	}
}

func TestUpdate_SpinnerTickAdvancesFrameWhileStreaming(t *testing.T) {
	m := *newTestChatModel()
	m.spinnerTicking = true
	m.messages = []chatMessage{
		{role: "assistant", content: "working", streaming: true},
	}
	startFrame := m.spinnerFrame

	updated, cmd := m.Update(spinnerTickMsg{})

	if cmd == nil {
		t.Error("expected next tick cmd while streaming")
	}
	if updated.spinnerFrame != (startFrame+1)%len(spinnerFrames) {
		t.Errorf("spinnerFrame = %d, want %d", updated.spinnerFrame, (startFrame+1)%len(spinnerFrames))
	}
	if !updated.spinnerTicking {
		t.Error("spinnerTicking should remain true while streaming")
	}
}

func TestUpdate_SpinnerTickWrapsAroundFrameIndex(t *testing.T) {
	m := *newTestChatModel()
	m.spinnerTicking = true
	m.spinnerFrame = len(spinnerFrames) - 1
	m.messages = []chatMessage{
		{role: "assistant", content: "x", streaming: true},
	}

	updated, _ := m.Update(spinnerTickMsg{})

	if updated.spinnerFrame != 0 {
		t.Errorf("expected frame to wrap to 0, got %d", updated.spinnerFrame)
	}
}

func TestUpdate_SpinnerTickStopsWhenNoStreamingMessage(t *testing.T) {
	m := *newTestChatModel()
	m.spinnerTicking = true
	m.messages = []chatMessage{
		{role: "user", content: "hi"},
		{role: "assistant", content: "done", streaming: false},
	}

	updated, cmd := m.Update(spinnerTickMsg{})

	if cmd != nil {
		t.Error("expected nil cmd once no message is streaming")
	}
	if updated.spinnerTicking {
		t.Error("expected spinnerTicking = false after tick sees no streaming messages")
	}
}

func TestUpdateViewport_RendersCurrentSpinnerFrame(t *testing.T) {
	vp := viewport.New()
	vp.SetWidth(40)
	vp.SetHeight(10)
	m := &chatModel{
		viewport:  vp,
		width:     40,
		agentName: "main",
		messages: []chatMessage{
			{role: "assistant", content: "hi", streaming: true},
		},
		spinnerFrame: 3,
	}

	m.updateViewport()
	view := ansi.Strip(m.viewport.View())

	want := spinnerFrames[3]
	if !strings.Contains(view, want) {
		t.Errorf("expected viewport to contain frame %q, view was:\n%s", want, view)
	}
	// Sanity: the old static underscore placeholder should be gone.
	if strings.Contains(view, "hi_") {
		t.Errorf("viewport should no longer render the legacy underscore placeholder:\n%s", view)
	}
}

func TestUpdateViewport_SpinnerNotRenderedForFinalisedMessage(t *testing.T) {
	vp := viewport.New()
	vp.SetWidth(40)
	vp.SetHeight(10)
	m := &chatModel{
		viewport:  vp,
		width:     40,
		agentName: "main",
		messages: []chatMessage{
			{role: "assistant", content: "hi", streaming: false},
		},
		spinnerFrame: 3,
	}

	m.updateViewport()
	view := ansi.Strip(m.viewport.View())

	if strings.Contains(view, spinnerFrames[3]) {
		t.Errorf("spinner should not appear on finalised message, view was:\n%s", view)
	}
}
