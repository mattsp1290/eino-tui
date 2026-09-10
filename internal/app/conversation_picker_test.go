package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
)

func altKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r), Mod: tea.ModAlt}
}
func ctrlKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r), Mod: tea.ModCtrl}
}
func plainKey(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }
func escKey() tea.KeyPressMsg         { return tea.KeyPressMsg{Code: tea.KeyEscape} }
func enterKey() tea.KeyPressMsg       { return tea.KeyPressMsg{Code: tea.KeyEnter} }

func conversationInfo(id session.ID, generation, number uint64, title string) runtimeui.ConversationInfo {
	return runtimeui.ConversationInfo{ID: id, Generation: generation, Number: number, Title: title}
}

func twoConversations() runtimeui.ConversationPage {
	created := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	return runtimeui.ConversationPage{Items: []runtimeui.ConversationSummary{
		{ID: "conv-2", Number: 2, Title: "Conversation 2 — second", CreatedAt: created.Add(time.Minute)},
		{ID: "conv-1", Number: 1, Title: "Conversation 1 — first", CreatedAt: created, Selected: true},
	}}
}

// readyConversationModel returns a resolved model selected on conv-1.
func readyConversationModel(service *fakeService) *Model {
	if service.load.Conversation.ID == "" {
		service.load = runtimeui.Snapshot{Phase: runtimeui.PhaseIdle, Conversation: conversationInfo("conv-1", 1, 1, "Conversation 1 — first")}
	}
	model := New(context.Background(), service, testDisplayConfig())
	model.Update(model.Init()())
	model.resize(100, 20)
	return model
}

func TestStartupHidesSubmissionUntilConversationLoads(t *testing.T) {
	service := &fakeService{load: runtimeui.Snapshot{Phase: runtimeui.PhaseIdle, Conversation: conversationInfo("conv-1", 1, 1, "Conversation 1")}}
	model := New(context.Background(), service, testDisplayConfig())
	model.resize(80, 12)
	if view := model.View().Content; !strings.Contains(view, noticeStartupLoading) {
		t.Fatalf("startup view = %q", view)
	}
	model.textarea.SetValue("early")
	if _, cmd := model.Update(enterKey()); cmd != nil || model.snapshot.Phase != runtimeui.PhaseIdle {
		t.Fatal("prompt submitted before the conversation loaded")
	}
	model.Update(altKey('s'))
	if model.conv.mode != convClosed {
		t.Fatal("picker opened before startup")
	}
	model.Update(model.Init()())
	if model.startup != startupReady || model.current.ID != "conv-1" || !strings.Contains(model.View().Content, "Conversation 1") {
		t.Fatalf("startup=%v current=%#v", model.startup, model.current)
	}
}

func TestStartupFailureOffersRetryAndReconcileBlocksMutations(t *testing.T) {
	service := &fakeService{loadErr: errors.New("TOKEN /tmp/private")}
	model := New(context.Background(), service, testDisplayConfig())
	model.resize(80, 12)
	model.Update(model.Init()())
	if model.startup != startupFailed || !strings.Contains(model.View().Content, noticeStartupFailed) || strings.Contains(model.View().Content, "TOKEN") {
		t.Fatalf("startup=%v view=%q", model.startup, model.View().Content)
	}
	if _, cmd := model.Update(enterKey()); cmd == nil {
		t.Fatal("enter did not retry")
	}
	if _, cmd := model.Update(plainKey('r')); cmd != nil {
		t.Fatal("retry duplicated while a load is in flight")
	}
	service.loadErr = runtimeui.ErrReconciliationRequired
	_, retry := model.Update(plainKey('r'))
	model.loading = false
	_, retry = model.Update(plainKey('r'))
	model.Update(retry())
	if model.startup != startupReconcile || model.conv.mode != convReconcile || !strings.Contains(model.View().Content, noticeReconcileRequired) {
		t.Fatalf("reconcile startup=%v mode=%v", model.startup, model.conv.mode)
	}
	model.textarea.SetValue("blocked")
	service.loadErr = nil
	service.load = runtimeui.Snapshot{Phase: runtimeui.PhaseIdle, Conversation: conversationInfo("conv-1", 2, 1, "Conversation 1")}
	// Enter in reconcile mode retries the load; it never submits the draft.
	_, load := model.Update(enterKey())
	if load == nil || service.startedPrompt != "" || model.snapshot.Phase != runtimeui.PhaseIdle {
		t.Fatal("submission during reconciliation")
	}
	model.Update(load())
	if model.startup != startupReady || model.conv.mode != convClosed || model.current.Generation != 2 {
		t.Fatalf("after reconcile startup=%v mode=%v current=%#v", model.startup, model.conv.mode, model.current)
	}
}

