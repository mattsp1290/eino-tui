package app

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
)

type updateCommandPanicModel struct{}

func (updateCommandPanicModel) Init() tea.Cmd { return nil }
func (updateCommandPanicModel) Update(tea.Msg) (tea.Model, tea.Cmd) {
	return updateCommandPanicModel{}, func() tea.Msg { panic("prompt /private/path\x1b]0;secret\a") }
}
func (updateCommandPanicModel) View() tea.View { return tea.NewView("ok") }

func TestSafeWrapsCommandsReturnedByUpdate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	wrapped, fatal := Safe(updateCommandPanicModel{}, cancel)
	_, command := wrapped.Update(struct{}{})
	message := command()
	if _, ok := message.(fatalMsg); !ok {
		t.Fatalf("message = %T", message)
	}
	if !fatal.Marked() || ctx.Err() == nil {
		t.Fatal("panic did not trigger fatal cancellation")
	}
	if strings.Contains(FatalDiagnostic, "prompt") || strings.Contains(FatalDiagnostic, "/private") {
		t.Fatal("diagnostic is not fixed")
	}
}

type toolViewPanicModel struct{ activity runtimeui.ToolActivity }

func (toolViewPanicModel) Init() tea.Cmd                         { return nil }
func (m toolViewPanicModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }
func (m toolViewPanicModel) View() tea.View                      { panic(m.activity.Subject) }

func TestSafeRedactsRenderingPanicContainingToolFixture(t *testing.T) {
	const secret = "SECRET_TOOL_ERROR /private/workspace/file.txt\x1b]0;owned\a"
	wrapped, fatal := Safe(toolViewPanicModel{activity: runtimeui.ToolActivity{
		ID: "call", Name: "file_read", Subject: secret, Status: runtimeui.ToolFailed,
	}}, func() {})
	view := wrapped.View()
	if view.Content != FatalDiagnostic || strings.Contains(view.Content, "SECRET") || strings.Contains(view.Content, "/private/") || !fatal.Marked() {
		t.Fatalf("unsafe panic rendering: content=%q fatal=%v", view.Content, fatal.Marked())
	}
}
