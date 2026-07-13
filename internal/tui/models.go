package tui

import (
	"context"
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/a3tai/openclaw-go/protocol"
	"github.com/sahilm/fuzzy"

	"github.com/lucinate-ai/lucinate/internal/backend"
	"github.com/lucinate-ai/lucinate/internal/config"
)

// modelItem is a list item for the model picker.
type modelItem struct {
	model protocol.ModelChoice
}

// FilterValue is the haystack the bubbles list filter (here: fuzzy
// match) runs against. Concatenating Name, ID, and Provider lets the
// user type any of them — e.g. "sonnet", "claude-sonnet-4", or
// "anthropic" all rank the same model.
func (i modelItem) FilterValue() string {
	parts := []string{i.model.Name, i.model.ID, i.model.Provider}
	return strings.Join(parts, " ")
}

// qualifiedModelRef returns the fully-qualified "<provider>/<id>" model
// reference the gateway expects for sessions.patch. models.list reports
// a provider-local id (e.g. "deepseek/deepseek-v4-pro") alongside a
// separate provider (e.g. "openrouter"), but the session model and the
// agent's configured model.primary are the qualified form
// ("openrouter/deepseek/deepseek-v4-pro"). Sending the bare id makes the
// gateway reject the switch with "model not allowed: <id>".
//
// Backends that don't populate Provider (openai, hermes) keep the bare
// id unchanged. The prefix guard is idempotent, so an already-qualified
// id from a future gateway isn't double-prefixed.
func qualifiedModelRef(mc protocol.ModelChoice) string {
	if mc.Provider == "" || strings.HasPrefix(mc.ID, mc.Provider+"/") {
		return mc.ID
	}
	return mc.Provider + "/" + mc.ID
}

// modelDelegate renders each model row in the picker.
type modelDelegate struct{}

func (d modelDelegate) Height() int                             { return 2 }
func (d modelDelegate) Spacing() int                            { return 1 }
func (d modelDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d modelDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	i, ok := item.(modelItem)
	if !ok {
		return
	}

	title := i.model.Name
	if title == "" {
		title = i.model.ID
	}

	subtitle := i.model.ID
	if i.model.Provider != "" {
		subtitle = i.model.Provider + " · " + i.model.ID
	}

	if index == m.Index() {
		str := lipgloss.NewStyle().
			Foreground(accent).
			Bold(true).
			Render(fmt.Sprintf("> %s", title))
		str += "\n" + lipgloss.NewStyle().
			Foreground(subtle).
			Render(fmt.Sprintf("  %s", subtitle))
		fmt.Fprint(w, str)
	} else {
		str := fmt.Sprintf("  %s", title)
		str += "\n" + lipgloss.NewStyle().
			Foreground(subtle).
			Render(fmt.Sprintf("  %s", subtitle))
		fmt.Fprint(w, str)
	}
}

// fuzzyFilter adapts sahilm/fuzzy to the bubbles list FilterFunc
// signature. Empty terms short-circuit to every item in original order
// so the unfiltered list isn't reshuffled.
func fuzzyFilter(term string, targets []string) []list.Rank {
	if strings.TrimSpace(term) == "" {
		ranks := make([]list.Rank, len(targets))
		for i := range targets {
			ranks[i] = list.Rank{Index: i}
		}
		return ranks
	}
	matches := fuzzy.Find(term, targets)
	ranks := make([]list.Rank, len(matches))
	for i, m := range matches {
		ranks[i] = list.Rank{
			Index:          m.Index,
			MatchedIndexes: m.MatchedIndexes,
		}
	}
	return ranks
}

// modelPickerModel is the model selection view.
type modelPickerModel struct {
	list           list.Model
	backend        backend.Backend
	sessionKey     string
	currentModelID string
	loading        bool
	err            error
	hideHints      bool
	activeConn     *config.Connection
}

func newModelPickerModel(b backend.Backend, sessionKey, currentModelID string, hideHints bool, activeConn *config.Connection, disableExitKeys bool) modelPickerModel {
	l := list.New(nil, modelDelegate{}, 0, 0)
	// Title is rendered explicitly above the list (see View) because
	// bubbles replaces the list's built-in title with the filter
	// prompt while typing, which would hide the screen name during
	// the very interaction this view exists for.
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(!hideHints)
	l.Styles.Title = headerStyle
	// Live filtering is the whole point of this view — unlike the
	// other pickers in the codebase, which keep filtering off.
	l.SetFilteringEnabled(true)
	l.Filter = fuzzyFilter
	if disableExitKeys {
		l.KeyMap.Quit.Unbind()
		l.KeyMap.ForceQuit.Unbind()
	}

	return modelPickerModel{
		list:           l,
		backend:        b,
		sessionKey:     sessionKey,
		currentModelID: currentModelID,
		loading:        true,
		hideHints:      hideHints,
		activeConn:     activeConn,
	}
}