func TestConversationPickerListsSelectsAndSwapsDrafts(t *testing.T) {
	service := &fakeService{page: twoConversations()}
	model := readyConversationModel(service)
	model.textarea.SetValue("draft for one")
	_, cmd := model.Update(altKey('s'))
	if cmd == nil || model.conv.mode != convLoading || !strings.Contains(model.View().Content, "Loading conversations") {
		t.Fatalf("open mode=%v", model.conv.mode)
	}
	model.Update(cmd())
	if model.conv.mode != convChoosing || model.conv.highlightedID != "conv-1" || model.conv.highlighted != 1 {
		t.Fatalf("choosing mode=%v highlighted=%d id=%s", model.conv.mode, model.conv.highlighted, model.conv.highlightedID)
	}
	view := ansi.Strip(model.View().Content)
	if !strings.Contains(view, "Conversation 2 — second") || !strings.Contains(view, "> *Conversation 1 — first") || !strings.Contains(view, "Enter open") {
		t.Fatalf("picker view = %q", view)
	}
	if _, cmd := model.Update(altKey('m')); cmd != nil || model.picker.mode != pickerClosed {
		t.Fatal("model picker opened while the conversation picker owns focus")
	}
	model.Update(plainKey('k'))
	if model.conv.highlightedID != "conv-2" {
		t.Fatalf("highlight = %s", model.conv.highlightedID)
	}
	service.selected = runtimeui.SelectionResult{Snapshot: runtimeui.Snapshot{
		Phase: runtimeui.PhaseIdle, Conversation: conversationInfo("conv-2", 2, 2, "Conversation 2 — second"),
		Messages: []runtimeui.Message{{ID: "m", Role: runtimeui.RoleUser, Content: "second history"}},
	}}
	_, selectCmd := model.Update(enterKey())
	if selectCmd == nil || model.conv.mode != convSelecting {
		t.Fatalf("select mode=%v", model.conv.mode)
	}
	if model.textarea.Value() != "draft for one" {
		t.Fatal("draft changed before selection committed")
	}
	model.Update(selectCmd())
	if model.conv.mode != convClosed || model.current.ID != "conv-2" || model.current.Generation != 2 {
		t.Fatalf("after select mode=%v current=%#v", model.conv.mode, model.current)
	}
	if model.textarea.Value() != "" || model.drafts["conv-1"] != "draft for one" || len(service.selectedIDs) != 1 || service.selectedIDs[0] != "conv-2" {
		t.Fatalf("drafts=%#v editor=%q selected=%v", model.drafts, model.textarea.Value(), service.selectedIDs)
	}
	view = ansi.Strip(model.View().Content)
	if !strings.Contains(view, "Conversation 2 — second") || !strings.Contains(view, "second history") {
		t.Fatalf("destination not rendered: %q", view)
	}
	// Switching back restores the retained draft exactly once.
	model.textarea.SetValue("draft for two")
	service.selected = runtimeui.SelectionResult{Snapshot: runtimeui.Snapshot{Phase: runtimeui.PhaseIdle, Conversation: conversationInfo("conv-1", 3, 1, "Conversation 1 — first")}}
	_, open := model.Update(altKey('s'))
	model.Update(open())
	model.Update(plainKey('j'))
	_, back := model.Update(enterKey())
	model.Update(back())
	if model.textarea.Value() != "draft for one" || model.drafts["conv-2"] != "draft for two" || len(model.drafts) != 1 {
		t.Fatalf("draft restore editor=%q drafts=%#v", model.textarea.Value(), model.drafts)
	}
}

