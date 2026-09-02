package app

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
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
					label = "Demo"
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
		sections += wrap.Render("Demo (streaming):\n" + m.snapshot.LiveAssistant)
	}
	m.viewport.SetContent(sections)
	m.viewport.GotoBottom()
}

func (m *Model) View() tea.View {
	header := headerStyle.Render("eino-tui · credential-free demo")
	notice := m.snapshot.Notice
	if notice == "" {
		notice = phaseText(m.phase)
	}
	content := header + "\n" + notice + "\n" + m.viewport.View() + "\n" + m.textarea.View() + "\nEnter send · Alt+Enter newline · Esc interrupt · Ctrl+C quit"
	view := tea.NewView(content)
	view.AltScreen = true
	return view
}

func phaseText(phase runtimeui.Phase) string {
	switch phase {
	case runtimeui.PhaseStarting:
		return "Starting demo response…"
	case runtimeui.PhaseRunning:
		return "Streaming scripted demo response…"
	case runtimeui.PhaseRecoveryWaiting, runtimeui.PhaseRecovering:
		return runtimeui.NoticeRecoveryWaiting
	default:
		return "Local scripted output; no network model call."
	}
}
