package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/conversationnames"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
)

type conversationMode uint8

const (
	convClosed conversationMode = iota
	convLoading
	convChoosing
	convCreating
	convSelecting
	convRenaming
	convSaving
	convRetrySelect
	convReconcile
)

const (
	noticeDirectoryUnavailable  = "Conversations could not be listed."
	noticeDirectoryExpired      = "The conversation list changed; refresh to continue."
	noticeConversationUnavail   = "That conversation is unavailable."
	noticeCreateFailed          = "A new conversation could not be created."
	noticeSelectionUnsaved      = "The conversation selection could not be saved. Try again."
	noticePreferencesInvalid    = "Workspace preferences are invalid; choose another state directory."
	noticeRenameFailed          = "The conversation could not be renamed."
	noticeRenameInvalid         = "Enter a nonblank title of at most 256 characters."
	noticeDraftBudget           = "Send or clear the current draft before switching conversations."
	noticeReconcileRequired     = "Selection unconfirmed. Press R to retry or Ctrl+C to quit."
	noticeCreatedNotSelected    = "The new conversation was created but could not be selected."
	noticeStartupFailed         = "The workspace could not be opened. Press R to retry or Ctrl+C to quit."
	noticeStartupLoading        = "Opening workspace…"
	noticeConversationsChanging = "Finish the current response first."
)

// conversationState owns the conversation dialogs: directory picker,
// mutation progress, the rename editor, and reconciliation. Exactly one dialog
// owns focus at a time and it is never open together with the model picker.
type conversationState struct {
	mode              conversationMode
	page              runtimeui.ConversationPage
	cursors           []string
	highlighted       int
	highlightedID     session.ID
	op                uint64
	cancel            context.CancelFunc
	err               string
	input             textinput.Model
	renameAfterSelect bool
	pendingID         session.ID
}

func newConversationState() conversationState {
	input := textinput.New()
	input.Prompt = "> "
	input.Placeholder = "Conversation title"
	input.CharLimit = conversationnames.MaxTitleRunes
	return conversationState{mode: convClosed, input: input, cursors: []string{""}}
}

func (m *Model) cancelConversationRequest() {
	if m.conv.cancel != nil {
		m.conv.cancel()
		m.conv.cancel = nil
	}
}

// beginOperation cancels any prior dialog read and returns the context and
// operation ID that tag the next asynchronous result.
func (m *Model) beginOperation(mode conversationMode) (context.Context, uint64) {
	m.cancelConversationRequest()
	m.conv.op++
	ctx, cancel := context.WithCancel(m.ctx)
	m.conv.cancel = cancel
	m.conv.mode = mode
	m.conv.err = ""
	m.textarea.Blur()
	return ctx, m.conv.op
}

func (m *Model) closeConversationDialog() {
	m.cancelConversationRequest()
	m.conv.op++
	m.conv.mode = convClosed
	m.conv.err = ""
	m.conv.renameAfterSelect = false
	m.conv.pendingID = ""
	m.conv.input.Blur()
	m.textarea.Focus()
}

func (m *Model) openConversationPicker() tea.Cmd {
	m.conv.page = runtimeui.ConversationPage{}
	m.conv.highlightedID = m.current.ID
	return m.listCmd([]string{""})
}

// listCmd reads one directory page. cursors is the intended navigation stack
// after this page loads; its last element is the cursor being requested.
func (m *Model) listCmd(cursors []string) tea.Cmd {
	ctx, op := m.beginOperation(convLoading)
	cursor := cursors[len(cursors)-1]
	service := m.service
	return func() tea.Msg {
		page, err := service.ListConversations(ctx, cursor)
		return directoryLoadedMsg{op: op, cursors: cursors, page: page, err: err}
	}
}

func (m *Model) beginCreate() tea.Cmd {
	if !m.draftFits(m.textarea.Value()) {
		m.closeConversationDialog()
		m.snapshot.Notice = noticeDraftBudget
		return nil
	}
	ctx, op := m.beginOperation(convCreating)
	generation := m.current.Generation
	service := m.service
	return func() tea.Msg {
		result, err := service.CreateConversation(ctx, generation)
		return conversationCreatedMsg{op: op, generation: generation, result: result, err: err}
	}
}

