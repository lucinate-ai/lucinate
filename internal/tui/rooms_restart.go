package tui

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/lucinate-ai/lucinate/internal/rooms"
)

// /restart [fresh|with-summary] — the roster, in a new room.
//
// What a room is: the people in it and the model each of them answers with.
// What accumulates: the id, the transcript, the context a few days of turns
// leave behind. A restart keeps the first and drops the rest, so a heavy room
// starts over without the operator re-inviting anyone. The old room is left
// exactly as it was — removing it is the browse screen's x, a separate
// decision with its own confirmation.

// restartDoneMsg carries the room a restart created, plus the hand-over
// message for with-summary, back into the model.
type restartDoneMsg struct {
	room    rooms.Room
	summary string
	// seatErr is a model that did not make it across: the room is open, but
	// a member would not seat. It rides with the room instead of replacing
	// it — a half-successful restart still leaves the roster together.
	seatErr error
	err     error
}

// cmdRestart validates /restart and arms it.
func (m roomsModel) cmdRestart(args []string) (roomsModel, tea.Cmd, bool) {
	m.composer = ""
	m.err = nil
	withSummary := false
	if len(args) > 0 {
		if len(args) > 1 {
			m.err = fmt.Errorf("/restart takes one mode, got %d", len(args))
			return m, nil, true
		}
		switch strings.ToLower(args[0]) {
		case "fresh":
			// the default, said out loud
		case "with-summary", "summary", "sum":
			withSummary = true
		default:
			m.err = fmt.Errorf("/restart %s is not a mode — use /restart [fresh|with-summary]", args[0])
			return m, nil, true
		}
	}
	if m.openRoom == nil || len(m.openRoom.Members) == 0 {
		m.err = fmt.Errorf("/restart needs an open room: it restarts that room's roster")
		return m, nil, true
	}

	oldID := m.openRoomID
	oldName := strings.TrimSpace(m.openRoom.Name)
	members := append([]rooms.Member(nil), m.openRoom.Members...)
	models := make(map[string]string, len(m.memberModels))
	for handle, model := range m.memberModels {
		models[handle] = model
	}

	var summary string
	if withSummary {
		summary = rooms.RestartSummary(oldID, oldName,
			m.roomDirIfExists(m.orientationFor(oldID), oldID), m.page.Events)
	}

	m.status = "restarting " + oldID + "…"
	return m, m.restartCmd(oldID, oldName, members, models, summary), true
}

// restartCmd creates the new room and seats the models on it in one command,
// so the room is never on screen with a roster that has only half moved.
func (m roomsModel) restartCmd(oldID, oldName string, members []rooms.Member, models map[string]string, summary string) tea.Cmd {
	conn, token := m.conn, m.token
	// The new id derives from the old one and can never equal it: the old
	// room still exists with this exact roster, so asking for its id would
	// adopt it back instead of starting over.
	roomID := rooms.FreshRoomID(oldID)
	// The directory exists before the gateway hears about the room — under
	// the same base the first message will name.
	_, dirErr := m.orientationFor(roomID).WithRoomDir(roomID)
	name := oldName
	if name == "" {
		name = roomID
	}
	return func() tea.Msg {
		if conn == nil {
			return restartDoneMsg{err: fmt.Errorf("no active connection")}
		}
		if dirErr != nil {
			return restartDoneMsg{err: dirErr}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c, err := rooms.DialBaseURL(ctx, conn.URL, token)
		if err != nil {
			return restartDoneMsg{err: err}
		}
		defer c.Close()
		room, err := c.Create(ctx, roomID, name, members)
		if err != nil {
			return restartDoneMsg{err: err}
		}
		var seatErr error
		if len(models) > 0 {
			seatErr = seatModels(ctx, c, room.RoomID, models)
		}
		return restartDoneMsg{room: room, summary: summary, seatErr: seatErr}
	}
}

// seatModels puts the session's models back on the new room's members. The
// first failure is returned: the room opened regardless, so a model that
// would not seat is reported rather than fatal.
func seatModels(ctx context.Context, c *rooms.Client, roomID string, models map[string]string) error {
	supported, err := c.SupportsMemberModel(ctx)
	if err != nil {
		return fmt.Errorf("checking for member models: %w", err)
	}
	if !supported {
		return fmt.Errorf("this gateway cannot carry member models into a new room — seat them again with /model")
	}
	handles := make([]string, 0, len(models))
	for handle := range models {
		handles = append(handles, handle)
	}
	sort.Strings(handles)
	for _, handle := range handles {
		if _, err := c.SetMemberModel(ctx, roomID, handle, models[handle], ""); err != nil {
			return fmt.Errorf("model for @%s: %w", handle, err)
		}
	}
	return nil
}

// roomDirIfExists reports where a room's shared directory is, if it is on
// disk under the base this orientation resolves — a summary that promised a
// directory nobody could open would be worse than no pointer at all.
func (m roomsModel) roomDirIfExists(orient rooms.Orientation, roomID string) string {
	base := strings.TrimSpace(orient.Path)
	if base == "" {
		b, err := rooms.RoomsBase()
		if err != nil {
			return ""
		}
		base = b
	}
	dir := rooms.RoomDir(base, roomID)
	if _, err := os.Stat(dir); err != nil {
		return ""
	}
	return dir
}
