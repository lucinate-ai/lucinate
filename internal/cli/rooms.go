package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/lucinate-ai/lucinate/internal/config"
	"github.com/lucinate-ai/lucinate/internal/rooms"
)

// runRooms dispatches the `lucinate rooms` family. Rooms are Hermes
// hosted "Bot Mode" group chats: a durable transcript with a frozen
// roster of 2-6 Hermes profiles that answer your messages. Every
// subcommand talks to the gateway over the same `/api/ws` endpoint the
// chat backend uses, so a stored Hermes connection configures both.
func runRooms(ctx context.Context, args []string, stdout io.Writer) error {
	out := stdout
	if out == nil {
		out = os.Stdout
	}
	if len(args) == 0 {
		printRoomsUsage(out)
		return nil
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list", "ls":
		return roomsList(ctx, rest, out)
	case "caps", "capabilities":
		return roomsCaps(ctx, rest, out)
	case "profiles":
		return roomsProfiles(rest, out)
	case "presets":
		return roomsPresets(rest, out)
	case "create":
		return roomsCreate(ctx, rest, out)
	case "from":
		return roomsFrom(ctx, rest, out)
	case "send":
		return roomsSend(ctx, rest, out)
	case "log":
		return roomsLog(ctx, rest, out)
	case "state":
		return roomsState(ctx, rest, out)
	case "stop":
		return roomsStop(ctx, rest, out)
	case "disband":
		return roomsDisband(ctx, rest, out)
	case "help", "-h", "--help":
		printRoomsUsage(out)
		return nil
	default:
		printRoomsUsage(out)
		return fmt.Errorf("rooms: unknown subcommand %q", sub)
	}
}

// ── shared plumbing ──────────────────────────────────────────────────────────

// hermesConnection picks the Hermes connection a rooms command should
// use: the named one, else the most recently used Hermes connection.
func hermesConnection(name string) (*config.Connection, string, error) {
	store := config.LoadConnections()
	if name != "" {
		conn := store.Find(name)
		if conn == nil {
			return nil, "", fmt.Errorf("no saved connection named %q (try `lucinate rooms list --connection <name>`)", name)
		}
		if conn.Type != config.ConnTypeHermes {
			return nil, "", fmt.Errorf("connection %q is a %s connection; rooms need a Hermes (dashboard) connection",
				conn.Name, conn.Type.Label())
		}
		return conn, config.GetAPIKey(conn.ID), nil
	}
	var best *config.Connection
	for i := range store.Connections {
		conn := &store.Connections[i]
		if conn.Type != config.ConnTypeHermes {
			continue
		}
		if best == nil || conn.LastUsed.After(best.LastUsed) {
			best = conn
		}
	}
	if best == nil {
		names := make([]string, 0, len(store.Connections))
		for _, c := range store.Connections {
			names = append(names, c.Name)
		}
		sort.Strings(names)
		if len(names) == 0 {
			return nil, "", errors.New("no saved connections; add a Hermes connection pointing at `hermes dashboard` (default http://localhost:9119) first")
		}
		return nil, "", fmt.Errorf("no Hermes connection saved (have: %s); rooms need one pointing at `hermes dashboard`",
			strings.Join(names, ", "))
	}
	return best, config.GetAPIKey(best.ID), nil
}

// roomsDial resolves the connection and dials its gateway.
func roomsDial(ctx context.Context, connection string) (*rooms.Client, *config.Connection, error) {
	conn, token, err := hermesConnection(connection)
	if err != nil {
		return nil, nil, err
	}
	client, err := rooms.DialBaseURL(ctx, conn.URL, token)
	if err != nil {
		return nil, nil, fmt.Errorf("connect to %s: %w (is `hermes dashboard` running?)", conn.URL, err)
	}
	return client, conn, nil
}

