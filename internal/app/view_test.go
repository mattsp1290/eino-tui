package app

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	agentmodel "github.com/mattsp1290/eino-agent/model"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
	"github.com/mattsp1290/eino-tui/internal/textsafe"
)

func TestViewUsesOnlySanitizedPresentationState(t *testing.T) {
	model := New(context.Background(), &fakeService{}, testDisplayConfig())
	malicious := "safe\x1b]0;title\a\x1b[31m red\x1b[0m\u202e"
	model.snapshot = runtimeui.Snapshot{Messages: []runtimeui.Message{{Role: runtimeui.RoleAssistant, Content: textsafe.Display(malicious)}}}
	model.resize(40, 12)
	content := ansi.Strip(model.View().Content)
	if strings.Contains(content, "title") || strings.Contains(content, "\u202e") {
		t.Fatalf("untrusted content reached view: %q", content)
	}
	if !strings.Contains(content, "safe red") {
		t.Fatalf("sanitized content missing: %q", content)
	}
}

func TestViewUsesImmutableBoundedCodexMetadata(t *testing.T) {
	model := New(context.Background(), &fakeService{}, Config{
		InitialSelection:       agentmodel.Selection{ProviderID: codexmodel.ProviderID, ModelID: "gpt-5.6"},
		InitialReasoningEffort: codexmodel.ReasoningEffortMedium,
	})
	model.snapshot = runtimeui.Snapshot{
		Phase:        runtimeui.PhaseRunning,
		Messages:     []runtimeui.Message{{Role: runtimeui.RoleAssistant, Content: "stable"}},
		LiveMessages: []runtimeui.Message{{ID: "live", Role: runtimeui.RoleAssistant, Content: "live"}},
	}
	model.resize(100, 15)
	content := model.View().Content
	for _, want := range []string{"eino-tui · Codex subscription · gpt-5.6 (gpt-5.6) · medium", "Codex:", "Codex (streaming):", "Streaming Codex response…"} {
		if !strings.Contains(content, want) {
			t.Fatalf("missing %q in %q", want, content)
		}
	}
	if strings.Count(content, "gpt-5.6") != 2 || strings.Contains(content, "TOKEN") {
		t.Fatalf("unsafe/repeated model metadata: %q", content)
	}
	unsafe := New(context.Background(), &fakeService{}, Config{
		InitialSelection:       agentmodel.Selection{ProviderID: codexmodel.ProviderID, ModelID: agentmodel.ID(strings.Repeat("p", 129) + "\n/account/SECRET")},
		InitialReasoningEffort: "SECRET",
	})
	unsafe.resize(100, 10)
	unsafeView := unsafe.View().Content
	if !strings.Contains(unsafeView, "Codex subscription · gpt-5.5 (gpt-5.5) · medium") || strings.Contains(unsafeView, "SECRET") || strings.Contains(unsafeView, "/account/") {
		t.Fatalf("unsafe metadata was not bounded: %q", unsafeView)
	}
}

func TestViewRendersToolLifecycleInMessageOrderAndSuppressesDuplicates(t *testing.T) {
	model := New(context.Background(), &fakeService{}, testDisplayConfig())
	tool := runtimeui.ToolActivity{ID: "call", Name: "file_read", Subject: "internal/app/view.go", Status: runtimeui.ToolRunning}
	model.snapshot = runtimeui.Snapshot{
		Phase:    runtimeui.PhaseRunning,
		Messages: []runtimeui.Message{{ID: "user", Role: runtimeui.RoleUser, Content: "inspect"}},
		LiveMessages: []runtimeui.Message{
			{ID: "request", Role: runtimeui.RoleAssistant, Content: "I’ll inspect it.", Tools: []runtimeui.ToolActivity{tool}},
			{ID: "continuation", Role: runtimeui.RoleAssistant, Content: "Later answer.", Tools: []runtimeui.ToolActivity{tool}},
		},
	}
	model.resize(80, 20)
	content := ansi.Strip(model.View().Content)
	preamble := strings.Index(content, "I’ll inspect it.")
	activity := strings.Index(content, "Tool · file_read · internal/app/view.go · running")
	continuation := strings.Index(content, "Later answer.")
	if preamble < 0 || activity < preamble || continuation < activity || strings.Count(content, "Tool · file_read") != 1 {
		t.Fatalf("unexpected order or duplication: %q", content)
	}
}

func TestViewRendersToolOnlyAndSanitizesDefensively(t *testing.T) {
	model := New(context.Background(), &fakeService{}, testDisplayConfig())
	model.snapshot = runtimeui.Snapshot{Phase: runtimeui.PhaseRunning, LiveMessages: []runtimeui.Message{{
		ID: "request", Role: runtimeui.RoleAssistant,
		Tools: []runtimeui.ToolActivity{{ID: "call", Name: "search\x1b[31m", Subject: "safe\x1b]0;SECRET\a\npath", Status: runtimeui.ToolCompleted}},
	}}}
	model.resize(18, 8)
	content := model.View().Content
	if !strings.Contains(content, "Tool") || !strings.Contains(content, "completed") || strings.Contains(content, "SECRET") || strings.Contains(content, "\x1b]0;") {
		t.Fatalf("unsafe or missing tool row: %q", content)
	}
}

func TestViewRendersEveryFixedToolStatus(t *testing.T) {
	statuses := []runtimeui.ToolStatus{
		runtimeui.ToolPending,
		runtimeui.ToolRunning,
		runtimeui.ToolCompleted,
		runtimeui.ToolFailed,
		runtimeui.ToolInterrupted,
	}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			model := New(context.Background(), &fakeService{}, testDisplayConfig())
			model.snapshot = runtimeui.Snapshot{Messages: []runtimeui.Message{{
				ID: "request", Role: runtimeui.RoleAssistant,
				Tools: []runtimeui.ToolActivity{{ID: "call", Name: "file_list", Subject: ".", Status: status}},
			}}}
			model.resize(40, 10)
			content := ansi.Strip(model.View().Content)
			if !strings.Contains(content, "Tool · file_list · . · "+string(status)) {
				t.Fatalf("status row missing: %q", content)
			}
		})
	}
}

func TestStableTranscriptCacheIncludesNestedToolChanges(t *testing.T) {
	model := New(context.Background(), &fakeService{}, testDisplayConfig())
	model.snapshot = runtimeui.Snapshot{Messages: []runtimeui.Message{{ID: "request", Role: runtimeui.RoleAssistant, Tools: []runtimeui.ToolActivity{{ID: "call", Name: "glob", Subject: "*.go · in .", Status: runtimeui.ToolPending}}}}}
	model.resize(60, 12)
	first := model.stableTranscript
	model.snapshot.Messages[0].Tools[0].Status = runtimeui.ToolCompleted
	model.refreshTranscript()
	if first == model.stableTranscript || !strings.Contains(model.stableTranscript, "completed") {
		t.Fatalf("nested activity did not invalidate cache: %q", model.stableTranscript)
	}
	statusVersion := model.stableTranscript
	model.snapshot.Messages[0].Tools[0].Subject = "internal/runtimeui/history.go"
	model.refreshTranscript()
	if statusVersion == model.stableTranscript || !strings.Contains(model.stableTranscript, "internal/runtimeui/history.go") {
		t.Fatalf("nested subject did not invalidate cache: %q", model.stableTranscript)
	}
	copySnapshot := cloneRuntimeMessages(model.snapshot.Messages)
	before := model.stableTranscript
	model.snapshot.Messages = copySnapshot
	model.refreshTranscript()
	if model.stableTranscript != before {
		t.Fatal("identical deep copy changed cached rendering")
	}
}