func (m *Model) beginSelect(id session.ID) tea.Cmd {
	if !m.draftFits(m.textarea.Value()) {
		m.closeConversationDialog()
		m.snapshot.Notice = noticeDraftBudget
		return nil
	}
	ctx, op := m.beginOperation(convSelecting)
	generation := m.current.Generation
	service := m.service
	return func() tea.Msg {
		result, err := service.SelectConversation(ctx, id, generation)
		return conversationSelectedMsg{op: op, generation: generation, id: id, result: result, err: err}
	}
}

func (m *Model) applyDirectoryResult(msg directoryLoadedMsg) {
	if m.conv.mode != convLoading || msg.op != m.conv.op {
		return
	}
	m.cancelConversationRequest()
	if msg.err != nil {
		switch {
		case errors.Is(msg.err, context.Canceled):
			m.closeConversationDialog()
		case errors.Is(msg.err, runtimeui.ErrDirectoryCursor):
			m.conv.mode = convChoosing
			m.conv.err = noticeDirectoryExpired
		case errors.Is(msg.err, runtimeui.ErrClosing):
			m.closeConversationDialog()
		default:
			m.conv.mode = convChoosing
			m.conv.err = noticeDirectoryUnavailable
		}
		return
	}
	m.conv.mode = convChoosing
	m.conv.page = msg.page
	m.conv.cursors = msg.cursors
	m.conv.highlighted = 0
	for i, item := range msg.page.Items {
		if item.ID == m.conv.highlightedID {
			m.conv.highlighted = i
			break
		}
	}
	if len(msg.page.Items) > 0 {
		m.conv.highlightedID = msg.page.Items[m.conv.highlighted].ID
	}
}

func (m *Model) applyCreatedResult(msg conversationCreatedMsg) tea.Cmd {
	if m.conv.mode != convCreating || msg.op != m.conv.op || msg.generation != m.current.Generation {
		return nil
	}
	m.cancelConversationRequest()
	if msg.err != nil {
		switch {
		case errors.Is(msg.err, runtimeui.ErrReconciliationRequired):
			m.conv.mode = convReconcile
			m.conv.err = noticeReconcileRequired
		case errors.Is(msg.err, runtimeui.ErrPreferenceFailed) && msg.result.CommittedID != "":
			m.conv.mode = convRetrySelect
			m.conv.pendingID = msg.result.CommittedID
			m.conv.err = noticeCreatedNotSelected
		case errors.Is(msg.err, context.Canceled), errors.Is(msg.err, runtimeui.ErrClosing):
			m.closeConversationDialog()
		case errors.Is(msg.err, runtimeui.ErrPreferenceInvalid):
			m.closeConversationDialog()
			m.snapshot.Notice = noticePreferencesInvalid
		case errors.Is(msg.err, runtimeui.ErrBusy), errors.Is(msg.err, runtimeui.ErrStaleGeneration):
			m.closeConversationDialog()
			m.snapshot.Notice = noticeConversationsChanging
		default:
			m.closeConversationDialog()
			m.snapshot.Notice = noticeCreateFailed
		}
		return nil
	}
	return m.installSelection(msg.result)
}

func (m *Model) applySelectedResult(msg conversationSelectedMsg) tea.Cmd {
	if m.conv.mode != convSelecting || msg.op != m.conv.op || msg.generation != m.current.Generation {
		return nil
	}
	m.cancelConversationRequest()
	if msg.err != nil {
		rename := m.conv.renameAfterSelect
		switch {
		case errors.Is(msg.err, runtimeui.ErrReconciliationRequired):
			m.conv.mode = convReconcile
			m.conv.err = noticeReconcileRequired
		case errors.Is(msg.err, context.Canceled), errors.Is(msg.err, runtimeui.ErrClosing):
			m.closeConversationDialog()
		case errors.Is(msg.err, runtimeui.ErrPreferenceInvalid):
			m.closeConversationDialog()
			m.snapshot.Notice = noticePreferencesInvalid
		case errors.Is(msg.err, runtimeui.ErrPreferenceFailed):
			m.closeConversationDialog()
			m.snapshot.Notice = noticeSelectionUnsaved
		case errors.Is(msg.err, runtimeui.ErrBusy), errors.Is(msg.err, runtimeui.ErrStaleGeneration):
			m.closeConversationDialog()
			m.snapshot.Notice = noticeConversationsChanging
		default:
			m.closeConversationDialog()
			m.snapshot.Notice = noticeConversationUnavail
		}
		_ = rename
		return nil
	}
	rename := m.conv.renameAfterSelect
	cmd := m.installSelection(msg.result)
	if rename && m.idle() {
		m.openRename()
	}
	return cmd
}