func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func splitProfiles(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// reorderFlagsFirst moves flag tokens (with their values) ahead of the
// positional arguments, so `rooms log <room> --follow` parses the same
// as `rooms log --follow <room>`.
//
// Go's flag package stops parsing at the first non-flag argument, so
// without this a trailing flag is silently dropped — which reads as a
// flag that simply does not work. A `--` escape is re-inserted when a
// positional itself starts with a dash, so the escape keeps working.
func reorderFlagsFirst(fs *flag.FlagSet, args []string) []string {
	takesValue := make(map[string]bool)
	fs.VisitAll(func(f *flag.Flag) {
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			return
		}
		takesValue[f.Name] = true
	})
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) > 1 && strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if strings.Contains(name, "=") {
				continue // --flag=value carries its own value
			}
			if takesValue[name] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	out := flags
	for _, p := range positional {
		if strings.HasPrefix(p, "-") {
			out = append(out, "--")
			break
		}
	}
	return append(out, positional...)
}

// ── read-only commands ───────────────────────────────────────────────────────

func roomsList(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("rooms list", flag.ContinueOnError)
	fs.SetOutput(out)
	var connection string
	var all, asJSON bool
	fs.StringVar(&connection, "connection", "", "saved Hermes connection name or ID")
	fs.StringVar(&connection, "c", "", "short for --connection")
	fs.BoolVar(&all, "all", false, "include disbanded rooms")
	fs.BoolVar(&asJSON, "json", false, "print JSON")
	fs.Usage = func() { printRoomsUsage(fs.Output()) }
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errUsage
		}
		return err
	}
	client, conn, err := roomsDial(ctx, connection)
	if err != nil {
		return err
	}
	defer client.Close()
	list, err := client.List(ctx, all)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(out, list)
	}
	fmt.Fprintf(out, "%s — %s\n\n", conn.Name, conn.URL)
	if len(list) == 0 {
		fmt.Fprintln(out, "no rooms yet. Create one:")
		fmt.Fprintln(out, "  lucinate rooms create --name Sztab --profiles matt,kowal")
		fmt.Fprintln(out, "or start from a predefined room:")
		fmt.Fprintln(out, "  lucinate rooms presets")
		return nil
	}
	for _, r := range list {
		marker := " "
		if r.DisbandedAt != nil {
			marker = "x"
		}
		fmt.Fprintf(out, "%s %-24s rev=%-3d %s\n", marker, r.RoomID, r.Revision, r.Name)
		for _, m := range r.Members {
			fmt.Fprintf(out, "      @%-16s %s\n", m.Handle, m.Profile)
		}
	}
	return nil
}

func roomsCaps(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("rooms caps", flag.ContinueOnError)
	fs.SetOutput(out)
	var connection string
	var asJSON bool
	fs.StringVar(&connection, "connection", "", "saved Hermes connection name or ID")
	fs.StringVar(&connection, "c", "", "short for --connection")
	fs.BoolVar(&asJSON, "json", false, "print JSON")
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errUsage
		}
		return err
	}
	client, _, err := roomsDial(ctx, connection)
	if err != nil {
		return err
	}
	defer client.Close()
	caps, err := client.Capabilities(ctx)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(out, caps)
	}
	fmt.Fprintf(out, "protocol   v%d\n", caps.ProtocolVersion)
	fmt.Fprintf(out, "driver     %t\n", caps.Driver)
	fmt.Fprintf(out, "gateway    %s\n", caps.AuthorityGatewayID)
	fmt.Fprintf(out, "room_link  enabled=%t profile=%s\n", caps.RoomLink.Enabled, caps.RoomLink.Profile)
	fmt.Fprintf(out, "log limit  %d\n", caps.MaxLogLimit)
	fmt.Fprintf(out, "methods    %d: %s\n", len(caps.Methods), strings.Join(caps.Methods, " "))
	return nil
}

func roomsProfiles(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("rooms profiles", flag.ContinueOnError)
	fs.SetOutput(out)
	var asJSON bool
	fs.BoolVar(&asJSON, "json", false, "print JSON")
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errUsage
		}
		return err
	}
	profiles, err := rooms.DiscoverLocalProfiles()
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(out, profiles)
	}
	fmt.Fprintf(out, "local Hermes profiles (%s) — the invite list:\n\n", rooms.ResolveInstallHome())
	for _, p := range profiles {
		fmt.Fprintf(out, "  %s\n", p)
	}
	fmt.Fprintf(out, "\ninvite up to %d (at least %d) per room:\n", rooms.MaxMembers, rooms.MinMembers)
	fmt.Fprintln(out, "  lucinate rooms create --name Sztab --profiles matt,kowal")
	return nil
}

