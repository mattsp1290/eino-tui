package app

import (
	"errors"

	tea "charm.land/bubbletea/v2"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
	"github.com/mattsp1290/eino-tui/internal/textsafe"
)

const busyConversationHint = "Finish the response or press Esc to interrupt it before changing conversations."

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.startup != startupReady {
		return m.handleStartupKey(msg)
	}
	if m.conv.mode != convClosed {
		return m.handleConversationKey(msg)
	}
	if m.picker.mode != pickerClosed {
		return m.handlePickerKey(msg)
	}
	switch msg.Keystroke() {
	case "ctrl+c":
		m.cancelCatalogRequest()
		return m, func() tea.Msg { return tea.Quit() }
	case "ctrl+d":
		if m.snapshot.Phase == runtimeui.PhaseIdle {
			return m, func() tea.Msg { return tea.Quit() }
		}
		return m, nil
	case "esc":
		if m.snapshot.Phase == runtimeui.PhaseStarting || m.snapshot.Phase == runtimeui.PhaseRunning {
			return m, m.interruptCmd()
		}
		return m, nil
	case "alt+m":
		if m.snapshot.Phase == runtimeui.PhaseIdle {
			return m, m.openPicker()
		}
		return m, nil
	case "alt+s":
		if m.snapshot.Phase != runtimeui.PhaseIdle {
			m.snapshot.Notice = busyConversationHint
			return m, nil
		}
		return m, m.openConversationPicker()
	case "alt+n":
		if m.snapshot.Phase != runtimeui.PhaseIdle {
			m.snapshot.Notice = busyConversationHint
			return m, nil
		}
		return m, m.beginCreate()
	case "alt+r":
		if m.snapshot.Phase != runtimeui.PhaseIdle {
			m.snapshot.Notice = busyConversationHint
			return m, nil
		}
		m.openRename()
		return m, nil
	case "enter":
		if m.snapshot.Phase != runtimeui.PhaseIdle {
			return m, nil
		}
		prompt, err := textsafe.Prompt(m.textarea.Value())
		if errors.Is(err, textsafe.ErrBlankPrompt) {
			return m, nil
		}
		if err != nil {
			m.snapshot.Notice = "Message is too large."
			return m, nil
		}
		m.snapshot.Phase = runtimeui.PhaseStarting
		m.snapshot.Notice = ""
		cfg := runtimeui.StartConfig{Selection: m.selected.selection, ReasoningEffort: m.selected.effort}
		return m, m.startCmd(prompt, cfg)
	}
	if m.snapshot.Phase != runtimeui.PhaseIdle {
		return m, nil
	}
	before := m.textarea.Value()
	updated, cmd := m.textarea.Update(msg)
	m.textarea = updated
	if normalized, err := textsafe.Input(m.textarea.Value()); err != nil {
		m.textarea.SetValue(before)
	} else if normalized != m.textarea.Value() {
		m.textarea.SetValue(normalized)
	}
	return m, cmd
}

// handleStartupKey owns input until the initial conversation has loaded.
// Prompt submission stays hidden; only retry and quit are available.
func (m *Model) handleStartupKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Keystroke() {
	case "ctrl+c":
		return m, func() tea.Msg { return tea.Quit() }
	case "r", "enter":
		if (m.startup == startupFailed || m.startup == startupReconcile) && !m.loading {
			return m, m.loadCmd()
		}
	}
	return m, nil
}