// installSelection publishes a committed selection: drafts swap, every
// per-run cache is dropped, and the destination snapshot is rendered.
func (m *Model) installSelection(result runtimeui.SelectionResult) tea.Cmd {
	previous := m.current.ID
	info := result.Snapshot.Conversation
	m.closeConversationDialog()
	m.switchDrafts(previous, info.ID)
	m.installConversation(info)
	m.applyLoad(result.Snapshot)
	if m.snapshot.Notice == "" && result.DurabilityWarning {
		m.snapshot.Notice = runtimeui.NoticeSelectionUnconfirmed
	}
	if m.snapshot.Phase == runtimeui.PhaseRecoveryWaiting {
		return recoveryTimer(m.ctx, m.snapshot.RecoveryAt)
	}
	return nil
}

func (m *Model) handleConversationKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.Keystroke() == "ctrl+c" {
		m.cancelConversationRequest()
		return m, func() tea.Msg { return tea.Quit() }
	}
	switch m.conv.mode {
	case convRenaming:
		return m.handleRenameKey(msg)
	case convLoading:
		if msg.Keystroke() == "esc" {
			m.closeConversationDialog()
		}
		return m, nil
	case convCreating, convSelecting, convSaving:
		// Esc requests cancellation; the dialog stays until the authoritative
		// result arrives so a committed write is never reported as rolled back.
		if msg.Keystroke() == "esc" {
			m.cancelConversationRequest()
		}
		return m, nil
	case convRetrySelect:
		switch msg.Keystroke() {
		case "enter", "r":
			return m, m.beginSelect(m.conv.pendingID)
		case "esc":
			m.closeConversationDialog()
		}
		return m, nil
	case convReconcile:
		if (msg.Keystroke() == "r" || msg.Keystroke() == "enter") && !m.loading {
			return m, m.loadCmd()
		}
		return m, nil
	case convChoosing:
		return m.handleChoosingKey(msg)
	}
	return m, nil
}

func (m *Model) handleChoosingKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	items := m.conv.page.Items
	switch msg.Keystroke() {
	case "esc":
		m.closeConversationDialog()
	case "ctrl+r":
		return m, m.listCmd([]string{""})
	case "n":
		return m, m.beginCreate()
	case "up", "k":
		if m.conv.highlighted > 0 {
			m.conv.highlighted--
			m.conv.highlightedID = items[m.conv.highlighted].ID
		}
	case "down", "j":
		if m.conv.highlighted+1 < len(items) {
			m.conv.highlighted++
			m.conv.highlightedID = items[m.conv.highlighted].ID
		}
	case "right":
		if m.conv.page.NextCursor != "" {
			return m, m.listCmd(append(append([]string(nil), m.conv.cursors...), m.conv.page.NextCursor))
		}
	case "left":
		if len(m.conv.cursors) > 1 {
			return m, m.listCmd(append([]string(nil), m.conv.cursors[:len(m.conv.cursors)-1]...))
		}
	case "enter", "r":
		if len(items) == 0 || m.conv.highlighted >= len(items) {
			return m, nil
		}
		item := items[m.conv.highlighted]
		if item.Unavailable {
			m.conv.err = noticeConversationUnavail
			return m, nil
		}
		if msg.Keystroke() == "r" {
			// Rename never mutates a hidden conversation: select first, then edit.
			if item.ID == m.current.ID {
				m.openRename()
				return m, nil
			}
			m.conv.renameAfterSelect = true
		}
		if item.ID == m.current.ID && msg.Keystroke() == "enter" {
			m.closeConversationDialog()
			return m, nil
		}
		return m, m.beginSelect(item.ID)
	}
	return m, nil
}