func roomsPresets(args []string, out io.Writer) error {
	fs := flag.NewFlagSet("rooms presets", flag.ContinueOnError)
	fs.SetOutput(out)
	var asJSON bool
	fs.BoolVar(&asJSON, "json", false, "print JSON")
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errUsage
		}
		return err
	}
	path, err := rooms.DefaultPresetsPath()
	if err != nil {
		return err
	}
	store, err := rooms.LoadPresets(path)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(out, store)
	}
	if len(store.Presets) == 0 {
		fmt.Fprintf(out, "no predefined rooms in %s\n\n", path)
		fmt.Fprintln(out, "Save one while creating a room:")
		fmt.Fprintln(out, "  lucinate rooms create --name Sztab --profiles matt,kowal --save")
		return nil
	}
	fmt.Fprintf(out, "predefined rooms (%s):\n\n", path)
	for _, p := range store.Presets {
		names := make([]string, 0, len(p.Members))
		for _, m := range p.Members {
			names = append(names, m.Profile)
		}
		fmt.Fprintf(out, "  %-20s room=%-20s %s\n", p.Name, p.RoomID, strings.Join(names, ","))
	}
	fmt.Fprintln(out, "\nstart one with: lucinate rooms from <name>")
	return nil
}

// ── write commands ───────────────────────────────────────────────────────────

func roomsCreate(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("rooms create", flag.ContinueOnError)
	fs.SetOutput(out)
	var connection, name, profiles, roomID, thread, worktree string
	var save, asJSON bool
	fs.StringVar(&connection, "connection", "", "saved Hermes connection name or ID")
	fs.StringVar(&connection, "c", "", "short for --connection")
	fs.StringVar(&name, "name", "", "room name (required)")
	fs.StringVar(&profiles, "profiles", "", "comma-separated Hermes profiles to invite (2-6, required)")
	fs.StringVar(&roomID, "room-id", "", "room id (defaults to a slug of --name)")
	fs.StringVar(&thread, "thread", rooms.DefaultThreadID, "conversation thread id")
	fs.StringVar(&worktree, "worktree", "", "Orca worktree selector this room works in (default: ORCA_WORKTREE_ID)")
	fs.BoolVar(&save, "save", false, "also save this roster as a predefined room")
	fs.BoolVar(&asJSON, "json", false, "print JSON")
	fs.Usage = func() { printRoomsUsage(fs.Output()) }
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errUsage
		}
		return err
	}
	if strings.TrimSpace(name) == "" || strings.TrimSpace(profiles) == "" {
		fs.Usage()
		return errors.New("rooms create: --name and --profiles are required")
	}
	members := rooms.RosterFor(splitProfiles(profiles))
	warnIfNotLocal(members)
	if err := rooms.ValidateRoster(members); err != nil {
		return err
	}
	if roomID == "" {
		roomID = rooms.Slug(name)
	}
	if strings.TrimSpace(worktree) == "" {
		worktree = rooms.OrientationFromEnv().Worktree
	}
	// The room's shared directory exists before the room does: members write
	// handoffs there from their very first turn, and the first message hands
	// out exactly this path.
	if _, err := rooms.OrientationFromWorktree(worktree).WithRoomDir(roomID); err != nil {
		return err
	}

	client, conn, err := roomsDial(ctx, connection)
	if err != nil {
		return err
	}
	defer client.Close()
	room, err := client.Create(ctx, roomID, name, members)
	if err != nil {
		return err
	}
	// groups.create may have minted a new id (the requested one was
	// tombstoned or taken), so the directory that exists is the one under
	// the id the room actually got.
	orient, err := rooms.OrientationFromWorktree(worktree).WithRoomDir(room.RoomID)
	if err != nil {
		return err
	}
	if save {
		if err := savePreset(rooms.Preset{Name: name, RoomID: room.RoomID, ThreadID: thread,
			Members: members, Worktree: worktree}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: room created but preset not saved: %v\n", err)
		}
	}
	if asJSON {
		return writeJSON(out, room)
	}
	fmt.Fprintf(out, "room %s (%s) on %s\n", room.RoomID, room.Name, conn.Name)
	fmt.Fprintf(out, "  shared directory: %s\n", orient.RoomDir)
	for _, m := range room.Members {
		fmt.Fprintf(out, "  @%-16s %s\n", m.Handle, m.Profile)
	}
	if orient := rooms.OrientationFromWorktree(worktree); !orient.Empty() {
		fmt.Fprintf(out, "\nproject context sent to the members on the first message: %s\n", worktree)
	} else {
		fmt.Fprintln(out, "\nno project context — pass --worktree (or run from an Orca terminal) so the")
		fmt.Fprintln(out, "members know which project they are working in and can drive its browser")
	}
	fmt.Fprintf(out, "\nsay something:\n  lucinate rooms send %s \"co robimy?\"\n", room.RoomID)
	return nil
}

