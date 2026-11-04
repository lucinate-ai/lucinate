package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/lucinate-ai/lucinate/internal/rooms"
)

// The room header has to keep the discussion budget visible even when the
// roster is wide. These tests drive the real model and assert on the rendered
// view, because the failure they guard against is a layout one: a full label
// appended to a row that is already full, and then cut off by the terminal.

func sixMemberHeaderModel(t *testing.T) roomsModel {
	t.Helper()
	m := v2Model(t)
	profiles := []string{"anna", "borys", "cezary", "dorota", "edward", "franek"}
	room := liveRoom("sztab", "Sztab", profiles...)
	m.openRoom = &room
	// One round, two member messages: `round 1/3 · 2/10 messages`.
	m.page.Events = []rooms.Event{
		ruleEv(1, "message.user", "", "co robimy?", -1),
		ruleEv(2, "message.member", "matt", "a", 0),
		ruleEv(3, "message.member", "kowal", "b", 0),
	}
	return m
}

func TestRoomsHeader_SixMembersKeepHandlesAndBudgetInline(t *testing.T) {
	m := sixMemberHeaderModel(t)
	m.setSize(100, 30)

	out := ansi.Strip(m.View())

	for _, profile := range []string{"anna", "borys", "cezary", "dorota", "edward", "franek"} {
		if handle := "@" + rooms.HandleFor(profile); !strings.Contains(out, handle) {
			t.Errorf("header is missing %s:\n%s", handle, out)
		}
	}
	if !strings.Contains(out, "1/3 · 2/10") {
		t.Errorf("header lost the discussion budget:\n%s", out)
	}
	if strings.Contains(out, "(profile default)") {
		t.Errorf("header should fall back to bare handles, not pad each one with the model default:\n%s", out)
	}
}

func TestRoomsHeader_NarrowRoomMovesBudgetToItsOwnRow(t *testing.T) {
	m := sixMemberHeaderModel(t)
	m.setSize(60, 30)

	out := ansi.Strip(m.View())

	if !strings.Contains(out, "1/3 · 2/10") {
		t.Errorf("budget vanished from a narrow header:\n%s", out)
	}
	if n := strings.Count(out, "\n"); n > 30 {
		t.Errorf("view is %d lines for a 30-line terminal:\n%s", n, out)
	}
	if !strings.Contains(out, "you › ") {
		t.Errorf("composer pushed off-screen by a wrapped header:\n%s", out)
	}
}

func TestRoomsHeader_TwoMembersKeepTheirModels(t *testing.T) {
	m := v2Model(t)
	m.memberModels = map[string]string{"matt": "gpt-5-mini", "kowal": "claude-sonnet-4"}

	out := ansi.Strip(m.View())

	if !strings.Contains(out, "@matt (gpt-5-mini)") {
		t.Errorf("header no longer shows the member's model:\n%s", out)
	}
	if !strings.Contains(out, "@kowal (claude-sonnet-4)") {
		t.Errorf("header no longer shows the second member's model:\n%s", out)
	}
}
