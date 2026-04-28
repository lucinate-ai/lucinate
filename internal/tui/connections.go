package tui

import (
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/lucinate-ai/lucinate/internal/config"
)

// connectionsSubState distinguishes the list view from the add/edit
// form and the inline delete-confirm state.
type connectionsSubState int

const (
	subStateConnList connectionsSubState = iota
	subStateConnForm
	subStateConnDeleteConfirm
)

// connectionItem is a list item for the connections picker.
type connectionItem struct {
	conn      config.Connection
	isDefault bool
}

func (i connectionItem) FilterValue() string {
	return i.conn.Name
}

// connectionDelegate renders each connection in the list.
type connectionDelegate struct{}

func (d connectionDelegate) Height() int                             { return 2 }
func (d connectionDelegate) Spacing() int                            { return 0 }
func (d connectionDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }

func (d connectionDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	i, ok := item.(connectionItem)
	if !ok {
		return
	}

	name := i.conn.Name
	if i.isDefault {
		name = name + " (default)"
	}
	url := i.conn.URL

	var line1, line2 string
	if index == m.Index() {
		line1 = lipgloss.NewStyle().Foreground(accent).Bold(true).Render("> " + name)
		line2 = lipgloss.NewStyle().Foreground(subtle).Render("  " + url)
	} else {
		line1 = "  " + name
		line2 = lipgloss.NewStyle().Foreground(subtle).Render("  " + url)
	}
	fmt.Fprint(w, line1+"\n"+line2)
}

// connectionsModel is the connections picker view.
type connectionsModel struct {
	store     *config.Connections
	list      list.Model
	subState  connectionsSubState
	hideHints bool

	// Form state shared by add and edit. The form has a fixed
	// type-radio at the top (always rendered, focusable on add and
	// read-only on edit since type is immutable) plus 2-3 text fields
	// that vary by selected type:
	//   OpenClaw → Name, Gateway URL
	//   OpenAI   → Name, Base URL, Default model
	editingID    string
	formType     config.ConnectionType
	nameInput    textinput.Model
	urlInput     textinput.Model
	modelInput   textinput.Model
	focusedField int // index into formFields()
	formErr      string

	// Delete confirm state.
	deletingID string

	lastErr error

	width  int
	height int
}

// formField identifies a focusable input on the connections form.
// formFields() returns the ordered list relevant to the currently
// selected type so Tab cycles only through visible inputs.
type formField int

const (
	formFieldType formField = iota // type radio (only on add)
	formFieldName
	formFieldURL
	formFieldModel // OpenAI only
)

func newConnectionsModel(store *config.Connections, hideHints bool) connectionsModel {
	l := list.New(nil, connectionDelegate{}, 0, 0)
	l.Title = "Connections"
	l.SetShowStatusBar(false)
	l.SetShowHelp(!hideHints)
	l.Styles.Title = headerStyle
	l.SetFilteringEnabled(false)
	return connectionsModel{
		store:     store,
		list:      l,
		subState:  subStateConnList,
		hideHints: hideHints,
	}
}

func (m connectionsModel) Init() tea.Cmd {
	return nil
}

// rebuildItems repopulates the list from the in-memory store. Called
// after every CRUD operation.
func (m *connectionsModel) rebuildItems() {
	if m.store == nil {
		m.list.SetItems(nil)
		return
	}
	items := make([]list.Item, len(m.store.Connections))
	for i, conn := range m.store.Connections {
		items[i] = connectionItem{conn: conn, isDefault: conn.ID == m.store.DefaultID}
	}
	m.list.SetItems(items)
}

func (m connectionsModel) Update(msg tea.Msg) (connectionsModel, tea.Cmd) {
	if _, isFirst := msg.(tea.WindowSizeMsg); isFirst {
		// AppModel forwards WindowSizeMsg through setSize, but a
		// fall-through here keeps the list happy if it ever arrives
		// directly.
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
		return m.handleKey(key)
	}
	if m.subState == subStateConnForm {
		return m.updateForm(msg)
	}
	if m.subState == subStateConnList {
		// Lazy first-render of items: caller sets store via NewApp
		// before any key arrives, but subsequent state changes after
		// edits/deletes also reach this model. Rebuild on every
		// message is cheap (small N) and keeps list state in sync.
		m.rebuildItems()
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		return m, cmd
	}
	return m, nil
}

