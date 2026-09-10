package app

import (
	"context"
	"errors"

	tea "charm.land/bubbletea/v2"
	"github.com/mattsp1290/eino-tui/internal/conversationnames"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
	"github.com/mattsp1290/eino-tui/internal/textsafe"
)

// openRename starts the manual rename editor seeded with the safe current
// title. Nothing is written until an explicit nonblank Enter.
func (m *Model) openRename() {
	m.cancelConversationRequest()
	m.conv.op++
	m.conv.mode = convRenaming
	m.conv.err = ""
	m.conv.renameAfterSelect = false
	m.textarea.Blur()
	m.conv.input.SetValue(oneLine(m.current.Title))
	m.conv.input.CursorEnd()
	m.conv.input.Focus()
}

func (m *Model) pasteRename(content string) {
	value := oneLine(m.conv.input.Value() + content)
	m.conv.input.SetValue(value)
	m.conv.input.CursorEnd()
}

func (m *Model) handleRenameKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Keystroke() {
	case "esc":
		m.closeConversationDialog()
		return m, nil
	case "enter":
		title, err := conversationnames.NormalizeTitle(m.conv.input.Value())
		if err != nil {
			if textsafe.Display(m.conv.input.Value()) == "" {
				return m, nil
			}
			m.conv.err = noticeRenameInvalid
			return m, nil
		}
		return m, m.renameCmd(title)
	}
	before := m.conv.input.Value()
	updated, cmd := m.conv.input.Update(msg)
	m.conv.input = updated
	if normalized := textsafe.Display(m.conv.input.Value()); normalized != m.conv.input.Value() {
		if normalized == "" && before != "" {
			m.conv.input.SetValue(before)
		} else {
			m.conv.input.SetValue(normalized)
		}
	}
	return m, cmd
}

func (m *Model) renameCmd(title string) tea.Cmd {
	m.cancelConversationRequest()
	m.conv.op++
	op := m.conv.op
	ctx, cancel := context.WithCancel(m.ctx)
	m.conv.cancel = cancel
	m.conv.mode = convSaving
	m.conv.err = ""
	generation := m.current.Generation
	id := m.current.ID
	service := m.service
	return func() tea.Msg {
		info, err := service.RenameConversation(ctx, id, generation, title)
		return conversationRenamedMsg{op: op, generation: generation, info: info, err: err}
	}
}

func (m *Model) applyRenamedResult(msg conversationRenamedMsg) {
	if m.conv.mode != convSaving || msg.op != m.conv.op || msg.generation != m.current.Generation {
		return
	}
	m.cancelConversationRequest()
	if msg.err != nil {
		switch {
		case errors.Is(msg.err, context.Canceled), errors.Is(msg.err, runtimeui.ErrClosing):
			m.closeConversationDialog()
		case errors.Is(msg.err, runtimeui.ErrInvalidTitle):
			m.conv.mode = convRenaming
			m.conv.err = noticeRenameInvalid
			m.conv.input.Focus()
		case errors.Is(msg.err, runtimeui.ErrBusy), errors.Is(msg.err, runtimeui.ErrStaleGeneration):
			m.closeConversationDialog()
			m.snapshot.Notice = noticeConversationsChanging
		default:
			// The editor keeps its content so the user can retry.
			m.conv.mode = convRenaming
			m.conv.err = noticeRenameFailed
			m.conv.input.Focus()
		}
		return
	}
	m.applyConversationInfo(msg.info)
	m.closeConversationDialog()
}

func (m *Model) renameView(width, height int) []string {
	hint := pickerHint(width, "Enter save · Esc cancel", "Enter · Esc", "↵/Esc")
	input := m.conv.input.View()
	if height == 1 {
		return []string{input}
	}
	if height == 2 {
		return []string{input, hint}
	}
	lines := []string{"Rename conversation", input}
	if m.conv.err != "" && height >= 4 {
		lines = append(lines, m.conv.err)
	}
	return append(lines, hint)
}
