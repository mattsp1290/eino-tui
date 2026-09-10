package app

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
)

func TestRenameEditorSubmitsNormalizedTitleAndKeepsInputOnFailure(t *testing.T) {
	service := &fakeService{renamed: conversationInfo("conv-1", 1, 1, "Release plan")}
	model := readyConversationModel(service)
	model.textarea.SetValue("unsent draft")
	model.Update(altKey('r'))
	if model.conv.mode != convRenaming || model.conv.input.Value() != "Conversation 1 — first" || !strings.Contains(ansi.Strip(model.View().Content), "Rename conversation") {
		t.Fatalf("editor mode=%v value=%q", model.conv.mode, model.conv.input.Value())
	}
	if model.Update(altKey('m')); model.picker.mode != pickerClosed || model.conv.mode != convRenaming {
		t.Fatal("model picker opened while renaming")
	}
	model.conv.input.SetValue("")
	if _, cmd := model.Update(enterKey()); cmd != nil || model.conv.mode != convRenaming || len(service.renames) != 0 {
		t.Fatal("blank title was submitted")
	}
	model.Update(tea.PasteMsg{Content: "  Release\nplan \x1b[31m "})
	if model.conv.input.Value() != "Release plan" {
		t.Fatalf("pasted value = %q", model.conv.input.Value())
	}
	if model.textarea.Value() != "unsent draft" {
		t.Fatal("paste reached the prompt editor")
	}
	service.renameErr = errors.New("TOKEN /tmp/private")
	_, save := model.Update(enterKey())
	if save == nil || model.conv.mode != convSaving {
		t.Fatalf("save mode=%v", model.conv.mode)
	}
	model.Update(save())
	if model.conv.mode != convRenaming || model.conv.input.Value() != "Release plan" || model.conv.err != noticeRenameFailed || strings.Contains(model.View().Content, "TOKEN") {
		t.Fatalf("failure mode=%v value=%q err=%q", model.conv.mode, model.conv.input.Value(), model.conv.err)
	}
	service.renameErr = runtimeui.ErrInvalidTitle
	_, invalid := model.Update(enterKey())
	model.Update(invalid())
	if model.conv.mode != convRenaming || model.conv.err != noticeRenameInvalid {
		t.Fatalf("invalid mode=%v err=%q", model.conv.mode, model.conv.err)
	}
	service.renameErr = nil
	_, ok := model.Update(enterKey())
	model.Update(ok())
	if model.conv.mode != convClosed || model.current.Title != "Release plan" || service.renames[len(service.renames)-1] != "Release plan" {
		t.Fatalf("success mode=%v title=%q renames=%v", model.conv.mode, model.current.Title, service.renames)
	}
	if !strings.Contains(ansi.Strip(model.View().Content), "Release plan") || model.textarea.Value() != "unsent draft" {
		t.Fatal("header not updated or draft lost")
	}
}

func TestRenameEscapeCancelsWithoutWriteAndLocalValidation(t *testing.T) {
	service := &fakeService{}
	model := readyConversationModel(service)
	model.Update(altKey('r'))
	model.conv.input.SetValue("changed")
	model.Update(escKey())
	if model.conv.mode != convClosed || len(service.renames) != 0 || model.current.Title != "Conversation 1 — first" {
		t.Fatal("escape wrote or changed the title")
	}
	model.Update(altKey('r'))
	if model.conv.input.CharLimit != 256 {
		t.Fatalf("editor limit = %d", model.conv.input.CharLimit)
	}
	model.conv.input.CharLimit = 0
	model.conv.input.SetValue(strings.Repeat("x", 257))
	if _, cmd := model.Update(enterKey()); cmd != nil || model.conv.err != noticeRenameInvalid || len(service.renames) != 0 {
		t.Fatalf("oversized title reached the service: err=%q", model.conv.err)
	}
	model.conv.input.CharLimit = 256
	model.conv.input.SetValue("ok")
	model.Update(plainKey('!'))
	if model.conv.input.Value() != "ok!" {
		t.Fatalf("typing = %q", model.conv.input.Value())
	}
	model.Update(escKey())
	// A stale rename result cannot rename a later selection.
	model.Update(conversationRenamedMsg{op: model.conv.op, generation: 7, info: conversationInfo("conv-1", 7, 1, "stale")})
	if model.current.Title != "Conversation 1 — first" {
		t.Fatal("stale rename applied")
	}
}

func TestPickerRenameSelectsBeforeEditing(t *testing.T) {
	service := &fakeService{page: twoConversations()}
	service.selected = runtimeui.SelectionResult{Snapshot: runtimeui.Snapshot{Phase: runtimeui.PhaseIdle, Conversation: conversationInfo("conv-2", 2, 2, "Conversation 2 — second")}}
	model := readyConversationModel(service)
	_, open := model.Update(altKey('s'))
	model.Update(open())
	model.Update(plainKey('k'))
	_, sel := model.Update(plainKey('r'))
	if sel == nil || model.conv.mode != convSelecting || len(service.renames) != 0 {
		t.Fatalf("rename mutated without selecting: mode=%v", model.conv.mode)
	}
	model.Update(sel())
	if model.conv.mode != convRenaming || model.current.ID != "conv-2" || model.conv.input.Value() != "Conversation 2 — second" {
		t.Fatalf("editor after select mode=%v current=%s value=%q", model.conv.mode, model.current.ID, model.conv.input.Value())
	}
	model.Update(escKey())
	_, reopen := model.Update(altKey('s'))
	model.Update(reopen())
	model.Update(plainKey('j'))
	model.Update(plainKey('k'))
	// R on the already selected conversation opens the editor directly.
	model.conv.highlighted, model.conv.highlightedID = 0, "conv-2"
	model.conv.page.Items[0].Selected = true
	if _, cmd := model.Update(plainKey('r')); cmd != nil || model.conv.mode != convRenaming {
		t.Fatalf("direct rename mode=%v", model.conv.mode)
	}
}

func TestRunResultsAreIsolatedByGeneration(t *testing.T) {
	service := &fakeService{}
	model := readyConversationModel(service)
	run := newFakeRun("run-1", runtimeui.Snapshot{RunID: "run-1", Version: 2, Terminal: true, Phase: runtimeui.PhaseIdle, Conversation: conversationInfo("conv-1", 1, 1, "Conversation 1 — renamed by agent"), Messages: []runtimeui.Message{{Role: runtimeui.RoleUser, Content: "hi"}}})
	stale := runtimeui.ActionResult{Kind: runtimeui.ActionStarted, Run: run, Snapshot: runtimeui.Snapshot{RunID: "run-1", Version: 1, Phase: runtimeui.PhaseRunning, Conversation: conversationInfo("conv-0", 0, 0, "old")}}
	stale.Snapshot.Conversation.Generation = 5
	model.textarea.SetValue("draft")
	model.snapshot.Phase = runtimeui.PhaseStarting
	model.Update(startedMsg{draft: "draft", result: stale})
	if model.pending != nil || model.textarea.Value() != "draft" {
		t.Fatal("stale start result installed a run or consumed the draft")
	}
	current := stale
	current.Snapshot.Conversation = conversationInfo("conv-1", 1, 1, "Conversation 1 — first")
	_, next := model.Update(startedMsg{draft: "draft", result: current})
	if model.pending == nil || model.textarea.Value() != "" || next == nil {
		t.Fatal("current start result not installed")
	}
	model.Update(next())
	if model.snapshot.Phase != runtimeui.PhaseIdle || model.current.Title != "Conversation 1 — renamed by agent" {
		t.Fatalf("terminal snapshot did not refresh title: %#v", model.current)
	}
}