func roomsFrom(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("rooms from", flag.ContinueOnError)
	fs.SetOutput(out)
	var connection string
	var save, asJSON bool
	fs.StringVar(&connection, "connection", "", "saved Hermes connection name or ID")
	fs.StringVar(&connection, "c", "", "short for --connection")
	fs.BoolVar(&save, "save", false, "update the stored preset after creating")
	fs.BoolVar(&asJSON, "json", false, "print JSON")
	fs.Usage = func() { printRoomsUsage(fs.Output()) }
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errUsage
		}
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return errors.New("rooms from: missing preset name")
	}
	path, err := rooms.DefaultPresetsPath()
	if err != nil {
		return err
	}
	store, err := rooms.LoadPresets(path)
	if err != nil {
		return err
	}
	preset := store.Find(rest[0])
	if preset == nil {
		names := store.Names()
		if len(names) == 0 {
			return fmt.Errorf("no predefined room named %q (none saved yet — `lucinate rooms create --name X --profiles a,b --save`)", rest[0])
		}
		return fmt.Errorf("no predefined room named %q (have: %s)", rest[0], strings.Join(names, ", "))
	}
	if err := rooms.ValidateRoster(preset.Members); err != nil {
		return fmt.Errorf("predefined room %q is invalid: %w", preset.Name, err)
	}
	// The directory exists before the gateway hears about the room, under the
	// project the preset names.
	if _, err := rooms.OrientationFromWorktree(preset.Worktree).WithRoomDir(preset.RoomID); err != nil {
		return err
	}
	client, conn, err := roomsDial(ctx, connection)
	if err != nil {
		return err
	}
	defer client.Close()
	room, err := client.Create(ctx, preset.RoomID, preset.Name, preset.Members)
	if err != nil {
		return err
	}
	// Snapshot before touching the store: Remove compacts the slice, and a
	// preset pointer into it would start naming its neighbour.
	stored := *preset
	if room.RoomID != stored.RoomID {
		// The id this preset named was tombstoned or taken, so groups.create
		// minted a new one. The preset follows the room it now starts —
		// otherwise every start mints yet another room.
		if save {
			stored.RoomID = room.RoomID
			store.Remove(stored.Name)
			if err := store.Add(stored); err != nil {
				fmt.Fprintf(os.Stderr, "warning: preset not updated: %v\n", err)
			} else if err := rooms.SavePresets(path, store); err != nil {
				fmt.Fprintf(os.Stderr, "warning: preset not saved: %v\n", err)
			}
		} else if changed, err := rooms.UpdatePresetRoomID(path, stored.Name, room.RoomID); err != nil {
			fmt.Fprintf(os.Stderr, "warning: preset still points at %s: %v\n", stored.RoomID, err)
		} else if changed {
			fmt.Fprintf(out, "  preset updated: room id %s → %s\n", stored.RoomID, room.RoomID)
		}
	} else if save {
		store.Remove(stored.Name)
		if err := store.Add(stored); err != nil {
			fmt.Fprintf(os.Stderr, "warning: preset not updated: %v\n", err)
		} else if err := rooms.SavePresets(path, store); err != nil {
			fmt.Fprintf(os.Stderr, "warning: preset not saved: %v\n", err)
		}
	}
	// The directory that exists is the one under the id the room actually
	// got — after a minted id, the requested one was never used.
	orient, err := rooms.OrientationFromWorktree(stored.Worktree).WithRoomDir(room.RoomID)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(out, room)
	}
	fmt.Fprintf(out, "room %s (%s) on %s — from preset %q\n", room.RoomID, room.Name, conn.Name, stored.Name)
	fmt.Fprintf(out, "  shared directory: %s\n", orient.RoomDir)
	for _, m := range room.Members {
		fmt.Fprintf(out, "  @%-16s %s\n", m.Handle, m.Profile)
	}
	return nil
}

