package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	agentmodel "github.com/mattsp1290/eino-agent/model"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
)

type fakeService struct {
	load          runtimeui.Snapshot
	start         runtimeui.ActionResult
	startErr      error
	startedPrompt string
	startedConfig runtimeui.StartConfig
	interrupts    int
}

func (s *fakeService) Load(context.Context) (runtimeui.Snapshot, error) { return s.load, nil }
func (s *fakeService) Start(_ context.Context, prompt string, cfg runtimeui.StartConfig) (runtimeui.ActionResult, error) {
	s.startedPrompt = prompt
	s.startedConfig = cfg
	return s.start, s.startErr
}
func (s *fakeService) InterruptActive(context.Context) error                   { s.interrupts++; return nil }
func (s *fakeService) Recover(context.Context) (runtimeui.ActionResult, error) { return s.start, nil }
func (s *fakeService) Close(context.Context) error                             { return nil }

type fakeRun struct {
	id        session.RunID
	snapshots []runtimeui.Snapshot
	finished  chan struct{}
}

func newFakeRun(id session.RunID, snapshots ...runtimeui.Snapshot) *fakeRun {
	ch := make(chan struct{})
	close(ch)
	return &fakeRun{id: id, snapshots: snapshots, finished: ch}
}

func testDisplayConfig() Config {
	return Config{
		InitialSelection:       agentmodel.Selection{ProviderID: codexmodel.ProviderID, ModelID: agentmodel.ID(codexmodel.DefaultModel)},
		InitialReasoningEffort: codexmodel.ReasoningEffortMedium,
	}
}
func (r *fakeRun) ID() session.RunID         { return r.id }
func (r *fakeRun) Finished() <-chan struct{} { return r.finished }
func (r *fakeRun) Next(context.Context) (runtimeui.Snapshot, bool) {
	if len(r.snapshots) == 0 {
		return runtimeui.Snapshot{}, false
	}
	s := r.snapshots[0]
	r.snapshots = r.snapshots[1:]
	return s, true
}

func TestModelLoadSubmitAndTerminalReplacement(t *testing.T) {
	run := newFakeRun("run-1", runtimeui.Snapshot{RunID: "run-1", Version: 2, LiveMessages: []runtimeui.Message{{ID: "live", Role: runtimeui.RoleAssistant, Content: "Demo "}}, Phase: runtimeui.PhaseRunning}, runtimeui.Snapshot{RunID: "run-1", Version: 3, Terminal: true, Phase: runtimeui.PhaseIdle, Messages: []runtimeui.Message{{Role: runtimeui.RoleUser, Content: "hello\nworld"}, {Role: runtimeui.RoleAssistant, Content: "Demo complete"}}})
	service := &fakeService{load: runtimeui.Snapshot{Phase: runtimeui.PhaseIdle}, start: runtimeui.ActionResult{Kind: runtimeui.ActionStarted, Run: run, Snapshot: runtimeui.Snapshot{RunID: "run-1", Version: 1, Phase: runtimeui.PhaseRunning, Messages: []runtimeui.Message{{Role: runtimeui.RoleUser, Content: "hello\nworld"}}}}}
	model := New(context.Background(), service, testDisplayConfig())
	_, cmd := model.Update(model.Init()())
	if cmd != nil {
		t.Fatal("unexpected load command")
	}
	model.textarea.SetValue("hello\nworld")
	_, startCmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if startCmd == nil || model.snapshot.Phase != runtimeui.PhaseStarting {
		t.Fatal("submit did not start")
	}
	_, next := model.Update(startCmd())
	if service.startedPrompt != "hello\nworld" || model.textarea.Value() != "" || next == nil {
		t.Fatalf("start state: prompt=%q input=%q", service.startedPrompt, model.textarea.Value())
	}
	_, next = model.Update(next())
	if len(model.snapshot.LiveMessages) != 1 || model.snapshot.LiveMessages[0].Content != "Demo " || next == nil {
		t.Fatalf("live = %#v", model.snapshot)
	}
	_, next = model.Update(next())
	if model.snapshot.Phase != runtimeui.PhaseIdle || model.pending != nil || len(model.snapshot.Messages) != 2 {
		t.Fatalf("terminal = %#v", model.snapshot)
	}
}