func TestConversationPickerBusyHintsAndStaleResults(t *testing.T) {
	service := &fakeService{page: twoConversations()}
	model := readyConversationModel(service)
	for _, phase := range []runtimeui.Phase{runtimeui.PhaseStarting, runtimeui.PhaseRunning, runtimeui.PhaseRecoveryWaiting, runtimeui.PhaseRecovering} {
		model.snapshot.Phase = phase
		for _, key := range []tea.KeyPressMsg{altKey('s'), altKey('n'), altKey('r')} {
			model.snapshot.Notice = ""
			if _, cmd := model.Update(key); cmd != nil || model.conv.mode != convClosed || model.snapshot.Notice != busyConversationHint {
				t.Fatalf("phase %s key %s opened dialog or lost hint", phase, key.Keystroke())
			}
		}
	}
	model.snapshot.Phase = runtimeui.PhaseIdle
	_, first := model.Update(altKey('s'))
	staleOp := model.conv.op
	model.Update(escKey())
	if model.conv.mode != convClosed {
		t.Fatal("escape did not close the loading picker")
	}
	_, second := model.Update(altKey('s'))
	model.Update(first())
	if model.conv.mode != convLoading || model.conv.op == staleOp {
		t.Fatal("stale directory result replaced the active read")
	}
	model.Update(second())
	if model.conv.mode != convChoosing {
		t.Fatalf("fresh result mode=%v", model.conv.mode)
	}
	// A delayed selection result from a previous generation is ignored.
	model.Update(conversationSelectedMsg{op: model.conv.op, generation: 99, id: "conv-2", result: runtimeui.SelectionResult{Snapshot: runtimeui.Snapshot{Conversation: conversationInfo("conv-2", 100, 2, "x")}}})
	if model.current.ID != "conv-1" {
		t.Fatal("stale generation result applied")
	}
	// A late run snapshot for the old conversation cannot touch the new one.
	run := newFakeRun("old-run")
	model.pending = nil
	model.Update(snapshotMsg{run: run, snapshot: runtimeui.Snapshot{RunID: "old-run", Version: 5, LiveMessages: []runtimeui.Message{{Content: "late"}}}, ok: true})
	if len(model.snapshot.LiveMessages) != 0 {
		t.Fatal("late snapshot applied without a pending run")
	}
}

func TestConversationPickerPagesAndRefreshes(t *testing.T) {
	first := twoConversations()
	first.NextCursor = "cursor-2"
	service := &fakeService{page: first}
	model := readyConversationModel(service)
	_, open := model.Update(altKey('s'))
	model.Update(open())
	if !strings.Contains(ansi.Strip(model.View().Content), "page 1 →") {
		t.Fatalf("page indicator missing: %q", model.View().Content)
	}
	service.page = runtimeui.ConversationPage{Items: []runtimeui.ConversationSummary{{ID: "conv-0", Number: 0, Title: "(unavailable)", Unavailable: true}}}
	_, next := model.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	model.Update(next())
	if len(model.conv.cursors) != 2 || model.conv.cursors[1] != "cursor-2" || service.listCursors[len(service.listCursors)-1] != "cursor-2" {
		t.Fatalf("cursors=%v requested=%v", model.conv.cursors, service.listCursors)
	}
	if _, cmd := model.Update(enterKey()); cmd != nil || model.conv.err != noticeConversationUnavail {
		t.Fatal("unavailable record was selectable")
	}
	service.page = first
	_, prev := model.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	model.Update(prev())
	if len(model.conv.cursors) != 1 || service.listCursors[len(service.listCursors)-1] != "" {
		t.Fatalf("previous page cursors=%v requested=%v", model.conv.cursors, service.listCursors)
	}
	service.listErr = runtimeui.ErrDirectoryCursor
	_, refresh := model.Update(ctrlKey('r'))
	model.Update(refresh())
	if model.conv.mode != convChoosing || model.conv.err != noticeDirectoryExpired || service.listCursors[len(service.listCursors)-1] != "" {
		t.Fatalf("expired cursor mode=%v err=%q", model.conv.mode, model.conv.err)
	}
	service.listErr = errors.New("TOKEN /tmp/private")
	_, again := model.Update(ctrlKey('r'))
	model.Update(again())
	if model.conv.err != noticeDirectoryUnavailable || strings.Contains(model.View().Content, "TOKEN") {
		t.Fatalf("directory failure err=%q view=%q", model.conv.err, model.View().Content)
	}
}

