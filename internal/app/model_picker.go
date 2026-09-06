package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	agentmodel "github.com/mattsp1290/eino-agent/model"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
)

type pickerMode uint8

const (
	pickerClosed pickerMode = iota
	pickerLoading
	pickerReady
	pickerEmpty
	pickerFailed
)

type selectedModel struct {
	selection   agentmodel.Selection
	displayName string
	effort      string
}

type pickerState struct {
	mode              pickerMode
	cache             []codexmodel.CatalogEntry
	cacheValid        bool
	highlightedModel  int
	highlightedEffort int
	generation        uint64
	cancel            context.CancelFunc
}

func (m *Model) openPicker() tea.Cmd {
	m.textarea.Blur()
	if m.picker.cacheValid {
		m.reconcilePicker()
		if len(m.picker.cache) == 0 {
			m.picker.mode = pickerEmpty
		} else {
			m.picker.mode = pickerReady
		}
		return nil
	}
	return m.beginCatalogRequest()
}

func (m *Model) beginCatalogRequest() tea.Cmd {
	m.cancelCatalogRequest()
	m.picker.cache = nil
	m.picker.cacheValid = false
	m.picker.generation++
	generation := m.picker.generation
	ctx, cancel := context.WithCancel(m.ctx)
	m.picker.cancel = cancel
	m.picker.mode = pickerLoading
	catalog := m.catalog
	return func() tea.Msg {
		if catalog == nil {
			return catalogLoadedMsg{generation: generation, err: errors.New("catalog unavailable")}
		}
		entries, err := catalog.ListModels(ctx)
		return catalogLoadedMsg{generation: generation, entries: entries, err: err}
	}
}

func (m *Model) cancelCatalogRequest() {
	if m.picker.cancel != nil {
		m.picker.cancel()
		m.picker.cancel = nil
	}
}

func (m *Model) closePicker() {
	m.cancelCatalogRequest()
	m.picker.generation++
	m.picker.mode = pickerClosed
	m.textarea.Focus()
}

func (m *Model) closePickerIfBusy(phase runtimeui.Phase) {
	if phase != runtimeui.PhaseIdle && m.picker.mode != pickerClosed {
		m.closePicker()
	}
}

func (m *Model) applyCatalogResult(msg catalogLoadedMsg) {
	if m.picker.mode != pickerLoading || msg.generation != m.picker.generation {
		return
	}
	if cancel := m.picker.cancel; cancel != nil {
		cancel()
		m.picker.cancel = nil
	}
	if msg.err != nil {
		m.picker.cache = nil
		m.picker.cacheValid = false
		m.picker.mode = pickerFailed
		return
	}
	m.picker.cache = append([]codexmodel.CatalogEntry(nil), msg.entries...)
	m.picker.cacheValid = true
	m.reconcilePicker()
	if len(m.picker.cache) == 0 {
		m.picker.mode = pickerEmpty
		return
	}
	m.picker.mode = pickerReady
}

func (m *Model) reconcilePicker() {
	m.picker.highlightedModel = 0
	for i, entry := range m.picker.cache {
		if entry.ModelID == m.selected.selection.ModelID {
			m.picker.highlightedModel = i
			break
		}
	}
	m.reconcileEffort()
}

func (m *Model) reconcileEffort() {
	m.picker.highlightedEffort = 0
	if len(m.picker.cache) == 0 {
		return
	}
	entry := m.picker.cache[m.picker.highlightedModel]
	target := entry.DefaultEffort
	for _, effort := range entry.SupportedEfforts {
		if effort.ID == m.selected.effort {
			target = m.selected.effort
			break
		}
	}
	for i, effort := range entry.SupportedEfforts {
		if effort.ID == target {
			m.picker.highlightedEffort = i
			return
		}
	}
}

