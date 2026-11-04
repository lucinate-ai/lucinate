package rooms

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lucinate-ai/lucinate/internal/backend/hermes/rpc"
)

// RoomIDConflictCode is the gateway's refusal to (re)create a room id.
//
// Measured on the live gateway (hermes-local, 2026-09-28):
//
//	disbanded id, same roster → 4110 "room_id belongs to a disbanded room"
//	live id, different roster → 4110 "room_id already exists with different state"
//	live id, identical roster → success (idempotent re-adopt)
//
// Both messages are one situation from the caller's point of view: this id is
// not available for the roster being asked for, and no amount of retrying the
// same id will change that. Every start the user makes — a fresh room after a
// disband, a predefined room whose id was disbanded — died here before any
// roster or preset logic could run.
const RoomIDConflictCode = 4110

// maxCreateAttempts bounds how often Create mints a replacement id: the first
// try plus this many mints. It exists so a gateway that refuses everything
// surfaces its error instead of looping.
const maxCreateAttempts = 3

// IsRoomIDConflict reports whether err is the gateway refusing the room id
// itself. Only that class of error may be retried with another id: a roster
// refusal or a dropped socket means something else entirely.
func IsRoomIDConflict(err error) bool {
	var rpcErr *rpc.RPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == RoomIDConflictCode
}

// FreshRoomID derives a replacement for an id the gateway refused:
//
//	kowal-matt → kowal-matt-190530
//
// The suffix is the time of day, which keeps the id readable and typed by
// hand the way room ids are, and unique across attempts made in different
// seconds. Callers pass a Slug, so the result stays a valid room id; the base
// is trimmed anyway because a room id is also a directory name.
func FreshRoomID(base string) string {
	base = strings.Trim(strings.TrimSpace(base), "-")
	if base == "" {
		base = "pokoj"
	}
	if len(base) > 120 {
		base = base[:120]
	}
	return fmt.Sprintf("%s-%s", base, time.Now().Format("150405"))
}