func TestKeysPasteAndResize(t *testing.T) {
	service := &fakeService{}
	model := New(context.Background(), service, testDisplayConfig())
	model.Update(tea.WindowSizeMsg{Width: 1, Height: 1})
	if model.viewport.Width() < 1 || model.viewport.Height() < 1 {
		t.Fatal("negative dimensions")
	}
	model.Update(tea.PasteMsg{Content: "one\n\x1b[31mtwo"})
	if got := model.textarea.Value(); got != "one\ntwo" {
		t.Fatalf("paste = %q", got)
	}
	if keys := model.textarea.KeyMap.InsertNewline.Keys(); len(keys) != 1 || keys[0] != "alt+enter" {
		t.Fatalf("newline keys = %#v", keys)
	}
	model.snapshot.Phase = runtimeui.PhaseRunning
	_, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd == nil {
		t.Fatal("escape did not interrupt")
	}
	cmd()
	if service.interrupts != 1 {
		t.Fatalf("interrupts = %d", service.interrupts)
	}
	_, cmd = model.Update(tea.KeyPressMsg{Code: 'd', Text: "d", Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatal("ctrl+d quit while running")
	}
}

func TestViewIsSemanticAtNarrowWidth(t *testing.T) {
	model := New(context.Background(), &fakeService{}, testDisplayConfig())
	model.snapshot = runtimeui.Snapshot{Messages: []runtimeui.Message{{Role: runtimeui.RoleUser, Content: "界é\tשלום"}, {Role: runtimeui.RoleAssistant, Content: strings.Repeat("x", 100)}}}
	model.resize(8, 6)
	view := model.View()
	if !view.AltScreen || !strings.Contains(view.Content, "Codex subscription") {
		t.Fatalf("view = %#v", view)
	}
}

func TestModelRejectsStaleAndPriorRunSnapshots(t *testing.T) {
	model := New(context.Background(), &fakeService{}, testDisplayConfig())
	run := newFakeRun("current")
	model.pending = run
	model.lastVersion = 5
	model.snapshot.Phase = runtimeui.PhaseRunning
	model.snapshot = runtimeui.Snapshot{RunID: "current", Version: 5, LiveMessages: []runtimeui.Message{{Content: "current"}}}
	model.Update(snapshotMsg{run: run, snapshot: runtimeui.Snapshot{RunID: "prior", Version: 99, LiveMessages: []runtimeui.Message{{Content: "wrong"}}}, ok: true})
	model.Update(snapshotMsg{run: run, snapshot: runtimeui.Snapshot{RunID: "current", Version: 5, LiveMessages: []runtimeui.Message{{Content: "also wrong"}}}, ok: true})
	if model.snapshot.LiveMessages[0].Content != "current" {
		t.Fatalf("stale snapshot applied: %#v", model.snapshot)
	}
	model.Update(snapshotMsg{run: run, snapshot: runtimeui.Snapshot{RunID: "current", Version: 6, Resync: true, LiveMessages: []runtimeui.Message{{Content: "new"}}}, ok: true})
	if model.snapshot.LiveMessages[0].Content != "new" || !model.snapshot.Resync {
		t.Fatalf("new snapshot not applied: %#v", model.snapshot)
	}
}

func TestModelAppliesHigherVersionToolOnlySnapshot(t *testing.T) {
	model := New(context.Background(), &fakeService{}, testDisplayConfig())
	run := newFakeRun("current")
	model.pending = run
	model.lastVersion = 1
	model.snapshot = runtimeui.Snapshot{RunID: "current", Version: 1, Phase: runtimeui.PhaseRunning}
	model.resize(60, 12)
	toolOnly := runtimeui.Snapshot{RunID: "current", Version: 2, Phase: runtimeui.PhaseRunning, LiveMessages: []runtimeui.Message{{ID: "request", Role: runtimeui.RoleAssistant, Tools: []runtimeui.ToolActivity{{ID: "call", Name: "file_list", Subject: ".", Status: runtimeui.ToolRunning}}}}}
	model.Update(snapshotMsg{run: run, snapshot: toolOnly, ok: true})
	if model.lastVersion != 2 || len(model.snapshot.LiveMessages) != 1 || !strings.Contains(model.viewport.View(), "file_list") || !strings.Contains(model.viewport.View(), "running") {
		t.Fatalf("tool-only snapshot not applied: %#v", model.snapshot)
	}
}

func TestRecoveryWaitingReplacesDeadlineAndRetainsDraft(t *testing.T) {
	model := New(context.Background(), &fakeService{}, testDisplayConfig())
	model.snapshot.Phase = runtimeui.PhaseRecoveryWaiting
	model.textarea.SetValue("unsent")
	_, recoverCommand := model.Update(recoveryDueMsg{})
	if recoverCommand == nil || model.snapshot.Phase != runtimeui.PhaseRecovering {
		t.Fatal("recovery was not scheduled")
	}
	refreshed := time.Now().Add(time.Minute)
	waiting := runtimeui.ActionResult{Kind: runtimeui.ActionRecoveryWaiting, Snapshot: runtimeui.Snapshot{Phase: runtimeui.PhaseRecoveryWaiting, RecoveryAt: refreshed, Notice: runtimeui.NoticeRecoveryWaiting}}
	_, timer := model.Update(recoveredMsg{result: waiting})
	if timer == nil || !model.snapshot.RecoveryAt.Equal(refreshed) || model.textarea.Value() != "unsent" {
		t.Fatalf("waiting state=%#v draft=%q", model.snapshot, model.textarea.Value())
	}
}

func TestSnapshotPhaseDrivesKeysStatusAndEditing(t *testing.T) {
	tests := []struct {
		phase      runtimeui.Phase
		editable   bool
		interrupts bool
	}{
		{phase: runtimeui.PhaseIdle, editable: true},
		{phase: runtimeui.PhaseStarting, interrupts: true},
		{phase: runtimeui.PhaseRunning, interrupts: true},
		{phase: runtimeui.PhaseRecoveryWaiting},
		{phase: runtimeui.PhaseRecovering},
	}
	for _, tt := range tests {
		t.Run(string(tt.phase), func(t *testing.T) {
			model := New(context.Background(), &fakeService{}, testDisplayConfig())
			model.snapshot.Phase = tt.phase
			model.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
			if got := model.textarea.Value() != ""; got != tt.editable {
				t.Fatalf("editable=%v value=%q", tt.editable, model.textarea.Value())
			}
			if view := model.View().Content; !strings.Contains(view, phaseText(tt.phase)) {
				t.Fatalf("view status does not reflect %q: %q", tt.phase, view)
			}
			_, interrupt := model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			if (interrupt != nil) != tt.interrupts {
				t.Fatalf("interrupt command=%v", interrupt != nil)
			}
		})
	}
}

type panicModel struct{ where string }

func (p panicModel) Init() tea.Cmd {
	if p.where == "init" {
		panic("secret /tmp/path")
	}
	if p.where == "cmd" {
		return func() tea.Msg { panic("secret") }
	}
	return nil
}
func (p panicModel) Update(tea.Msg) (tea.Model, tea.Cmd) {
	if p.where == "update" {
		panic("secret")
	}
	return p, nil
}
func (p panicModel) View() tea.View {
	if p.where == "view" {
		panic("secret")
	}
	return tea.NewView("ok")
}

func TestSafeModelRedactsPanics(t *testing.T) {
	for _, where := range []string{"init", "update", "view", "cmd"} {
		t.Run(where, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			wrapped, fatal := Safe(panicModel{where: where}, cancel)
			safe := wrapped.(*safeModel)
			switch where {
			case "init":
				safe.Init()
			case "update":
				safe.Update(struct{}{})
			case "view":
				v := safe.View()
				if strings.Contains(v.Content, "secret") {
					t.Fatal("panic leaked")
				}
			case "cmd":
				safe.Init()()
			}
			if !fatal.Marked() {
				t.Fatal("fatal not marked")
			}
			if !errors.Is(ctx.Err(), context.Canceled) {
				t.Fatal("context not canceled")
			}
		})
	}
}