func (m *Model) handlePickerKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Keystroke() {
	case "ctrl+c":
		m.cancelCatalogRequest()
		return m, func() tea.Msg { return tea.Quit() }
	case "esc":
		m.closePicker()
		return m, nil
	case "r":
		if m.picker.mode != pickerLoading {
			return m, m.beginCatalogRequest()
		}
		return m, nil
	}
	if m.picker.mode != pickerReady || len(m.picker.cache) == 0 {
		return m, nil
	}
	switch msg.Keystroke() {
	case "up", "k":
		if m.picker.highlightedModel > 0 {
			m.picker.highlightedModel--
			m.reconcileEffort()
		}
	case "down", "j":
		if m.picker.highlightedModel+1 < len(m.picker.cache) {
			m.picker.highlightedModel++
			m.reconcileEffort()
		}
	case "tab":
		m.cycleEffort(1)
	case "shift+tab":
		m.cycleEffort(-1)
	case "enter":
		entry := m.picker.cache[m.picker.highlightedModel]
		effort := entry.SupportedEfforts[m.picker.highlightedEffort].ID
		m.selected = selectedModel{
			selection:   agentmodel.Selection{ProviderID: codexmodel.ProviderID, ModelID: entry.ModelID},
			displayName: entry.DisplayName, effort: effort,
		}
		m.closePicker()
	}
	return m, nil
}

func (m *Model) cycleEffort(delta int) {
	efforts := m.picker.cache[m.picker.highlightedModel].SupportedEfforts
	if len(efforts) == 0 {
		return
	}
	m.picker.highlightedEffort = (m.picker.highlightedEffort + delta + len(efforts)) % len(efforts)
}

func (m *Model) pickerView(width, height int) string {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	var lines []string
	switch m.picker.mode {
	case pickerLoading:
		lines = compactPickerState(height, "Loading account model catalog…", "Esc close")
	case pickerFailed:
		lines = compactPickerState(height, "Model catalog unavailable.", "R retry · Esc close")
	case pickerEmpty:
		lines = compactPickerState(height, "No compatible Codex models are available.", "R refresh · Esc close")
	case pickerReady:
		selectedEntry := m.picker.cache[m.picker.highlightedModel]
		selectedEffort := selectedEntry.SupportedEfforts[m.picker.highlightedEffort]
		hint := "↑/↓ models · Tab effort · Enter apply · R refresh · Esc close"
		if width < 60 {
			hint = "Enter apply · Esc close"
		}
		selectedLine := m.pickerModelLine(m.picker.highlightedModel)
		if height == 1 {
			if width >= 48 {
				lines = []string{"Enter apply · Esc close · " + selectedLine}
			} else {
				lines = []string{"Enter apply · Esc close"}
			}
			break
		}
		if height == 2 {
			lines = []string{selectedLine, hint}
			break
		}
		var details []string
		if height >= 5 && selectedEntry.Description != "" {
			details = append(details, "  "+selectedEntry.Description)
		}
		if height >= 6 && selectedEffort.Description != "" {
			details = append(details, "  "+selectedEffort.ID+": "+selectedEffort.Description)
		}
		room := height - 2 - len(details)
		if room < 1 {
			room = 1
		}
		start := m.picker.highlightedModel - room/2
		if start < 0 {
			start = 0
		}
		end := min(len(m.picker.cache), start+room)
		if end-start < room {
			start = max(0, end-room)
		}
		lines = []string{"Model & reasoning"}
		for i := start; i < end; i++ {
			lines = append(lines, m.pickerModelLine(i))
		}
		lines = append(lines, details...)
		lines = append(lines, hint)
	}
	for i := range lines {
		lines[i] = boundedLine(strings.TrimRight(lines[i], " "), width)
	}
	return strings.Join(lines, "\n")
}

func compactPickerState(height int, state, hint string) []string {
	if height <= 1 {
		return []string{state + " · " + hint}
	}
	if height == 2 {
		return []string{state, hint}
	}
	return []string{"Model & reasoning", state, hint}
}

func (m *Model) pickerModelLine(index int) string {
	entry := m.picker.cache[index]
	marker := "  "
	if index == m.picker.highlightedModel {
		marker = "> "
	}
	effort := entry.DefaultEffort
	if index == m.picker.highlightedModel {
		effort = entry.SupportedEfforts[m.picker.highlightedEffort].ID
	}
	applied := ""
	if entry.ModelID == m.selected.selection.ModelID {
		applied = " [applied " + m.selected.effort + "]"
	}
	return fmt.Sprintf("%s%s · [%s] · %s%s", marker, entry.ModelID, effort, entry.DisplayName, applied)
}
