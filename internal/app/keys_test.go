package app

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
)

func TestEnterBlankAndControlDContract(t *testing.T) {
	model := New(context.Background(), &fakeService{}, testDisplayConfig())
	model.textarea.SetValue(" \n")
	_, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if command != nil || model.snapshot.Phase != runtimeui.PhaseIdle {
		t.Fatal("blank prompt submitted")
	}
	model.textarea.SetValue("unsent draft")
	_, command = model.Update(tea.KeyPressMsg{Code: 'd', Text: "d", Mod: tea.ModCtrl})
	if command == nil {
		t.Fatal("ctrl+d did not quit while idle")
	}
	if _, ok := command().(tea.QuitMsg); !ok {
		t.Fatalf("ctrl+d command = %T", command())
	}
	model.snapshot.Phase = runtimeui.PhaseRecoveryWaiting
	_, command = model.Update(tea.KeyPressMsg{Code: 'd', Text: "d", Mod: tea.ModCtrl})
	if command != nil {
		t.Fatal("ctrl+d quit during recovery")
	}
}

func TestAltEnterBuildsOneMultilineSubmission(t *testing.T) {
	run := newFakeRun("run")
	service := &fakeService{start: runtimeui.ActionResult{Kind: runtimeui.ActionStarted, Run: run, Snapshot: runtimeui.Snapshot{RunID: "run", Version: 1, Phase: runtimeui.PhaseRunning}}}
	model := New(context.Background(), service, testDisplayConfig())
	model.textarea.SetValue("first")
	model.Update(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	model.Update(tea.PasteMsg{Content: "second"})
	if got := model.textarea.Value(); got != "first\nsecond" {
		t.Fatalf("multiline draft=%q", got)
	}
	_, command := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if command == nil {
		t.Fatal("multiline prompt did not submit")
	}
	command()
	if service.startedPrompt != "first\nsecond" {
		t.Fatalf("submitted=%q", service.startedPrompt)
	}
}
