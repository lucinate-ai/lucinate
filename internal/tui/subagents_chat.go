package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/lucinate-ai/lucinate/internal/backend"
)

// formatSubagentList renders a tracker/RPC snapshot as a single
// multi-line system message for the inline `/subagents list` verb. err
// trumps items — a list failure becomes the visible body and items are
// ignored. Empty success is its own short message so the user knows
// the verb ran but found nothing.
func formatSubagentList(items []backend.SubagentInfo, err error) string {
	if err != nil {
		return fmt.Sprintf("subagents: error — %v", err)
	}
	if len(items) == 0 {
		return "No subagents in this session."
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Subagents (%d):\n", len(items)))
	for i, info := range items {
		label := strings.TrimSpace(info.Label)
		if label == "" {
			label = info.SessionKey
		}
		status := strings.TrimSpace(info.Status)
		if status == "" {
			status = "unknown"
		}
		fmt.Fprintf(&b, "  %d. [%s] %s\n", i+1, status, label)
		fmt.Fprintf(&b, "     key=%s", info.SessionKey)
		if info.AgentID != "" {
			fmt.Fprintf(&b, "  agent=%s", info.AgentID)
		}
		if info.Model != "" {
			fmt.Fprintf(&b, "  model=%s", info.Model)
		}
		b.WriteString("\n")
	}
	b.WriteString("Use /subagents info <#> for details or /subagents kill <#|all> to abort.")
	return b.String()
}

// formatSubagentInfo renders a single SubagentInfo for the inline
// `/subagents info` verb. err trumps info; a nil info with no error
// reads as "not found".
func formatSubagentInfo(info *backend.SubagentInfo, err error) string {
	if err != nil {
		return fmt.Sprintf("subagent info: error — %v", err)
	}
	if info == nil {
		return "subagent info: not found"
	}
	var b strings.Builder
	label := strings.TrimSpace(info.Label)
	if label == "" {
		label = info.SessionKey
	}
	fmt.Fprintf(&b, "Subagent: %s\n", label)
	fmt.Fprintf(&b, "  status: %s\n", firstNonEmpty(info.Status, "unknown"))
	fmt.Fprintf(&b, "  key:    %s\n", info.SessionKey)
	if info.ParentKey != "" {
		fmt.Fprintf(&b, "  parent: %s\n", info.ParentKey)
	}
	if info.AgentID != "" {
		fmt.Fprintf(&b, "  agent:  %s\n", info.AgentID)
	}
	if info.Model != "" {
		fmt.Fprintf(&b, "  model:  %s\n", info.Model)
	}
	if info.SpawnDepth > 0 {
		fmt.Fprintf(&b, "  depth:  %d\n", info.SpawnDepth)
	}
	if preview := strings.TrimSpace(info.LastMessage); preview != "" {
		if len(preview) > 200 {
			preview = preview[:197] + "..."
		}
		fmt.Fprintf(&b, "  last:   %s\n", preview)
	}
	return strings.TrimRight(b.String(), "\n")
}

// subagentTracker records subagent state observed from the live event
// stream so the browser view and the inline `/subagents list` verb can
// answer without a round-trip and so transient activity (a spawn we
// just saw on the wire, a kill we just dispatched) shows up
// immediately. The RPC list is still authoritative — the tracker is a
// cache that the browser refreshes against the gateway on entry and
// merges live updates into.
//
// The tracker is safe for concurrent use; the chat update loop and the
// event pump can both touch it. The mutex stays internal so callers
// pass the tracker into goroutines without worrying about ordering.
type subagentTracker struct {
	mu       sync.Mutex
	children map[string]backend.SubagentInfo
}

// newSubagentTracker returns an empty tracker.
func newSubagentTracker() *subagentTracker {
	return &subagentTracker{children: map[string]backend.SubagentInfo{}}
}

// upsert merges info into the tracker. Empty status / label / model
// fields don't overwrite known values, so a partial update (e.g. just
// a status change from an event) preserves whatever the previous full
// fetch saw. SessionKey is required; entries without it are ignored.
func (t *subagentTracker) upsert(info backend.SubagentInfo) {
	if info.SessionKey == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	existing := t.children[info.SessionKey]
	if info.Status != "" {
		existing.Status = info.Status
	}
	if info.Label != "" {
		existing.Label = info.Label
	}
	if info.AgentID != "" {
		existing.AgentID = info.AgentID
	}
	if info.Model != "" {
		existing.Model = info.Model
	}
	if info.ParentKey != "" {
		existing.ParentKey = info.ParentKey
	}
	if info.SpawnDepth != 0 {
		existing.SpawnDepth = info.SpawnDepth
	}
	if info.CreatedAtMs != 0 {
		existing.CreatedAtMs = info.CreatedAtMs
	}
	if info.UpdatedAtMs != 0 {
		existing.UpdatedAtMs = info.UpdatedAtMs
	}
	if info.LastMessage != "" {
		existing.LastMessage = info.LastMessage
	}
	existing.SessionKey = info.SessionKey
	t.children[info.SessionKey] = existing
}

// replace overwrites the tracker with the canonical list from an
// authoritative source (the SubagentsList RPC). Children present in
// the old map but absent from `items` are dropped — sessions that
// disappeared from the gateway shouldn't keep appearing in the
// browser.
func (t *subagentTracker) replace(items []backend.SubagentInfo) {
	t.mu.Lock()
	defer t.mu.Unlock()
	next := make(map[string]backend.SubagentInfo, len(items))
	for _, item := range items {
		if item.SessionKey == "" {
			continue
		}
		next[item.SessionKey] = item
	}
	t.children = next
}

// remove drops a child from the tracker (called after a successful
// kill, or when the user dismisses the row).
func (t *subagentTracker) remove(sessionKey string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.children, sessionKey)
}