func TestCreateConversationFlowsAndPartialFailure(t *testing.T) {
	service := &fakeService{created: runtimeui.SelectionResult{Snapshot: runtimeui.Snapshot{Phase: runtimeui.PhaseIdle, Conversation: conversationInfo("conv-3", 2, 3, "Conversation 3")}, DurabilityWarning: true}}
	model := readyConversationModel(service)
	model.textarea.SetValue("keep me")
	_, create := model.Update(altKey('n'))
	if create == nil || model.conv.mode != convCreating || !strings.Contains(model.View().Content, "Creating a new conversation") {
		t.Fatalf("create mode=%v", model.conv.mode)
	}
	model.Update(escKey())
	if model.conv.mode != convCreating {
		t.Fatal("escape closed a committed mutation before its result")
	}
	model.Update(create())
	if model.conv.mode != convClosed || model.current.ID != "conv-3" || model.textarea.Value() != "" || model.drafts["conv-1"] != "keep me" {
		t.Fatalf("create result current=%#v editor=%q drafts=%#v", model.current, model.textarea.Value(), model.drafts)
	}
	if model.snapshot.Notice != runtimeui.NoticeSelectionUnconfirmed {
		t.Fatalf("durability warning notice=%q", model.snapshot.Notice)
	}
	// Partial failure: the session exists but the selection was not saved.
	service.createErr = runtimeui.ErrPreferenceFailed
	service.created = runtimeui.SelectionResult{CommittedID: "conv-4"}
	_, partial := model.Update(altKey('n'))
	model.Update(partial())
	if model.conv.mode != convRetrySelect || model.conv.pendingID != "conv-4" || !strings.Contains(model.View().Content, noticeCreatedNotSelected) {
		t.Fatalf("partial mode=%v pending=%s", model.conv.mode, model.conv.pendingID)
	}
	service.selected = runtimeui.SelectionResult{Snapshot: runtimeui.Snapshot{Phase: runtimeui.PhaseIdle, Conversation: conversationInfo("conv-4", 3, 4, "Conversation 4")}}
	_, retry := model.Update(enterKey())
	model.Update(retry())
	if service.creates != 2 || len(service.selectedIDs) != 1 || service.selectedIDs[0] != "conv-4" || model.current.ID != "conv-4" {
		t.Fatalf("retry created again or did not select: creates=%d selected=%v", service.creates, service.selectedIDs)
	}
	// Reconciliation-required blocks submission until a load succeeds.
	service.createErr = runtimeui.ErrReconciliationRequired
	_, blocked := model.Update(altKey('n'))
	model.Update(blocked())
	if model.conv.mode != convReconcile {
		t.Fatalf("reconcile mode=%v", model.conv.mode)
	}
	model.textarea.SetValue("no send")
	service.load = runtimeui.Snapshot{Phase: runtimeui.PhaseIdle, Conversation: conversationInfo("conv-4", 4, 4, "Conversation 4")}
	_, reload := model.Update(enterKey())
	if service.startedPrompt != "" || reload == nil {
		t.Fatal("prompt submitted during reconciliation or retry missing")
	}
	model.Update(reload())
	if model.conv.mode != convClosed || model.current.Generation != 4 {
		t.Fatalf("after reconcile mode=%v current=%#v", model.conv.mode, model.current)
	}
}