func (m connectionsModel) handleKey(msg tea.KeyPressMsg) (connectionsModel, tea.Cmd) {
	switch m.subState {
	case subStateConnForm:
		return m.handleFormKey(msg)
	case subStateConnDeleteConfirm:
		return m.handleDeleteKey(msg)
	}

	if msg.String() == "enter" {
		if item, ok := m.list.SelectedItem().(connectionItem); ok {
			conn := item.conn
			return m, func() tea.Msg {
				return connectionPickedMsg{connection: &conn}
			}
		}
		return m, nil
	}

	for _, a := range m.Actions() {
		if a.Key == msg.String() {
			return m.TriggerAction(a.ID)
		}
	}

	m.rebuildItems()
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// Actions surfaces the discoverable view-level commands. The list's
// "Enter to connect" is intrinsic and stays inline; new/edit/delete
// are routed through Actions so embedders get buttons and the inline
// help line stays in sync.
//
// In the delete-confirm sub-state the Actions list flips to confirm/
// cancel pairs so native platform embedders can render them as
// buttons rather than relying on inline `y/n` prompts.
func (m connectionsModel) Actions() []Action {
	switch m.subState {
	case subStateConnDeleteConfirm:
		return []Action{
			{ID: "delete-confirm", Label: "Delete", Key: "y"},
			{ID: "delete-cancel", Label: "Cancel", Key: "n"},
		}
	case subStateConnForm:
		return nil
	}
	hasSelection := m.list.SelectedItem() != nil
	actions := []Action{{ID: "new-connection", Label: "New", Key: "n"}}
	if hasSelection {
		actions = append(actions,
			Action{ID: "edit-connection", Label: "Edit", Key: "e"},
			Action{ID: "delete-connection", Label: "Delete", Key: "d"},
		)
	}
	return actions
}

func (m connectionsModel) TriggerAction(id string) (connectionsModel, tea.Cmd) {
	switch id {
	case "new-connection":
		m.enterFormForNew()
		return m, m.nameInput.Focus()
	case "edit-connection":
		if item, ok := m.list.SelectedItem().(connectionItem); ok {
			m.enterFormForEdit(item.conn)
			return m, m.nameInput.Focus()
		}
	case "delete-connection":
		if item, ok := m.list.SelectedItem().(connectionItem); ok {
			m.deletingID = item.conn.ID
			m.subState = subStateConnDeleteConfirm
		}
	case "delete-confirm":
		return m.confirmDelete()
	case "delete-cancel":
		m.deletingID = ""
		m.subState = subStateConnList
	}
	return m, nil
}

func (m *connectionsModel) enterFormForNew() {
	m.subState = subStateConnForm
	m.editingID = ""
	m.formErr = ""
	m.formType = config.ConnTypeOpenClaw
	m.focusedField = 0

	m.nameInput = textinput.New()
	m.nameInput.Placeholder = "home pi"
	m.nameInput.CharLimit = 64

	m.urlInput = textinput.New()
	m.urlInput.CharLimit = 256
	m.applyTypeDefaults()

	m.modelInput = textinput.New()
	m.modelInput.Placeholder = "llama3.2"
	m.modelInput.CharLimit = 128
}

func (m *connectionsModel) enterFormForEdit(conn config.Connection) {
	m.subState = subStateConnForm
	m.editingID = conn.ID
	m.formErr = ""
	m.formType = conn.Type
	m.focusedField = 1 // skip the type radio — it's read-only on edit

	m.nameInput = textinput.New()
	m.nameInput.SetValue(conn.Name)
	m.nameInput.CharLimit = 64

	m.urlInput = textinput.New()
	m.urlInput.SetValue(conn.URL)
	m.urlInput.CharLimit = 256

	m.modelInput = textinput.New()
	m.modelInput.SetValue(conn.DefaultModel)
	m.modelInput.CharLimit = 128
}

// applyTypeDefaults updates the URL placeholder when the selected
// type changes so the user sees a hint that matches the chosen
// backend.
func (m *connectionsModel) applyTypeDefaults() {
	switch m.formType {
	case config.ConnTypeOpenAI:
		m.urlInput.Placeholder = "http://localhost:11434/v1"
	default:
		m.urlInput.Placeholder = "https://gateway.example.com"
	}
}

// formFields returns the focusable inputs for the current form
// state, in tab order. Type is included only on add; the model field
// is included only when type=OpenAI.
func (m connectionsModel) formFields() []formField {
	fields := []formField{}
	if m.editingID == "" {
		fields = append(fields, formFieldType)
	}
	fields = append(fields, formFieldName, formFieldURL)
	if m.formType == config.ConnTypeOpenAI {
		fields = append(fields, formFieldModel)
	}
	return fields
}

func (m connectionsModel) currentField() formField {
	fields := m.formFields()
	if m.focusedField < 0 || m.focusedField >= len(fields) {
		return formFieldName
	}
	return fields[m.focusedField]
}

func (m connectionsModel) handleFormKey(msg tea.KeyPressMsg) (connectionsModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.subState = subStateConnList
		return m, nil
	case "tab":
		return m.advanceFocus(1)
	case "shift+tab":
		return m.advanceFocus(-1)
	case "left", "right":
		// Type-radio navigation: cycle through ConnectionType options
		// when the radio is focused.
		if m.currentField() == formFieldType {
			m.cycleType(msg.String() == "right")
			return m, nil
		}
	case "enter":
		return m.submitForm()
	}
	return m.updateForm(msg)
}