func (m *Model) conversationView(width, height int) string {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	var lines []string
	switch m.conv.mode {
	case convLoading:
		lines = compactDialogState(width, height, dialogTitle, "Loading conversations…", "Load", pickerHint(width, "Esc cancel", "Esc"))
	case convCreating:
		lines = compactDialogState(width, height, dialogTitle, "Creating a new conversation…", "New", pickerHint(width, "Esc request cancel · Ctrl+C quit", "Esc · Ctrl+C", "Esc"))
	case convSelecting:
		lines = compactDialogState(width, height, dialogTitle, "Opening conversation…", "Open", pickerHint(width, "Esc request cancel · Ctrl+C quit", "Esc · Ctrl+C", "Esc"))
	case convSaving:
		lines = compactDialogState(width, height, "Rename conversation", "Saving conversation title…", "Save", pickerHint(width, "Esc request cancel · Ctrl+C quit", "Esc · Ctrl+C", "Esc"))
	case convRetrySelect:
		lines = compactDialogState(width, height, dialogTitle, noticeCreatedNotSelected, "Retry", pickerHint(width, "Enter retry · Esc close", "Enter · Esc", "↵/Esc"))
	case convReconcile:
		lines = compactDialogState(width, height, dialogTitle, noticeReconcileRequired, "Sync", pickerHint(width, "R retry · Ctrl+C quit", "R · Ctrl+C", "R"))
	case convRenaming:
		lines = m.renameView(width, height)
	case convChoosing:
		lines = m.choosingView(width, height)
	}
	for i := range lines {
		lines[i] = boundedLine(strings.TrimRight(lines[i], " "), width)
	}
	if len(lines) > height {
		lines = lines[len(lines)-height:]
	}
	return strings.Join(lines, "\n")
}

const dialogTitle = "Conversations"

func (m *Model) choosingView(width, height int) []string {
	hint := pickerHint(width, "↑/↓ choose · Enter open · N new · R rename · Ctrl+R refresh · ←/→ page · Esc close", "Enter open · N new · R rename · Esc close", "Enter · N · R · Esc", "↵/Esc")
	if height == 1 {
		return []string{hint}
	}
	title := fmt.Sprintf("Conversations · page %d", len(m.conv.cursors))
	if m.conv.page.NextCursor != "" {
		title += " →"
	}
	if len(m.conv.cursors) > 1 {
		title = "← " + title
	}
	items := m.conv.page.Items
	var extra []string
	if m.conv.err != "" {
		extra = append(extra, m.conv.err)
	} else if len(items) == 0 {
		extra = append(extra, "No conversations yet.")
	}
	if height == 2 {
		if len(items) > 0 {
			return []string{m.conversationLine(m.conv.highlighted, width), hint}
		}
		if len(extra) > 0 {
			return []string{extra[0], hint}
		}
		return []string{title, hint}
	}
	room := height - 2 - len(extra)
	if room < 1 {
		room = 1
	}
	start := m.conv.highlighted - room/2
	if start < 0 {
		start = 0
	}
	end := min(len(items), start+room)
	if end-start < room {
		start = max(0, end-room)
	}
	lines := []string{title}
	for i := start; i < end; i++ {
		lines = append(lines, m.conversationLine(i, width))
	}
	lines = append(lines, extra...)
	lines = append(lines, hint)
	return lines
}

func (m *Model) conversationLine(index int, width int) string {
	item := m.conv.page.Items[index]
	marker := "  "
	if index == m.conv.highlighted {
		marker = "> "
	}
	selected := " "
	if item.Selected {
		selected = "*"
	}
	label := oneLine(item.Title)
	if item.Unavailable {
		label = "(unavailable)"
	}
	created := ""
	if !item.CreatedAt.IsZero() {
		created = " · " + item.CreatedAt.Local().Format("2006-01-02 15:04")
	}
	line := marker + selected + label + created
	if boundedLine(line, width) == line {
		return line
	}
	return marker + selected + label
}