func roomsSend(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("rooms send", flag.ContinueOnError)
	fs.SetOutput(out)
	var connection, thread, worktree string
	var asJSON bool
	fs.StringVar(&connection, "connection", "", "saved Hermes connection name or ID")
	fs.StringVar(&connection, "c", "", "short for --connection")
	fs.StringVar(&thread, "thread", rooms.DefaultThreadID, "conversation thread id")
	fs.StringVar(&worktree, "worktree", "", "Orca worktree selector for the project context (default: the room's predefined room, else ORCA_WORKTREE_ID)")
	fs.BoolVar(&asJSON, "json", false, "print JSON")
	fs.Usage = func() { printRoomsUsage(fs.Output()) }
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errUsage
		}
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		fs.Usage()
		return errors.New("rooms send: needs <room-id> and a message")
	}
	roomID, message := rest[0], strings.Join(rest[1:], " ")

	client, _, err := roomsDial(ctx, connection)
	if err != nil {
		return err
	}
	defer client.Close()
	// The room's first message carries the project context AND the room's
	// shared directory with its handoff contract, so the members know where
	// they are, where to write, and what counts as proof of a write.
	orient, err := resolveOrientation(roomID, worktree).WithRoomDir(roomID)
	if err != nil {
		return err
	}
	event, err := client.SendOpening(ctx, roomID, message, thread, orient)
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(out, event)
	}
	fmt.Fprintf(out, "sent seq=%d to room %s — members are answering; watch with:\n  lucinate rooms log %s --follow\n",
		event.Seq, roomID, roomID)
	return nil
}

func roomsLog(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("rooms log", flag.ContinueOnError)
	fs.SetOutput(out)
	var connection string
	var since, limit int
	var follow, asJSON bool
	var timeout time.Duration
	fs.StringVar(&connection, "connection", "", "saved Hermes connection name or ID")
	fs.StringVar(&connection, "c", "", "short for --connection")
	fs.IntVar(&since, "since", 0, "start after this sequence number")
	fs.IntVar(&limit, "limit", 0, "page size (gateway default when 0)")
	fs.BoolVar(&follow, "follow", false, "keep polling for new events")
	fs.DurationVar(&timeout, "timeout", 0, "with --follow, stop after this long (0 = until interrupted)")
	fs.BoolVar(&asJSON, "json", false, "print raw JSON events")
	fs.Usage = func() { printRoomsUsage(fs.Output()) }
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errUsage
		}
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return errors.New("rooms log: missing room id")
	}
	roomID := rest[0]

	client, _, err := roomsDial(ctx, connection)
	if err != nil {
		return err
	}
	defer client.Close()

	deadline := time.Time{}
	if follow && timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	render := func(page rooms.LogPage) error {
		for _, ev := range page.Events {
			if asJSON {
				if err := writeJSON(out, ev); err != nil {
					return err
				}
				continue
			}
			fmt.Fprintln(out, formatRoomEvent(ev))
		}
		return nil
	}
	for {
		page, err := client.Log(ctx, roomID, since, limit)
		if err != nil {
			return err
		}
		if err := render(page); err != nil {
			return err
		}
		since = page.Cursor
		if !follow {
			return nil
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-client.Done():
			return errors.New("gateway connection closed")
		case <-time.After(time.Second):
		}
	}
}