func TestDraftBudgetRefusesSwitchBeforeMutation(t *testing.T) {
	service := &fakeService{page: twoConversations()}
	model := readyConversationModel(service)
	model.drafts["other"] = strings.Repeat("x", MaxDraftBytes-3)
	model.recountDrafts()
	model.textarea.SetValue("four")
	if _, cmd := model.Update(altKey('n')); cmd != nil || service.creates != 0 || model.snapshot.Notice != noticeDraftBudget {
		t.Fatalf("budget breach started a mutation: creates=%d notice=%q", service.creates, model.snapshot.Notice)
	}
	if model.textarea.Value() != "four" {
		t.Fatal("refused switch changed the draft")
	}
	model.textarea.SetValue("abc")
	if _, cmd := model.Update(altKey('n')); cmd == nil {
		t.Fatal("draft within budget was refused")
	}
}

func TestConversationCancellationAndFailuresPreserveSource(t *testing.T) {
	service := &fakeService{page: twoConversations(), listBlock: make(chan struct{})}
	model := readyConversationModel(service)
	_, open := model.Update(altKey('s'))
	completion := make(chan tea.Msg, 1)
	go func() { completion <- open() }()
	time.Sleep(20 * time.Millisecond)
	model.Update(escKey())
	select {
	case msg := <-completion:
		model.Update(msg)
	case <-time.After(time.Second):
		t.Fatal("canceled directory read did not return")
	}
	if model.conv.mode != convClosed || model.snapshot.Notice != "" {
		t.Fatalf("canceled read mode=%v notice=%q", model.conv.mode, model.snapshot.Notice)
	}
	service.listBlock = nil
	model.textarea.SetValue("source draft")
	for _, failure := range []struct {
		err    error
		notice string
	}{
		{err: runtimeui.ErrConversationUnavailable, notice: noticeConversationUnavail},
		{err: runtimeui.ErrPreferenceFailed, notice: noticeSelectionUnsaved},
		{err: runtimeui.ErrPreferenceInvalid, notice: noticePreferencesInvalid},
		{err: errors.New("TOKEN /tmp/private"), notice: noticeConversationUnavail},
	} {
		service.selectErr = failure.err
		_, open := model.Update(altKey('s'))
		model.Update(open())
		model.Update(plainKey('k'))
		_, sel := model.Update(enterKey())
		model.Update(sel())
		if model.conv.mode != convClosed || model.current.ID != "conv-1" || model.textarea.Value() != "source draft" || model.snapshot.Notice != failure.notice {
			t.Fatalf("failure %v: mode=%v current=%s draft=%q notice=%q", failure.err, model.conv.mode, model.current.ID, model.textarea.Value(), model.snapshot.Notice)
		}
		if strings.Contains(model.View().Content, "TOKEN") {
			t.Fatal("raw error leaked")
		}
	}
	service.selectErr = context.Canceled
	_, open = model.Update(altKey('s'))
	model.Update(open())
	model.Update(plainKey('k'))
	_, sel := model.Update(enterKey())
	model.snapshot.Notice = ""
	model.Update(sel())
	if model.conv.mode != convClosed || model.snapshot.Notice != "" || model.current.ID != "conv-1" {
		t.Fatalf("canceled selection mode=%v notice=%q", model.conv.mode, model.snapshot.Notice)
	}
}

