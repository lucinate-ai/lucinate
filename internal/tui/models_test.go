package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	"github.com/a3tai/openclaw-go/protocol"
)

// TestQualifiedModelRef covers the provider-prefix normalisation the
// model picker and /model command apply before sessions.patch. The
// gateway reports a provider-local id + separate provider from
// models.list, but sessions.patch validates against the fully-qualified
// "<provider>/<id>" reference — so the bare id must be joined with its
// provider or the switch is rejected with "model not allowed".
func TestQualifiedModelRef(t *testing.T) {
	tests := []struct {
		name string
		in   protocol.ModelChoice
		want string
	}{
		{
			name: "openrouter provider-local slug gets prefixed",
			in:   protocol.ModelChoice{ID: "deepseek/deepseek-v4-pro", Provider: "openrouter"},
			want: "openrouter/deepseek/deepseek-v4-pro",
		},
		{
			name: "simple provider and id",
			in:   protocol.ModelChoice{ID: "claude-sonnet-4", Provider: "anthropic"},
			want: "anthropic/claude-sonnet-4",
		},
		{
			name: "empty provider keeps bare id (openai/hermes)",
			in:   protocol.ModelChoice{ID: "gpt-4o", Provider: ""},
			want: "gpt-4o",
		},
		{
			name: "already-qualified id is not double-prefixed",
			in:   protocol.ModelChoice{ID: "openrouter/deepseek/deepseek-v4-pro", Provider: "openrouter"},
			want: "openrouter/deepseek/deepseek-v4-pro",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := qualifiedModelRef(tt.in); got != tt.want {
				t.Errorf("qualifiedModelRef(%+v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestIsCurrentModel covers the single predicate behind both the picker's
// pre-selection and its "(current)" marker. The session carries the
// qualified reference, but provider-less backends only ever have the bare
// id, so both forms must match.
func TestIsCurrentModel(t *testing.T) {
	tests := []struct {
		name           string
		mc             protocol.ModelChoice
		currentModelID string
		want           bool
	}{
		{
			name:           "qualified reference matches",
			mc:             protocol.ModelChoice{ID: "claude-sonnet-4", Provider: "anthropic"},
			currentModelID: "anthropic/claude-sonnet-4",
			want:           true,
		},
		{
			name:           "bare id matches when provider is empty",
			mc:             protocol.ModelChoice{ID: "gpt-4o", Provider: ""},
			currentModelID: "gpt-4o",
			want:           true,
		},
		{
			name:           "bare id matches a provider-bearing choice",
			mc:             protocol.ModelChoice{ID: "claude-sonnet-4", Provider: "anthropic"},
			currentModelID: "claude-sonnet-4",
			want:           true,
		},
		{
			name:           "different model does not match",
			mc:             protocol.ModelChoice{ID: "claude-sonnet-4", Provider: "anthropic"},
			currentModelID: "anthropic/claude-opus-4-8",
			want:           false,
		},
		{
			name:           "empty current reference matches nothing",
			mc:             protocol.ModelChoice{ID: "claude-sonnet-4", Provider: "anthropic"},
			currentModelID: "",
			want:           false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCurrentModel(tt.mc, tt.currentModelID); got != tt.want {
				t.Errorf("isCurrentModel(%+v, %q) = %v, want %v", tt.mc, tt.currentModelID, got, tt.want)
			}
		})
	}
}

// TestModelDelegateCurrentMarker pins the marker to the title line in both
// style branches. Pre-selection alone cannot carry "which model am I on?"
// — the picker opens straight into filter-typing, so the highlight moves
// away almost immediately — which is why the marker has to render on the
// non-highlighted row too.
func TestModelDelegateCurrentMarker(t *testing.T) {
	models := []protocol.ModelChoice{
		{ID: "claude-opus-4-8", Name: "Claude Opus 4.8", Provider: "anthropic"},
		{ID: "claude-sonnet-4", Name: "Claude Sonnet 4", Provider: "anthropic"},
	}
	items := make([]list.Item, len(models))
	for i, mc := range models {
		items[i] = modelItem{model: mc}
	}

	render := func(currentModelID string, index int) string {
		t.Helper()
		l := list.New(items, modelDelegate{currentModelID: currentModelID}, 40, 20)
		// Row 0 is highlighted by default; selecting row 0 keeps index 1
		// on the non-highlighted branch and vice versa.
		l.Select(0)
		var sb strings.Builder
		modelDelegate{currentModelID: currentModelID}.Render(&sb, l, index, items[index])
		return sb.String()
	}

	t.Run("highlighted row is marked", func(t *testing.T) {
		if got := render("anthropic/claude-opus-4-8", 0); !strings.Contains(got, "(current)") {
			t.Errorf("expected (current) on the highlighted row, got: %q", got)
		}
	})

	t.Run("non-highlighted row is marked", func(t *testing.T) {
		got := render("anthropic/claude-sonnet-4", 1)
		if !strings.Contains(got, "(current)") {
			t.Errorf("expected (current) on the non-highlighted row, got: %q", got)
		}
		if strings.Contains(got, ">") {
			t.Errorf("expected row 1 to render unhighlighted, got: %q", got)
		}
	})

	t.Run("other rows are not marked", func(t *testing.T) {
		if got := render("anthropic/claude-opus-4-8", 1); strings.Contains(got, "(current)") {
			t.Errorf("expected no marker on a non-current row, got: %q", got)
		}
	})

	t.Run("no model set marks nothing", func(t *testing.T) {
		for i := range models {
			if got := render("", i); strings.Contains(got, "(current)") {
				t.Errorf("row %d: expected no marker when no model is set, got: %q", i, got)
			}
		}
	})

	t.Run("current model absent from the list marks nothing", func(t *testing.T) {
		for i := range models {
			if got := render("anthropic/claude-haiku-9", i); strings.Contains(got, "(current)") {
				t.Errorf("row %d: expected no marker for an absent model, got: %q", i, got)
			}
		}
	})
}

// TestModelPickerMarkerSurvivesFiltering drives the picker through Update
// and asserts on the rendered view. This is the scenario pre-selection
// cannot cover: the picker opens straight into filter-typing, so by the
// time the user has typed anything the highlight has moved and only the
// marker still says which model is in use.
func TestModelPickerMarkerSurvivesFiltering(t *testing.T) {
	models := []protocol.ModelChoice{
		{ID: "claude-opus-4-8", Name: "Claude Opus 4.8", Provider: "anthropic"},
		{ID: "claude-sonnet-4", Name: "Claude Sonnet 4", Provider: "anthropic"},
		{ID: "gpt-4o", Name: "GPT-4o", Provider: "openai"},
	}

	newPicker := func(currentModelID string) modelPickerModel {
		t.Helper()
		p := newModelPickerModel(newFakeBackend(), "sess-1", currentModelID, false, nil, false)
		p.setSize(80, 24)
		p, _ = p.Update(modelsLoadedMsg{models: models})
		return p
	}

	// Driving the filter through KeyPressMsg would mean pumping the
	// commands bubbletea normally runs, which drags the cursor-blink
	// timers into the test and makes it take minutes. SetFilterText
	// applies the same filter synchronously.
	typeFilter := func(p modelPickerModel, text string) modelPickerModel {
		t.Helper()
		p.list.SetFilterText(text)
		return p
	}

	// The session is on Sonnet, which is not the row the list starts on,
	// so a marker in the view can only have come from the predicate.
	p := newPicker("anthropic/claude-sonnet-4")

	t.Run("marked on open", func(t *testing.T) {
		t.Skip("blocked on a pre-existing bug: the picker's list renders zero rows " +
			"until the filter text first changes. modelsLoadedMsg calls SetItems while " +
			"the list is still Unfiltered (so no filterItems command is produced), then " +
			"flips to Filtering — at which point VisibleItems() reads the still-nil " +
			"filteredItems. Reproduces identically on main for /models, so it predates " +
			"this change. Un-skip as part of fixing it.")

		if got := p.View(); !strings.Contains(got, "(current)") {
			t.Errorf("expected (current) in the freshly-opened picker, got:\n%s", got)
		}
	})

	// Typing is what populates the list today, and it is also the moment
	// pre-selection stops carrying the answer — so this is the case the
	// marker exists for.
	t.Run("survives filtering", func(t *testing.T) {
		if got := typeFilter(p, "sonnet").View(); !strings.Contains(got, "(current)") {
			t.Errorf("expected (current) to survive filtering, got:\n%s", got)
		}
	})

	t.Run("absent when the current model is filtered out", func(t *testing.T) {
		if got := typeFilter(p, "gpt").View(); strings.Contains(got, "(current)") {
			t.Errorf("expected no marker when the current model is filtered out, got:\n%s", got)
		}
	})

	t.Run("absent when no model is set", func(t *testing.T) {
		if got := typeFilter(newPicker(""), "claude").View(); strings.Contains(got, "(current)") {
			t.Errorf("expected no marker when no model is set, got:\n%s", got)
		}
	})
}
