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

func TestViewUsesImmutableBoundedCodexMetadata(t *testing.T) {
	model := New(context.Background(), &fakeService{}, Config{Provider: "Codex subscription\x1b]0;TOKEN\a", Model: "gpt-5.6"})
	model.snapshot = runtimeui.Snapshot{
		Phase:         runtimeui.PhaseRunning,
		Messages:      []runtimeui.Message{{Role: runtimeui.RoleAssistant, Content: "stable"}},
		LiveAssistant: "live",
	}
	model.resize(100, 15)
	content := model.View().Content
	for _, want := range []string{"eino-tui · Codex subscription · gpt-5.6", "Codex:", "Codex (streaming):", "Streaming Codex response…"} {
		if !strings.Contains(content, want) {
			t.Fatalf("missing %q in %q", want, content)
		}
	}
	if strings.Count(content, "gpt-5.6") != 1 || strings.Contains(content, "TOKEN") {
		t.Fatalf("unsafe/repeated model metadata: %q", content)
	}
	unsafe := New(context.Background(), &fakeService{}, Config{Provider: strings.Repeat("p", 129), Model: "gpt-5.6\n/account/SECRET"})
	unsafe.resize(100, 10)
	unsafeView := unsafe.View().Content
	if !strings.Contains(unsafeView, "Codex subscription · unknown model") || strings.Contains(unsafeView, "SECRET") || strings.Contains(unsafeView, "/account/") {
		t.Fatalf("unsafe metadata was not bounded: %q", unsafeView)
	}
}