func (m modelPickerModel) loadModels() tea.Cmd {
	b := m.backend
	return func() tea.Msg {
		result, err := b.ModelsList(context.Background())
		if err != nil {
			return modelsLoadedMsg{err: err}
		}
		return modelsLoadedMsg{models: result.Models}
	}
}

func (m modelPickerModel) Init() tea.Cmd {
	return m.loadModels()
}

func (m modelPickerModel) Update(msg tea.Msg) (modelPickerModel, tea.Cmd) {
	switch msg := msg.(type) {
	case modelsLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		items := make([]list.Item, len(msg.models))
		for i, mc := range msg.models {
			items[i] = modelItem{model: mc}
		}
		m.list.SetItems(items)
		// Pre-select the active model so Enter on an empty filter
		// picks "the one you already have" rather than a surprising
		// jump to the top of the list. currentModelID is the qualified
		// "<provider>/<id>" reference (from agent.Model.Primary), so
		// match it against the qualified ref; the bare-id fallback keeps
		// backends without a provider (openai, hermes) working.
		if m.currentModelID != "" {
			for idx, mc := range msg.models {
				if qualifiedModelRef(mc) == m.currentModelID || mc.ID == m.currentModelID {
					m.list.Select(idx)
					break
				}
			}
		}
		// Drop straight into filter-typing mode so the user doesn't
		// have to press `/` first — typing-as-filter is the headline
		// improvement of this view.
		m.list.SetFilterState(list.Filtering)
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m modelPickerModel) handleKey(msg tea.KeyPressMsg) (modelPickerModel, tea.Cmd) {
	// Enter picks the highlighted model from any filter state. The
	// bubbles default in Filtering mode is "apply filter" — that
	// would force a second Enter to actually choose, which is hostile
	// when the whole point of the view is type-then-pick.
	if msg.String() == "enter" && !m.loading && m.err == nil {
		if item, ok := m.list.SelectedItem().(modelItem); ok {
			modelID := qualifiedModelRef(item.model)
			b := m.backend
			sessionKey := m.sessionKey
			switchCmd := func() tea.Msg {
				if err := b.SessionPatchModel(context.Background(), sessionKey, modelID); err != nil {
					return modelSwitchedMsg{err: err}
				}
				return modelSwitchedMsg{modelID: modelID}
			}
			backCmd := func() tea.Msg { return goBackFromModelPickerMsg{} }
			return m, tea.Batch(switchCmd, backCmd)
		}
	}

	// While typing in the filter, all other keys (printable chars,
	// backspace, arrow keys, esc-to-clear) belong to the list
	// widget so the filter input and highlight track together.
	if m.list.FilterState() == list.Filtering {
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}

	// Outside filter mode, route discoverable shortcuts through
	// Actions/TriggerAction so the help line and key dispatch share
	// a single source of truth.
	for _, a := range m.Actions() {
		if a.Key == msg.String() {
			return m.TriggerAction(a.ID)
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// Actions returns the discoverable, view-level commands the model
// picker exposes. Like sessions.go, intrinsic list controls (filter
// typing, cursor navigation, enter-to-select) stay implicit; only
// view-level commands appear here.
func (m modelPickerModel) Actions() []Action {
	var actions []Action
	actions = append(actions, Action{ID: "back", Label: "Back", Key: "esc"})
	if m.err != nil {
		actions = append(actions, Action{ID: "retry", Label: "Retry", Key: "r"})
	}
	return actions
}

func (m modelPickerModel) TriggerAction(id string) (modelPickerModel, tea.Cmd) {
	switch id {
	case "back":
		return m, func() tea.Msg { return goBackFromModelPickerMsg{} }
	case "retry":
		if m.err == nil {
			return m, nil
		}
		m.loading = true
		m.err = nil
		return m, m.loadModels()
	}
	return m, nil
}

func (m modelPickerModel) View() string {
	if m.loading {
		return "\n  Loading models...\n"
	}
	hints := ""
	if !m.hideHints {
		hints = helpStyle.Render(renderActionHints(m.Actions()))
	}
	banner := renderConnectionBanner(m.activeConn)
	if m.err != nil {
		var b strings.Builder
		b.WriteString(banner)
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("  Error: %v", m.err)))
		b.WriteString("\n\n")
		b.WriteString(hints)
		b.WriteString("\n")
		return b.String()
	}
	header := headerStyle.Render(" Select a model ") + "\n"
	if len(m.list.Items()) == 0 {
		var b strings.Builder
		b.WriteString(banner)
		b.WriteString("\n")
		b.WriteString(header)
		b.WriteString("\n")
		b.WriteString("  No models available.\n\n")
		b.WriteString(hints)
		b.WriteString("\n")
		return b.String()
	}
	return banner + "\n" + header + m.list.View() + "\n" + hints
}

func (m *modelPickerModel) setSize(w, h int) {
	// Reserve rows for the explicit header + spacer (see View) on top
	// of the hint line the other pickers already account for.
	m.list.SetSize(w, h-4)
}