func roomsState(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("rooms state", flag.ContinueOnError)
	fs.SetOutput(out)
	var connection string
	var asJSON bool
	fs.StringVar(&connection, "connection", "", "saved Hermes connection name or ID")
	fs.StringVar(&connection, "c", "", "short for --connection")
	fs.BoolVar(&asJSON, "json", false, "print JSON")
	fs.Usage = func() { printRoomsUsage(fs.Output()) }
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errUsage
		}
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return errors.New("rooms state: missing room id")
	}
	client, _, err := roomsDial(ctx, connection)
	if err != nil {
		return err
	}
	defer client.Close()
	state, err := client.State(ctx, rest[0])
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(out, state)
	}
	fmt.Fprintf(out, "room %s (%s) rev=%d members=%d\n", state.Room.RoomID, state.Room.Name,
		state.Room.Revision, len(state.Room.Members))
	if ds := state.DriverStatus; ds != nil {
		fmt.Fprintf(out, "driver running=%t working=%t blocked=%t counts=%v\n", ds.Running, ds.Working, ds.Blocked, ds.Counts)
		for _, a := range ds.PendingActions {
			fmt.Fprintf(out, "  pending: %v\n", a)
		}
	}
	return nil
}

// roomsStop cancels queued and running member turns for a room. It is
// the recovery path for a room whose driver is stuck on one member: the
// driver runs turns one message at a time, so a member that never
// settles blocks every message posted after it.
func roomsStop(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("rooms stop", flag.ContinueOnError)
	fs.SetOutput(out)
	var connection string
	var asJSON bool
	fs.StringVar(&connection, "connection", "", "saved Hermes connection name or ID")
	fs.StringVar(&connection, "c", "", "short for --connection")
	fs.BoolVar(&asJSON, "json", false, "print JSON")
	fs.Usage = func() { printRoomsUsage(fs.Output()) }
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errUsage
		}
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return errors.New("rooms stop: missing room id")
	}
	client, _, err := roomsDial(ctx, connection)
	if err != nil {
		return err
	}
	defer client.Close()
	cancelled, err := client.Stop(ctx, rest[0])
	if err != nil {
		return err
	}
	if asJSON {
		return writeJSON(out, map[string]any{"room_id": rest[0], "cancelled": cancelled})
	}
	fmt.Fprintf(out, "cancelled %d turn(s) in %s\n", cancelled, rest[0])
	return nil
}

func roomsDisband(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("rooms disband", flag.ContinueOnError)
	fs.SetOutput(out)
	var connection string
	var yes, asJSON bool
	fs.StringVar(&connection, "connection", "", "saved Hermes connection name or ID")
	fs.StringVar(&connection, "c", "", "short for --connection")
	fs.BoolVar(&yes, "yes", false, "required: confirms the room is tombstoned permanently")
	fs.BoolVar(&asJSON, "json", false, "print JSON")
	fs.Usage = func() { printRoomsUsage(fs.Output()) }
	if err := fs.Parse(reorderFlagsFirst(fs, args)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return errUsage
		}
		return err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fs.Usage()
		return errors.New("rooms disband: missing room id")
	}
	if !yes {
		return fmt.Errorf("rooms disband %s: this permanently tombstones the room and its history; re-run with --yes", rest[0])
	}
	client, _, err := roomsDial(ctx, connection)
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Disband(ctx, rest[0]); err != nil {
		return err
	}
	if asJSON {
		return writeJSON(out, map[string]any{"room_id": rest[0], "disbanded": true})
	}
	fmt.Fprintf(out, "disbanded %s\n", rest[0])
	return nil
}

// ── helpers ──────────────────────────────────────────────────────────────────

// warnIfNotLocal flags a roster entry that local discovery did not find.
// It only warns: the gateway is the authority on which profiles are
// local to it, and local discovery is a heuristic (symlinked profile
// homes, alternate install layouts), so a hard failure here could block
// a roster the gateway would accept.
func warnIfNotLocal(members []rooms.Member) {
	known, err := rooms.DiscoverLocalProfiles()
	if err != nil {
		return
	}
	set := make(map[string]struct{}, len(known))
	for _, k := range known {
		set[k] = struct{}{}
	}
	for _, m := range members {
		if _, ok := set[m.Profile]; !ok {
			fmt.Fprintf(os.Stderr, "warning: profile %q was not found under %s — the gateway will refuse it if it is not local\n",
				m.Profile, rooms.ResolveInstallHome())
		}
	}
}

