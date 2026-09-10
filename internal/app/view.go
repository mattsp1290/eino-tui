package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
	"github.com/mattsp1290/eino-tui/internal/textsafe"
)

func (m *Model) refreshTranscript() {
	width := m.viewport.Width()
	if width < 1 {
		width = 1
	}
	wrap := transcriptStyle(width)
	if m.stableWidth != width || !messagesEqual(m.stableMessages, m.snapshot.Messages) {
		var stableSections []string
		seenMessages := make(map[string]bool)
		seenTools := make(map[string]bool)
		for _, message := range m.snapshot.Messages {
			if body := renderMessage(message, false, seenMessages, seenTools); body != "" {
				stableSections = append(stableSections, wrap.Render(body))
			}
		}
		m.stableTranscript = strings.Join(stableSections, "\n\n")
		m.stableMessages = cloneRuntimeMessages(m.snapshot.Messages)
		m.stableWidth = width
	}
	sections := m.stableTranscript
	seenMessages, seenTools := snapshotIDs(m.snapshot.Messages)
	var liveSections []string
	for _, message := range m.snapshot.LiveMessages {
		if body := renderMessage(message, true, seenMessages, seenTools); body != "" {
			liveSections = append(liveSections, wrap.Render(body))
		}
	}
	if live := strings.Join(liveSections, "\n\n"); live != "" {
		if sections != "" {
			sections += "\n\n"
		}
		sections += live
	}
	m.viewport.SetContent(sections)
	m.viewport.GotoBottom()
}

func renderMessage(message runtimeui.Message, streaming bool, seenMessages, seenTools map[string]bool) string {
	if message.ID != "" {
		if seenMessages[message.ID] {
			return ""
		}
		seenMessages[message.ID] = true
	}
	var lines []string
	content := textsafe.Display(message.Content)
	if message.Role == runtimeui.RoleNotice {
		if content != "" {
			lines = append(lines, content)
		}
	} else if content != "" {
		label := "You"
		if message.Role == runtimeui.RoleAssistant {
			label = "Codex"
			if streaming {
				label += " (streaming)"
			}
		}
		lines = append(lines, label+":\n"+content)
	}
	for _, activity := range message.Tools {
		if activity.ID == "" || seenTools[activity.ID] {
			continue
		}
		seenTools[activity.ID] = true
		if row := renderToolActivity(activity); row != "" {
			lines = append(lines, row)
		}
	}
	if message.Status == runtimeui.StatusInterrupted {
		lines = append(lines, "[interrupted]")
	}
	if message.Status == runtimeui.StatusFailed {
		lines = append(lines, "[failed]")
	}
	return strings.Join(lines, "\n")
}

func renderToolActivity(activity runtimeui.ToolActivity) string {
	status := string(activity.Status)
	switch activity.Status {
	case runtimeui.ToolPending, runtimeui.ToolRunning, runtimeui.ToolCompleted, runtimeui.ToolFailed, runtimeui.ToolInterrupted:
	default:
		return ""
	}
	name := oneLine(activity.Name)
	subject := oneLine(activity.Subject)
	if name == "" || subject == "" {
		return ""
	}
	return toolLabelStyle.Render("Tool") + " · " + name + " · " + subject + " · " + toolStatusStyle.Render(status)
}

func oneLine(value string) string { return strings.Join(strings.Fields(textsafe.Display(value)), " ") }

func messagesEqual(left, right []runtimeui.Message) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].ID != right[i].ID || left[i].Role != right[i].Role || left[i].Content != right[i].Content || left[i].Status != right[i].Status || len(left[i].Tools) != len(right[i].Tools) {
			return false
		}
		for j := range left[i].Tools {
			if left[i].Tools[j] != right[i].Tools[j] {
				return false
			}
		}
	}
	return true
}

func cloneRuntimeMessages(source []runtimeui.Message) []runtimeui.Message {
	result := make([]runtimeui.Message, len(source))
	for i := range source {
		result[i] = source[i]
		result[i].Tools = append([]runtimeui.ToolActivity(nil), source[i].Tools...)
	}
	return result
}

func snapshotIDs(messages []runtimeui.Message) (map[string]bool, map[string]bool) {
	messageIDs, toolIDs := make(map[string]bool), make(map[string]bool)
	for _, message := range messages {
		if message.ID != "" {
			messageIDs[message.ID] = true
		}
		for _, activity := range message.Tools {
			if activity.ID != "" {
				toolIDs[activity.ID] = true
			}
		}
	}
	return messageIDs, toolIDs
}

// conversationTitle is the display-safe header label. Titles never reach the
// terminal window title, logs, or diagnostics.
func (m *Model) conversationTitle() string {
	title := oneLine(m.current.Title)
	if title == "" {
		return "Conversation"
	}
	return title
}

// lineWidth is the bound for single-line chrome; an unknown terminal size
// leaves lines unclipped rather than collapsing them to an ellipsis.
func (m *Model) lineWidth() int {
	if m.terminalWidth < 1 {
		return unknownWidth
	}
	return m.terminalWidth
}

const unknownWidth = 1 << 20

func (m *Model) headerLine() string {
	title := m.conversationTitle()
	modelText := m.selected.displayName + " (" + string(m.selected.selection.ModelID) + ") · " + m.selected.effort
	candidates := []string{
		"eino-tui · " + title + " · Codex subscription · " + modelText,
		"eino-tui · " + title + " · " + string(m.selected.selection.ModelID) + " · " + m.selected.effort,
		title + " · " + string(m.selected.selection.ModelID) + " · " + m.selected.effort,
		title,
	}
	for _, candidate := range candidates {
		if m.terminalWidth <= 0 || ansi.StringWidth(candidate) <= m.terminalWidth {
			return candidate
		}
	}
	return candidates[len(candidates)-1]
}

func (m *Model) View() tea.View {
	width := m.lineWidth()
	header := headerStyle.Render(boundedLine(m.headerLine(), width))
	var content string
	switch {
	case m.startup != startupReady && m.conv.mode != convReconcile:
		status := noticeStartupLoading
		if m.startup == startupFailed {
			status = noticeStartupFailed
		}
		if m.terminalHeight <= 1 {
			content = boundedLine(status, width)
		} else {
			content = header + "\n" + boundedLine(status, width)
		}
	case m.conv.mode != convClosed:
		if m.terminalHeight <= 1 {
			content = m.conversationView(m.terminalWidth, 1)
		} else {
			content = header + "\n" + m.conversationView(m.terminalWidth, m.terminalHeight-1)
		}
	case m.picker.mode != pickerClosed:
		if m.terminalHeight <= 1 {
			content = m.pickerView(m.terminalWidth, 1)
		} else {
			content = header + "\n" + m.pickerView(m.terminalWidth, m.terminalHeight-1)
		}
	default:
		notice := m.snapshot.Notice
		if notice == "" {
			notice = phaseText(m.snapshot.Phase)
		}
		footer := pickerHint(width,
			"Enter send · Alt+Enter newline · Alt+S conversations · Alt+N new · Alt+R rename · Alt+M models · Esc interrupt · Ctrl+C quit",
			"Enter send · Alt+S conversations · Alt+N new · Alt+R rename · Alt+M models · Ctrl+C quit",
			"Alt+S conversations · Alt+M models · Ctrl+C quit",
			"Alt+S · Alt+M · Ctrl+C",
		)
		content = header + "\n" + boundedLine(notice, width) + "\n" + m.viewport.View() + "\n" + m.textarea.View() + "\n" + boundedLine(footer, width)
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