// snapshot returns a copy of the tracked children sorted by spawn
// time (newest first). Callers receive a slice they can mutate without
// affecting the tracker.
func (t *subagentTracker) snapshot() []backend.SubagentInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]backend.SubagentInfo, 0, len(t.children))
	for _, info := range t.children {
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAtMs != out[j].CreatedAtMs {
			return out[i].CreatedAtMs > out[j].CreatedAtMs
		}
		return out[i].SessionKey < out[j].SessionKey
	})
	return out
}

// len reports how many children the tracker is holding.
func (t *subagentTracker) len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.children)
}

// subagentToolNames is the set of tool names that indicate subagent /
// delegation activity. The TUI promotes these to a distinct subagent
// flavor on the inline tool card and feeds their lifecycle into the
// tracker so the browser/list verb reflect live state without an extra
// fetch. Add new entries here when surfacing additional backends —
// the matcher is exact (case-insensitive) but tolerant of common
// hyphen/underscore variants.
var subagentToolNames = map[string]bool{
	"sessions_spawn": true,
	"sessions.spawn": true,
	"sessions_yield": true,
	"sessions.yield": true,
	"subagents":      true,
	"subagent":       true,
	"delegate_task":  true,
	"delegate":       true,
	"delegation":     true,
}

// isSubagentToolName reports whether name corresponds to a subagent
// or delegation tool call.
func isSubagentToolName(name string) bool {
	if name == "" {
		return false
	}
	lower := strings.ToLower(strings.TrimSpace(name))
	if subagentToolNames[lower] {
		return true
	}
	// Tolerate common variants ("sessions-spawn" etc.).
	canon := strings.ReplaceAll(strings.ReplaceAll(lower, "-", "_"), ".", "_")
	return subagentToolNames[canon]
}

// subagentToolArgs is the loose shape we extract from a tool call's
// argument JSON for tracking purposes. All fields are best-effort; the
// tracker tolerates missing values.
type subagentToolArgs struct {
	AgentID    string `json:"agentId"`
	Label      string `json:"label"`
	Task       string `json:"task"`
	TaskName   string `json:"taskName"`
	Model      string `json:"model"`
	SessionKey string `json:"sessionKey"`
	Goal       string `json:"goal"`
}

// observeSubagentToolStart updates the tracker when a subagent /
// delegation tool call starts. The caller has already confirmed the
// tool name is a subagent-related one; the args come straight from
// the gateway-typed event so missing fields just leave zero values in
// the tracker entry.
func (t *subagentTracker) observeToolStart(parentSessionKey, toolCallID string, args json.RawMessage) {
	if toolCallID == "" {
		return
	}
	a := decodeSubagentToolArgs(args)
	label := strings.TrimSpace(a.Label)
	if label == "" {
		label = strings.TrimSpace(a.TaskName)
	}
	if label == "" {
		label = strings.TrimSpace(a.Task)
	}
	if label == "" {
		label = strings.TrimSpace(a.Goal)
	}
	key := strings.TrimSpace(a.SessionKey)
	if key == "" {
		// The gateway hasn't issued the child session key yet — track
		// the in-flight call by tool-call-id so a later result phase
		// can reconcile against it without losing the placeholder.
		key = "tool:" + toolCallID
	}
	t.upsert(backend.SubagentInfo{
		SessionKey: key,
		ParentKey:  parentSessionKey,
		Label:      label,
		AgentID:    strings.TrimSpace(a.AgentID),
		Model:      strings.TrimSpace(a.Model),
		Status:     "running",
		LastMessage: firstNonEmpty(
			strings.TrimSpace(a.Task),
			strings.TrimSpace(a.Goal),
		),
	})
}

// observeToolResult flips a tracked subagent's status to completed /
// failed based on the result phase's IsError flag.
func (t *subagentTracker) observeToolResult(toolCallID string, isError bool, errText string) {
	if toolCallID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	placeholder := "tool:" + toolCallID
	for key, info := range t.children {
		if key != placeholder && info.SessionKey != placeholder {
			// We don't have a back-reference from session key to
			// tool-call id; only the placeholder rows get flipped here.
			// Real child rows are reconciled by SubagentsList.
			continue
		}
		if isError {
			info.Status = "failed"
			if errText != "" {
				info.LastMessage = errText
			}
		} else {
			info.Status = "completed"
		}
		t.children[key] = info
	}
}

// decodeSubagentToolArgs parses the raw tool args into the loose
// subagentToolArgs shape. A parse failure returns the zero value so
// callers can keep going.
func decodeSubagentToolArgs(raw json.RawMessage) subagentToolArgs {
	var out subagentToolArgs
	if len(raw) == 0 {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

// firstNonEmpty returns the first non-empty string in s, or "" when
// every input is empty.
func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
