package app

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
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
