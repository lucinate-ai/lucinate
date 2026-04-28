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

	// Form state shared by add and edit.
	editingID    string
	nameInput    textinput.Model
	urlInput     textinput.Model
	focusedField int // 0 = name, 1 = url
	formErr      string

	// Delete confirm state.
	deletingID string

	lastErr error

	width  int
	height int
}

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
	m.focusedField = 0

	m.nameInput = textinput.New()
	m.nameInput.Placeholder = "home pi"
	m.nameInput.CharLimit = 64

	m.urlInput = textinput.New()
	m.urlInput.Placeholder = "https://gateway.example.com"
	m.urlInput.CharLimit = 256
}

func (m *connectionsModel) enterFormForEdit(conn config.Connection) {
	m.subState = subStateConnForm
	m.editingID = conn.ID
	m.formErr = ""
	m.focusedField = 0

	m.nameInput = textinput.New()
	m.nameInput.SetValue(conn.Name)
	m.nameInput.CharLimit = 64

	m.urlInput = textinput.New()
	m.urlInput.SetValue(conn.URL)
	m.urlInput.CharLimit = 256
}

func (m connectionsModel) handleFormKey(msg tea.KeyPressMsg) (connectionsModel, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.subState = subStateConnList
		return m, nil
	case "tab", "shift+tab":
		return m, m.switchFormFocus()
	case "enter":
		return m.submitForm()
	}
	return m.updateForm(msg)
}

func (m connectionsModel) switchFormFocus() tea.Cmd {
	if m.focusedField == 0 {
		m.focusedField = 1
		m.nameInput.Blur()
		return m.urlInput.Focus()
	}
	m.focusedField = 0
	m.urlInput.Blur()
	return m.nameInput.Focus()
}

func (m connectionsModel) updateForm(msg tea.Msg) (connectionsModel, tea.Cmd) {
	var cmd tea.Cmd
	if m.focusedField == 0 {
		m.nameInput, cmd = m.nameInput.Update(msg)
	} else {
		m.urlInput, cmd = m.urlInput.Update(msg)
	}
	return m, cmd
}

func (m connectionsModel) submitForm() (connectionsModel, tea.Cmd) {
	if m.store == nil {
		return m, nil
	}
	name := strings.TrimSpace(m.nameInput.Value())
	url := strings.TrimSpace(m.urlInput.Value())

	if m.editingID == "" {
		if _, err := m.store.Add(name, config.ConnTypeOpenClaw, url); err != nil {
			m.formErr = err.Error()
			return m, nil
		}
	} else {
		if err := m.store.Update(m.editingID, name, url); err != nil {
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

	b.WriteString("  Type: OpenClaw\n\n")

	b.WriteString("  Name:\n")
	b.WriteString("  " + m.nameInput.View() + "\n\n")

	b.WriteString("  Gateway URL:\n")
	b.WriteString("  " + m.urlInput.View() + "\n\n")

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