// advanceFocus moves the focused-field index by delta, wrapping at
// either end and updating the textinput Focus/Blur flags so the cursor
// renders on the right field. Returns the mutated model along with the
// focus command — Bubble Tea value receivers can't mutate the caller's
// state, so the index update must round-trip through the return value.
func (m connectionsModel) advanceFocus(delta int) (connectionsModel, tea.Cmd) {
	fields := m.formFields()
	if len(fields) == 0 {
		return m, nil
	}
	m.focusedField = (m.focusedField + delta + len(fields)) % len(fields)
	m.nameInput.Blur()
	m.urlInput.Blur()
	m.modelInput.Blur()
	switch fields[m.focusedField] {
	case formFieldName:
		return m, m.nameInput.Focus()
	case formFieldURL:
		return m, m.urlInput.Focus()
	case formFieldModel:
		return m, m.modelInput.Focus()
	}
	return m, nil
}

// cycleType rotates the formType through AllConnectionTypes in either
// direction. Adjusts URL placeholder via applyTypeDefaults so the
// hint stays accurate.
func (m *connectionsModel) cycleType(forward bool) {
	types := config.AllConnectionTypes
	idx := 0
	for i, t := range types {
		if t == m.formType {
			idx = i
			break
		}
	}
	step := 1
	if !forward {
		step = -1
	}
	m.formType = types[(idx+step+len(types))%len(types)]
	m.applyTypeDefaults()
}

func (m connectionsModel) updateForm(msg tea.Msg) (connectionsModel, tea.Cmd) {
	var cmd tea.Cmd
	switch m.currentField() {
	case formFieldName:
		m.nameInput, cmd = m.nameInput.Update(msg)
	case formFieldURL:
		m.urlInput, cmd = m.urlInput.Update(msg)
	case formFieldModel:
		m.modelInput, cmd = m.modelInput.Update(msg)
	}
	return m, cmd
}

func (m connectionsModel) submitForm() (connectionsModel, tea.Cmd) {
	if m.store == nil {
		return m, nil
	}
	name := strings.TrimSpace(m.nameInput.Value())
	url := strings.TrimSpace(m.urlInput.Value())

	fields := config.ConnectionFields{
		Name:         name,
		Type:         m.formType,
		URL:          url,
		DefaultModel: strings.TrimSpace(m.modelInput.Value()),
	}
	if m.editingID == "" {
		if _, err := m.store.Add(fields); err != nil {
			m.formErr = err.Error()
			return m, nil
		}
	} else {
		if err := m.store.Update(m.editingID, fields); err != nil {
			m.formErr = err.Error()
			return m, nil
		}
	}
	m.subState = subStateConnList
	m.formErr = ""
	m.rebuildItems()
	return m, func() tea.Msg { return connectionsChangedMsg{} }
}

func (m connectionsModel) handleDeleteKey(msg tea.KeyPressMsg) (connectionsModel, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		return m.confirmDelete()
	case "n", "esc":
		m.deletingID = ""
		m.subState = subStateConnList
		return m, nil
	}
	return m, nil
}

