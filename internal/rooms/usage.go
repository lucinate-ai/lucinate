package rooms

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Token and cost accounting per member.
//
// A room multiplies whatever a single chat turn costs by its roster size, and
// the user has no way to see that from the transcript. The gateway reports
// usage per member turn; this file folds those numbers per member so /cost can
// answer "who is spending what".
//
// The reporting shapes vary: the hosted-room gateway has no frozen usage
// schema, and the numbers arrive nested under `usage` or `tokens`, named
// `input_tokens`/`prompt_tokens`/`in`, and only sometimes carry a price. The
// extractor is deliberately tolerant — a number it cannot read is an item it
// must not guess.

// Usage is the token and cost total for one turn set.
type Usage struct {
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
	TotalTokens  int     `json:"total_tokens"`
	CostUSD      float64 `json:"cost_usd"`
	// HasCost separates "cost nothing" from "no price was reported".
	HasCost bool `json:"has_cost"`
}

// MemberUsage is one member's row in /cost.
type MemberUsage struct {
	Handle   string `json:"handle"`
	Profile  string `json:"profile"`
	Turns    int    `json:"turns"`
	Messages int    `json:"messages"`
	Usage
}

// usageNumberKeys are the spellings a token count has been seen under.
var (
	inputKeys  = []string{"input_tokens", "prompt_tokens", "input", "in", "inputTokens", "promptTokens"}
	outputKeys = []string{"output_tokens", "completion_tokens", "output", "out", "outputTokens", "completionTokens"}
	totalKeys  = []string{"total_tokens", "total", "totalTokens", "tokens"}
	costKeys   = []string{"cost_usd", "cost", "costUSD", "total_cost_usd"}
)

// UsageFromPayload reads the usage a room event carries. ok is false when the
// payload reports nothing at all, which is the signal /cost needs to say
// "nothing reported yet" instead of printing a table of zeroes.
func UsageFromPayload(payload map[string]any) (Usage, bool) {
	if len(payload) == 0 {
		return Usage{}, false
	}
	var u Usage
	found := false

	// Candidates, innermost first: a usage block usually nests the numbers,
	// and the top level is the fallback for gateways that flatten them.
	candidates := make([]map[string]any, 0, 3)
	for _, key := range []string{"usage", "tokens", "token_usage"} {
		if nested, ok := asMap(payload[key]); ok {
			candidates = append(candidates, nested)
		}
	}
	candidates = append(candidates, payload)

	for _, c := range candidates {
		if n, ok := numberForKey(c, inputKeys...); ok {
			u.InputTokens, found = n, true
		}
		if n, ok := numberForKey(c, outputKeys...); ok {
			u.OutputTokens, found = n, true
		}
		if n, ok := numberForKey(c, totalKeys...); ok {
			u.TotalTokens, found = n, true
		}
		if f, ok := floatForKey(c, costKeys...); ok {
			u.CostUSD, u.HasCost, found = f, true, true
		}
	}
	if !found {
		return Usage{}, false
	}
	if u.TotalTokens == 0 && (u.InputTokens > 0 || u.OutputTokens > 0) {
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}
	return u, true
}

// UsageFor folds a transcript into one row per member.
//
// Rows follow the roster first (so the order is stable between calls), then any
// actor the roster does not know about, sorted by handle: a member removed from
// the room still appears if it spent tokens.
func UsageFor(events []Event, members []Member) []MemberUsage {
	index := map[string]*MemberUsage{}
	var order []string

	rowFor := func(key string, ev Event) *MemberUsage {
		if r, ok := index[key]; ok {
			return r
		}
		index[key] = &MemberUsage{Handle: key, Profile: ev.Actor.Profile}
		order = append(order, key)
		return index[key]
	}

	// open tracks a member whose turn has started but not finished, so a
	// settled event that never had a start is still counted exactly once.
	open := map[string]bool{}

	for _, ev := range events {
		switch ev.Kind {
		case "message.user", "room.activity", "member.unavailable":
			continue
		}
		key := memberKeyForEvent(ev)
		if key == "" {
			continue
		}
		row := rowFor(key, ev)
		if row.Profile == "" {
			row.Profile = ev.Actor.Profile
		}

		switch ev.Kind {
		case "message.member":
			row.Messages++
		case "turn.started":
			row.Turns++
			open[key] = true
		case "turn.settled", "turn.failed", "turn.deferred":
			if !open[key] {
				// Some gateways only emit the terminal event; counting it is
				// better than reporting a member with zero turns.
				row.Turns++
			}
			open[key] = false
		}
		if u, ok := UsageFromPayload(ev.Payload); ok {
			row.InputTokens += u.InputTokens
			row.OutputTokens += u.OutputTokens
			row.TotalTokens += u.TotalTokens
			row.CostUSD += u.CostUSD
			row.HasCost = row.HasCost || u.HasCost
		}
	}

	rows := make([]MemberUsage, 0, len(members)+len(order))
	seen := map[string]bool{}
	for _, m := range members {
		key := memberHandle(m)
		seen[key] = true
		if r, ok := index[key]; ok {
			if r.Profile == "" {
				r.Profile = m.Profile
			}
			rows = append(rows, *r)
			continue
		}
		rows = append(rows, MemberUsage{Handle: key, Profile: m.Profile})
	}
	var extra []string
	for _, key := range order {
		if !seen[key] {
			extra = append(extra, key)
		}
	}
	sort.Strings(extra)
	for _, key := range extra {
		rows = append(rows, *index[key])
	}
	return rows
}