// resolveOrientation picks the project context a room's first message
// carries: an explicit flag wins, then the predefined room the room was
// seated from, then the Orca terminal this command runs in.
func resolveOrientation(roomID, flagWorktree string) rooms.Orientation {
	if strings.TrimSpace(flagWorktree) != "" {
		return rooms.OrientationFromWorktree(flagWorktree)
	}
	if path, err := rooms.DefaultPresetsPath(); err == nil {
		if store, err := rooms.LoadPresets(path); err == nil {
			if p := store.Find(roomID); p != nil && strings.TrimSpace(p.Worktree) != "" {
				return rooms.OrientationFromWorktree(p.Worktree)
			}
		}
	}
	return rooms.OrientationFromEnv()
}

func savePreset(p rooms.Preset) error {
	path, err := rooms.DefaultPresetsPath()
	if err != nil {
		return err
	}
	store, err := rooms.LoadPresets(path)
	if err != nil {
		return err
	}
	store.Remove(p.Name)
	if err := store.Add(p); err != nil {
		return err
	}
	return rooms.SavePresets(path, store)
}

// formatRoomEvent renders one transcript row for the terminal.
func formatRoomEvent(ev rooms.Event) string {
	stamp := time.Unix(int64(ev.CreatedAt), 0).Format("15:04:05")
	speaker := ev.Speaker()
	switch ev.Kind {
	case "message.user":
		return fmt.Sprintf("%s  you → %s", stamp, ev.Text())
	case "message.member":
		return fmt.Sprintf("%s  @%s: %s", stamp, speaker, ev.Text())
	case "turn.settled":
		passed, _ := ev.Payload["passed"].(bool)
		if passed {
			return fmt.Sprintf("%s  · %s passed", stamp, speaker)
		}
		return fmt.Sprintf("%s  · %s finished", stamp, speaker)
	case "turn.started":
		return fmt.Sprintf("%s  · %s is thinking…", stamp, speaker)
	case "turn.failed":
		return fmt.Sprintf("%s  ! %s failed: %v", stamp, speaker, ev.Payload["error"])
	case "turn.deferred":
		return fmt.Sprintf("%s  · %s deferred: %v", stamp, speaker, ev.Payload["reason"])
	case "room.activity":
		return fmt.Sprintf("%s  · activity: %v", stamp, ev.Payload["status"])
	case "member.unavailable":
		return fmt.Sprintf("%s  ! %s unavailable", stamp, speaker)
	}
	return fmt.Sprintf("%s  %s %s %s", stamp, ev.Kind, speaker, compactPayload(ev.Payload))
}

func compactPayload(p map[string]any) string {
	if len(p) == 0 {
		return ""
	}
	text, _ := p["text"].(string)
	if text != "" {
		return text
	}
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	if len(b) > 200 {
		b = b[:200]
	}
	return string(b)
}

func printRoomsUsage(out io.Writer) {
	fmt.Fprintln(out, `Usage: lucinate rooms <command> [flags]

Rooms are Hermes hosted "Bot Mode" group chats. A room has a durable
transcript and a roster of 2-6 Hermes profiles on this machine; each is a
bot that answers when you post. @handle addresses one bot, @all (or no
mention) addresses everyone.

The roster is frozen when the room is created — to change members, create
a new room (a disbanded room id is tombstoned and cannot be reused).

Commands:
  list [--all]                 list rooms on the gateway
  caps                         gateway room-protocol capabilities
  profiles                     local Hermes profiles (the invite list)
  presets                      predefined rooms saved on this machine
  create --name N --profiles a,b[,c]
         [--room-id ID] [--thread T] [--worktree SEL] [--save]
                               create a room and invite profiles
  from <preset> [--save]       create a room from a predefined room
  send <room-id> <message...> [--worktree SEL]
                               post a message; the roster answers
  log <room-id> [--since N] [--follow] [--timeout D]
                               print the transcript
  state <room-id>              room + live driver status
  stop <room-id>               cancel queued and running member turns
  disband <room-id> --yes      tombstone a room permanently

Every command takes --connection|-c <name> to pick a saved Hermes
connection, and --json for machine-readable output.`)
}