func (m connectionsModel) confirmDelete() (connectionsModel, tea.Cmd) {
	if m.store == nil || m.deletingID == "" {
		m.subState = subStateConnList
		return m, nil
	}
	if err := m.store.Delete(m.deletingID); err != nil {
		m.lastErr = err
	}
	m.deletingID = ""
	m.subState = subStateConnList
	m.rebuildItems()
	return m, func() tea.Msg { return connectionsChangedMsg{} }
}

// wantsInput reports whether the form fields are focused. The list and
// confirm states use single-key navigation only.
func (m connectionsModel) wantsInput() bool {
	return m.subState == subStateConnForm
}

func (m *connectionsModel) setSize(w, h int) {
	m.width = w
	m.height = h
	m.list.SetSize(w, h-3)
}

func (m connectionsModel) View() string {
	switch m.subState {
	case subStateConnForm:
		return m.viewForm()
	case subStateConnDeleteConfirm:
		return m.viewDeleteConfirm()
	}
	return m.viewList()
}

func (m connectionsModel) viewList() string {
	var b strings.Builder
	if m.lastErr != nil {
		b.WriteString("\n")
		b.WriteString(errorStyle.Render(fmt.Sprintf("  Error: %v", m.lastErr)))
		b.WriteString("\n")
	}
	if m.store == nil || len(m.store.Connections) == 0 {
		b.WriteString("\n")
		b.WriteString(headerStyle.Render(" Connections "))
		b.WriteString("\n\n")
		b.WriteString("  No connections yet.\n")
		b.WriteString("  Press n to add one.\n\n")
	} else {
		b.WriteString(m.list.View())
		b.WriteString("\n")
	}
	if !m.hideHints {
		b.WriteString(helpStyle.Render(renderActionHints(m.Actions())))
	}
	return b.String()
}

func (m connectionsModel) viewForm() string {
	var b strings.Builder
	title := " New connection "
	if m.editingID != "" {
		title = " Edit connection "
	}
	b.WriteString("\n")
	b.WriteString(headerStyle.Render(title))
	b.WriteString("\n\n")

	b.WriteString("  Type:")
	if m.editingID == "" && m.currentField() == formFieldType {
		b.WriteString(helpStyle.Render("  (← →)"))
	}
	b.WriteString("\n")
	b.WriteString("  ")
	for i, t := range config.AllConnectionTypes {
		marker := "( )"
		if t == m.formType {
			marker = "(•)"
		}
		label := t.Label()
		if m.editingID == "" && m.currentField() == formFieldType && t == m.formType {
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(accent).Render(marker + " " + label))
		} else if m.editingID != "" && t != m.formType {
			b.WriteString(helpStyle.Render(marker + " " + label))
		} else {
			b.WriteString(marker + " " + label)
		}
		if i < len(config.AllConnectionTypes)-1 {
			b.WriteString("   ")
		}
	}
	b.WriteString("\n\n")

	b.WriteString("  Name:\n")
	b.WriteString("  " + m.nameInput.View() + "\n\n")

	urlLabel := "Gateway URL:"
	if m.formType == config.ConnTypeOpenAI {
		urlLabel = "Base URL:"
	}
	b.WriteString("  " + urlLabel + "\n")
	b.WriteString("  " + m.urlInput.View() + "\n\n")

	if m.formType == config.ConnTypeOpenAI {
		b.WriteString("  Default model (optional):\n")
		b.WriteString("  " + m.modelInput.View() + "\n\n")
	}

	if m.formErr != "" {
		b.WriteString(errorStyle.Render("  " + m.formErr))
		b.WriteString("\n\n")
	}
	if !m.hideHints {
		b.WriteString(helpStyle.Render("  Tab: switch fields | Enter: save | Esc: cancel"))
		b.WriteString("\n")
	}
	return b.String()
}

func (m connectionsModel) viewDeleteConfirm() string {
	var name string
	if m.store != nil {
		if conn := m.store.Find(m.deletingID); conn != nil {
			name = conn.Name
		}
	}
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(headerStyle.Render(" Delete connection "))
	b.WriteString("\n\n")
	if name != "" {
		b.WriteString(fmt.Sprintf("  Delete connection %q?\n", name))
	} else {
		b.WriteString("  Delete this connection?\n")
	}
	b.WriteString("  Stored device tokens for this URL are kept on disk; only the entry is removed.\n\n")
	if !m.hideHints {
		b.WriteString(helpStyle.Render(renderActionHints(m.Actions())))
	}
	return b.String()
}