// UsageTotals sums the rows.
func UsageTotals(rows []MemberUsage) MemberUsage {
	var total MemberUsage
	for _, r := range rows {
		total.Turns += r.Turns
		total.Messages += r.Messages
		total.InputTokens += r.InputTokens
		total.OutputTokens += r.OutputTokens
		total.TotalTokens += r.TotalTokens
		total.CostUSD += r.CostUSD
		total.HasCost = total.HasCost || r.HasCost
	}
	return total
}

// FormatUsage renders the /cost table. It is plain text on purpose: the TUI
// colours it, the CLI prints it as is, and neither has to parse a layout.
func FormatUsage(rows []MemberUsage) string {
	if len(rows) == 0 {
		return "no usage to report"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%-22s %-16s %6s %6s %9s %9s %9s %10s\n",
		"MEMBER", "PROFILE", "TURNS", "MSGS", "INPUT", "OUTPUT", "TOTAL", "COST")
	for _, r := range rows {
		writeUsageRow(&b, r)
	}
	if len(rows) > 1 {
		writeUsageRow(&b, UsageTotals(rows))
	}
	return strings.TrimRight(b.String(), "\n")
}

func writeUsageRow(b *strings.Builder, r MemberUsage) {
	handle := r.Handle
	if handle == "" {
		handle = "TOTAL"
	} else if !strings.HasPrefix(handle, "@") {
		handle = "@" + handle
	}
	cost := "—"
	if r.HasCost {
		cost = fmt.Sprintf("$%.4f", r.CostUSD)
	}
	fmt.Fprintf(b, "%-22s %-16s %6d %6d %9d %9d %9d %10s\n",
		handle, r.Profile, r.Turns, r.Messages, r.InputTokens, r.OutputTokens, r.TotalTokens, cost)
}

// memberKeyForEvent is the handle an event's usage is attributed to.
func memberKeyForEvent(ev Event) string {
	if ev.Actor.Profile != "" {
		return HandleFor(ev.Actor.Profile)
	}
	if ev.Actor.DisplayName != "" {
		return HandleFor(ev.Actor.DisplayName)
	}
	if ev.Actor.ID != "" && ev.Actor.Kind == "member" {
		return HandleFor(ev.Actor.ID)
	}
	return ""
}

func asMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func numberForKey(m map[string]any, keys ...string) (int, bool) {
	for _, k := range keys {
		if n, ok := asInt(m[k]); ok {
			return n, true
		}
	}
	return 0, false
}

func floatForKey(m map[string]any, keys ...string) (float64, bool) {
	for _, k := range keys {
		if f, ok := asFloat(m[k]); ok {
			return f, true
		}
	}
	return 0, false
}

// asInt accepts the numeric shapes JSON decoding and hand-built payloads
// produce. Negative counts are rejected: a negative token total is a broken
// report, not a credit.
func asInt(v any) (int, bool) {
	f, ok := asFloat(v)
	if !ok {
		return 0, false
	}
	if f < 0 {
		return 0, false
	}
	return int(f), true
}

func asFloat(v any) (float64, bool) {
	var f float64
	switch n := v.(type) {
	case float64:
		f = n
	case float32:
		f = float64(n)
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	case json.Number:
		parsed, err := strconv.ParseFloat(string(n), 64)
		if err != nil {
			return 0, false
		}
		f = parsed
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		if err != nil {
			return 0, false
		}
		f = parsed
	default:
		return 0, false
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}
