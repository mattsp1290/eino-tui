package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	agentmodel "github.com/mattsp1290/eino-agent/model"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
	"github.com/mattsp1290/eino-tui/internal/textsafe"
)

// MaxDraftBytes bounds the total unsent draft text kept for non-selected
// conversations. Each draft is additionally bounded by textsafe.MaxPromptBytes.
const MaxDraftBytes = 2 << 20

type startupMode uint8

const (
	startupLoading startupMode = iota
	startupReady
	startupFailed
	startupReconcile
)

type Model struct {
	ctx              context.Context
	service          runtimeui.Service
	textarea         textarea.Model
	viewport         viewport.Model
	snapshot         runtimeui.Snapshot
	pending          runtimeui.Run
	lastVersion      uint64
	stableWidth      int
	stableMessages   []runtimeui.Message
	stableTranscript string
	selected         selectedModel
	catalog          codexmodel.Catalog
	picker           pickerState
	terminalWidth    int
	terminalHeight   int

	startup  startupMode
	loading  bool
	current  runtimeui.ConversationInfo
	conv     conversationState
	drafts   map[session.ID]string
	draftUse int
}

type Config struct {
	Catalog                codexmodel.Catalog
	InitialSelection       agentmodel.Selection
	InitialReasoningEffort string
}

func New(ctx context.Context, service runtimeui.Service, cfg Config) *Model {
	selection := cfg.InitialSelection
	if codexmodel.ValidateSelection(selection) != nil {
		selection = agentmodel.Selection{ProviderID: codexmodel.ProviderID, ModelID: agentmodel.ID(codexmodel.DefaultModel)}
	}
	effort := cfg.InitialReasoningEffort
	if !codexmodel.ValidReasoningEffort(effort) {
		effort = codexmodel.ReasoningEffortMedium
	}
	input := textarea.New()
	input.Placeholder = "Type a message…"
	input.Prompt = "> "
	input.ShowLineNumbers = false
	input.CharLimit = textsafe.MaxPromptBytes
	input.MaxHeight = 6
	input.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter"), key.WithHelp("alt+enter", "newline"))
	input.Focus()
	view := viewport.New()
	view.SoftWrap = true
	return &Model{
		ctx: ctx, service: service, textarea: input, viewport: view,
		snapshot: runtimeui.Snapshot{Phase: runtimeui.PhaseIdle}, catalog: cfg.Catalog,
		selected: selectedModel{selection: selection, displayName: safeMetadata(string(selection.ModelID), "unknown model", codexmodel.MaxModelDisplayNameBytes), effort: effort},
		picker:   pickerState{mode: pickerClosed},
		startup:  startupLoading,
		conv:     newConversationState(),
		drafts:   make(map[session.ID]string),
	}
}

func safeMetadata(value, fallback string, maxBytes int) string {
	value = textsafe.Display(value)
	if value == "" || len(value) > maxBytes || strings.ContainsAny(value, "\n\t") {
		return fallback
	}
	return value
}

func (m *Model) Init() tea.Cmd { return m.loadCmd() }

func (m *Model) loadCmd() tea.Cmd {
	m.loading = true
	return func() tea.Msg { snapshot, err := m.service.Load(m.ctx); return loadedMsg{snapshot: snapshot, err: err} }
}

func (m *Model) startCmd(draft string, cfg runtimeui.StartConfig) tea.Cmd {
	return func() tea.Msg {
		result, err := m.service.Start(m.ctx, draft, cfg)
		return startedMsg{draft: draft, result: result, err: err}
	}
}

func nextCmd(ctx context.Context, run runtimeui.Run) tea.Cmd {
	return func() tea.Msg {
		snapshot, ok := run.Next(ctx)
		return snapshotMsg{run: run, snapshot: snapshot, ok: ok}
	}
}

func (m *Model) interruptCmd() tea.Cmd {
	return func() tea.Msg { return interruptedMsg{err: m.service.InterruptActive(m.ctx)} }
}

func (m *Model) recoveryCmd() tea.Cmd {
	return func() tea.Msg { result, err := m.service.Recover(m.ctx); return recoveredMsg{result: result, err: err} }
}

func recoveryTimer(ctx context.Context, at time.Time) tea.Cmd {
	return func() tea.Msg {
		delay := time.Until(at)
		if delay <= 0 {
			return recoveryDueMsg{}
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			return recoveryDueMsg{}
		}
	}
}

// idle reports whether a new turn or conversation mutation may begin.
func (m *Model) idle() bool {
	return m.startup == startupReady && m.snapshot.Phase == runtimeui.PhaseIdle
}

