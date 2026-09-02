package app

import (
	"context"
	"strings"
	"testing"

	"github.com/mattsp1290/eino-tui/internal/runtimeui"
	"github.com/mattsp1290/eino-tui/internal/textsafe"
)

func TestViewUsesOnlySanitizedPresentationState(t *testing.T) {
	model := New(context.Background(), &fakeService{})
	malicious := "safe\x1b]0;title\a\x1b[31m red\x1b[0m\u202e"
	model.snapshot = runtimeui.Snapshot{Messages: []runtimeui.Message{{Role: runtimeui.RoleAssistant, Content: textsafe.Display(malicious)}}}
	model.resize(40, 12)
	content := model.View().Content
	if strings.Contains(content, "title") || strings.Contains(content, "\u202e") {
		t.Fatalf("untrusted content reached view: %q", content)
	}
	if !strings.Contains(content, "safe red") {
		t.Fatalf("sanitized content missing: %q", content)
	}
}
