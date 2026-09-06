package app

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
)

func (m *Model) refreshTranscript() {
	width := m.viewport.Width()
	if width < 1 {
		width = 1
	}
	wrap := transcriptStyle(width)
	if m.stableWidth != width || !slices.Equal(m.stableMessages, m.snapshot.Messages) {
		var stableSections []string
		for _, message := range m.snapshot.Messages {
			body := message.Content
			if message.Role != runtimeui.RoleNotice {
				label := "You"
				if message.Role == runtimeui.RoleAssistant {
					label = "Codex"
				}
				body = label + ":\n" + message.Content
			}
			if message.Status == runtimeui.StatusInterrupted {
				body += "\n[interrupted]"
			}
			if message.Status == runtimeui.StatusFailed {
				body += "\n[failed]"
			}
			stableSections = append(stableSections, wrap.Render(body))
		}
		m.stableTranscript = strings.Join(stableSections, "\n\n")
		m.stableMessages = slices.Clone(m.snapshot.Messages)
		m.stableWidth = width
	}
	sections := m.stableTranscript
	if m.snapshot.LiveAssistant != "" {
		if sections != "" {
			sections += "\n\n"
		}
		sections += wrap.Render("Codex (streaming):\n" + m.snapshot.LiveAssistant)
	}
	m.viewport.SetContent(sections)
	m.viewport.GotoBottom()
}

func (m *Model) View() tea.View {
	headerText := "eino-tui · Codex subscription · " + m.selected.displayName + " (" + string(m.selected.selection.ModelID) + ") · " + m.selected.effort
	if m.terminalWidth > 0 && ansi.StringWidth(headerText) > m.terminalWidth {
		headerText = "eino-tui · " + string(m.selected.selection.ModelID) + " · " + m.selected.effort
	}
	header := headerStyle.Render(boundedLine(headerText, m.terminalWidth))
	notice := m.snapshot.Notice
	if notice == "" {
		notice = phaseText(m.snapshot.Phase)
	}
	var content string
	if m.picker.mode != pickerClosed {
		if m.terminalHeight <= 1 {
			content = m.pickerView(m.terminalWidth, 1)
		} else {
			content = header + "\n" + m.pickerView(m.terminalWidth, m.terminalHeight-1)
		}
	} else {
		footer := boundedLine("Enter send · Alt+Enter newline · Alt+M models · Esc interrupt · Ctrl+C quit", m.terminalWidth)
		content = header + "\n" + notice + "\n" + m.viewport.View() + "\n" + m.textarea.View() + "\n" + footer
	}
	view := tea.NewView(content)
	view.AltScreen = true
	return view
}

func boundedLine(value string, width int) string {
	if width < 1 {
		width = 1
	}
	return ansi.Truncate(value, width, "…")
}

func phaseText(phase runtimeui.Phase) string {
	switch phase {
	case runtimeui.PhaseStarting:
		return "Starting Codex response…"
	case runtimeui.PhaseRunning:
		return "Streaming Codex response…"
	case runtimeui.PhaseRecoveryWaiting, runtimeui.PhaseRecovering:
		return runtimeui.NoticeRecoveryWaiting
	default:
		return "Codex subscription ready."
	}
}