// editing reports whether the prompt editor owns keyboard focus.
func (m *Model) editing() bool {
	return m.idle() && m.picker.mode == pickerClosed && m.conv.mode == convClosed
}

func (m *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case loadedMsg:
		return m.applyLoaded(msg)
	case startedMsg:
		if msg.err != nil {
			m.snapshot.Phase = runtimeui.PhaseIdle
			if !errors.Is(msg.err, context.Canceled) {
				m.snapshot.Notice = "The message could not be started."
			}
			return m, nil
		}
		m.closePickerIfBusy(runtimeui.PhaseStarting)
		if !m.currentGeneration(msg.result.Snapshot.Conversation) {
			return m, nil
		}
		if msg.result.Kind == runtimeui.ActionRecoveryWaiting {
			m.snapshot = msg.result.Snapshot
			return m, recoveryTimer(m.ctx, msg.result.Snapshot.RecoveryAt)
		}
		m.installRun(msg.result)
		// The admitted draft is consumed exactly once, for this conversation.
		m.textarea.SetValue("")
		delete(m.drafts, m.current.ID)
		m.recountDrafts()
		return m, nextCmd(m.ctx, m.pending)
	case snapshotMsg:
		if m.pending == nil || msg.run != m.pending || !msg.ok {
			return m, nil
		}
		if msg.snapshot.RunID == m.pending.ID() && msg.snapshot.Version > m.lastVersion && m.currentGeneration(msg.snapshot.Conversation) {
			m.closePickerIfBusy(msg.snapshot.Phase)
			m.snapshot = msg.snapshot
			m.lastVersion = msg.snapshot.Version
			m.applyConversationInfo(msg.snapshot.Conversation)
			if msg.snapshot.Terminal {
				m.snapshot.Phase = runtimeui.PhaseIdle
				m.pending = nil
			} else {
				m.snapshot.Phase = runtimeui.PhaseRunning
			}
			m.refreshTranscript()
		}
		if m.pending != nil {
			return m, nextCmd(m.ctx, m.pending)
		}
		return m, nil
	case recoveryDueMsg:
		if m.snapshot.Phase == runtimeui.PhaseRecoveryWaiting {
			m.snapshot.Phase = runtimeui.PhaseRecovering
			return m, m.recoveryCmd()
		}
		return m, nil
	case recoveredMsg:
		m.closePickerIfBusy(runtimeui.PhaseRecovering)
		if msg.err != nil {
			m.snapshot.Phase = runtimeui.PhaseRecoveryWaiting
			m.snapshot.Notice = runtimeui.NoticeRecoveryWaiting
			return m, recoveryTimer(m.ctx, time.Now().Add(time.Second))
		}
		if !m.currentGeneration(msg.result.Snapshot.Conversation) {
			return m, nil
		}
		if msg.result.Kind == runtimeui.ActionRecoveryWaiting {
			m.snapshot = msg.result.Snapshot
			return m, recoveryTimer(m.ctx, msg.result.Snapshot.RecoveryAt)
		}
		m.installRun(msg.result)
		return m, nextCmd(m.ctx, m.pending)
	case interruptedMsg:
		if msg.err != nil {
			m.snapshot.Notice = "The active response could not be interrupted."
		}
		return m, nil
	case catalogLoadedMsg:
		m.applyCatalogResult(msg)
		return m, nil
	case directoryLoadedMsg:
		m.applyDirectoryResult(msg)
		return m, nil
	case conversationCreatedMsg:
		return m, m.applyCreatedResult(msg)
	case conversationSelectedMsg:
		return m, m.applySelectedResult(msg)
	case conversationRenamedMsg:
		m.applyRenamedResult(msg)
		return m, nil
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		return m, nil
	case tea.PasteMsg:
		if m.conv.mode == convRenaming {
			m.pasteRename(msg.Content)
			return m, nil
		}
		if !m.editing() {
			return m, nil
		}
		value, err := textsafe.Input(m.textarea.Value() + msg.Content)
		if err == nil {
			m.textarea.SetValue(value)
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	if m.conv.mode == convRenaming {
		updated, cmd := m.conv.input.Update(message)
		m.conv.input = updated
		return m, cmd
	}
	if m.editing() {
		before := m.textarea.Value()
		updated, cmd := m.textarea.Update(message)
		m.textarea = updated
		if normalized, err := textsafe.Input(m.textarea.Value()); err != nil {
			m.textarea.SetValue(before)
		} else if normalized != m.textarea.Value() {
			m.textarea.SetValue(normalized)
		}
		return m, cmd
	}
	updated, cmd := m.viewport.Update(message)
	m.viewport = updated
	return m, cmd
}

func (m *Model) applyLoaded(msg loadedMsg) (tea.Model, tea.Cmd) {
	m.loading = false
	if msg.err != nil {
		switch {
		case errors.Is(msg.err, runtimeui.ErrReconciliationRequired):
			m.startup = startupReconcile
			m.conv.mode = convReconcile
		case m.startup == startupReady:
			m.snapshot.Notice = runtimeui.NoticeUnavailable
		default:
			m.startup = startupFailed
		}
		return m, nil
	}
	m.startup = startupReady
	if m.conv.mode == convReconcile {
		m.closeConversationDialog()
	}
	if id := msg.snapshot.Conversation.ID; id != "" && id != m.current.ID {
		// A reconciled or startup identity change is a switch: both the
		// outgoing and the destination drafts are preserved, never evicted.
		switch {
		case m.current.ID == "":
			m.textarea.SetValue(m.drafts[id])
			delete(m.drafts, id)
			m.recountDrafts()
		case m.draftFits(m.textarea.Value()):
			m.switchDrafts(m.current.ID, id)
		default:
			msg.snapshot.Notice = noticeDraftBudget
		}
		m.installConversation(msg.snapshot.Conversation)
	}
	m.applyLoad(msg.snapshot)
	m.closePickerIfBusy(msg.snapshot.Phase)
	if m.snapshot.Phase == runtimeui.PhaseRecoveryWaiting {
		return m, recoveryTimer(m.ctx, msg.snapshot.RecoveryAt)
	}
	return m, nil
}

// currentGeneration rejects results produced for a previous selection. A zero
// generation is accepted for services that do not report conversations.
func (m *Model) currentGeneration(info runtimeui.ConversationInfo) bool {
	return info.Generation == 0 || info.Generation == m.current.Generation
}

func (m *Model) applyConversationInfo(info runtimeui.ConversationInfo) {
	if info.ID != "" && info.ID == m.current.ID {
		m.current = info
	}
}

// installConversation replaces the selected conversation identity and drops
// every per-run cache that belonged to the previous one.
func (m *Model) installConversation(info runtimeui.ConversationInfo) {
	m.current = info
	m.pending = nil
	m.lastVersion = 0
	m.stableMessages = nil
	m.stableTranscript = ""
	m.stableWidth = 0
}

func (m *Model) applyLoad(snapshot runtimeui.Snapshot) {
	m.snapshot = snapshot
	m.applyConversationInfo(snapshot.Conversation)
	m.refreshTranscript()
}

func (m *Model) installRun(result runtimeui.ActionResult) {
	m.pending = result.Run
	m.snapshot = result.Snapshot
	m.lastVersion = result.Snapshot.Version
	m.snapshot.Phase = runtimeui.PhaseRunning
	m.applyConversationInfo(result.Snapshot.Conversation)
	m.refreshTranscript()
}

func (m *Model) resize(width, height int) {
	if width < 1 {
		width = 1
	}
	if height < 1 {
		height = 1
	}
	m.terminalWidth = width
	m.terminalHeight = height
	contentWidth := width - 2
	if contentWidth < 1 {
		contentWidth = 1
	}
	inputHeight := 3
	if height < 9 {
		inputHeight = 1
	}
	viewportHeight := height - inputHeight - 4
	if viewportHeight < 1 {
		viewportHeight = 1
	}
	m.textarea.SetWidth(contentWidth)
	m.textarea.SetHeight(inputHeight)
	m.viewport.SetWidth(contentWidth)
	m.viewport.SetHeight(viewportHeight)
	m.conv.input.SetWidth(contentWidth)
	m.refreshTranscript()
}

// recountDrafts recomputes the retained draft budget after any change.
func (m *Model) recountDrafts() {
	total := 0
	for _, draft := range m.drafts {
		total += len(draft)
	}
	m.draftUse = total
}

// draftFits reports whether saving the current editor text for the current
// conversation would stay within the total draft budget.
func (m *Model) draftFits(draft string) bool {
	return m.draftUse-len(m.drafts[m.current.ID])+len(draft) <= MaxDraftBytes
}

// switchDrafts saves the outgoing editor text and restores the destination's
// draft. It runs only after a selection succeeded.
func (m *Model) switchDrafts(from, to session.ID) {
	if draft := m.textarea.Value(); draft != "" {
		m.drafts[from] = draft
	} else {
		delete(m.drafts, from)
	}
	m.textarea.SetValue(m.drafts[to])
	delete(m.drafts, to)
	m.recountDrafts()
}