func TestSelectedRecoveryWaitingDestinationSchedulesRecovery(t *testing.T) {
	service := &fakeService{page: twoConversations()}
	service.selected = runtimeui.SelectionResult{Snapshot: runtimeui.Snapshot{
		Phase: runtimeui.PhaseRecoveryWaiting, Notice: runtimeui.NoticeRecoveryWaiting, RecoveryAt: time.Now().Add(time.Minute),
		Conversation: conversationInfo("conv-2", 2, 2, "Conversation 2 — second"),
	}}
	model := readyConversationModel(service)
	_, open := model.Update(altKey('s'))
	model.Update(open())
	model.Update(plainKey('k'))
	_, sel := model.Update(enterKey())
	_, timer := model.Update(sel())
	if timer == nil || model.snapshot.Phase != runtimeui.PhaseRecoveryWaiting || model.current.ID != "conv-2" {
		t.Fatalf("recovery destination phase=%v current=%s timer=%v", model.snapshot.Phase, model.current.ID, timer != nil)
	}
	if _, cmd := model.Update(altKey('s')); cmd != nil || model.snapshot.Notice != busyConversationHint {
		t.Fatal("switching allowed while the destination recovers")
	}
}

func TestConversationViewsStayBoundedAtSmallSizes(t *testing.T) {
	model := readyConversationModel(&fakeService{})
	model.conv.page = twoConversations()
	model.conv.page.Items[0].Title = strings.Repeat("界", 300) + "\x1b]0;SECRET\a"
	for height := 1; height <= 6; height++ {
		model.conv.mode = convClosed
		model.resize(60, height)
		if lines := strings.Count(model.View().Content, "\n") + 1; lines > height {
			t.Fatalf("chat view height=%d rendered %d lines: %q", height, lines, model.View().Content)
		}
	}
	for _, mode := range []conversationMode{convLoading, convChoosing, convCreating, convSelecting, convSaving, convRetrySelect, convReconcile, convRenaming} {
		model.conv.mode = mode
		model.conv.input.SetValue("title")
		for _, width := range []int{1, 8, 18, 40, 120} {
			for _, height := range []int{1, 2, 3, 5, 8} {
				view := model.conversationView(width, height)
				lines := strings.Split(view, "\n")
				if len(lines) > height {
					t.Fatalf("mode=%d width=%d height=%d lines=%d", mode, width, height, len(lines))
				}
				for _, line := range lines {
					if ansi.StringWidth(line) > width {
						t.Fatalf("mode=%d width=%d overlong line=%q", mode, width, line)
					}
				}
				if strings.Contains(view, "SECRET") || strings.Contains(view, "\x1b]0;") {
					t.Fatalf("mode=%d leaked unsafe title: %q", mode, view)
				}
				if mode != convRenaming && mode != convReconcile && width >= 40 && !strings.Contains(view, "Esc") {
					t.Fatalf("mode=%d width=%d height=%d lost escape control: %q", mode, width, height, view)
				}
			}
		}
	}
	model.conv.mode = convChoosing
	model.resize(120, 1)
	if lines := strings.Count(model.View().Content, "\n") + 1; lines > 1 {
		t.Fatalf("one-row terminal rendered %d lines", lines)
	}
}

func TestHeaderPrioritizesConversationTitleWhenNarrow(t *testing.T) {
	model := readyConversationModel(&fakeService{load: runtimeui.Snapshot{Phase: runtimeui.PhaseIdle, Conversation: conversationInfo("conv-1", 1, 1, "Conversation 1 — plan\tthe\x1b[31m release")}})
	model.resize(120, 20)
	wide := ansi.Strip(model.View().Content)
	if !strings.Contains(wide, "eino-tui · Conversation 1 — plan the release · Codex subscription · gpt-5.5 (gpt-5.5) · medium") {
		t.Fatalf("wide header = %q", wide)
	}
	model.resize(58, 20)
	narrow := ansi.Strip(model.View().Content)
	if !strings.HasPrefix(narrow, "Conversation 1 — plan the release · gpt-5.5 · medium\n") {
		t.Fatalf("narrow header = %q", narrow)
	}
	model.resize(20, 20)
	if tiny := ansi.Strip(model.View().Content); !strings.HasPrefix(tiny, "Conversation 1 — pl…\n") {
		t.Fatalf("tiny header = %q", tiny)
	}
	if view := model.View(); view.WindowTitle != "" {
		t.Fatal("title reached the window title")
	}
}
