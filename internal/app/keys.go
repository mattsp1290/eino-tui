package app

import (
	"errors"

	tea "charm.land/bubbletea/v2"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
	"github.com/mattsp1290/eino-tui/internal/textsafe"
)

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Keystroke() {
	case "ctrl+c":
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
		return m, m.startCmd(prompt)
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
