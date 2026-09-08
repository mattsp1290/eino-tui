package runtimeui

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mattsp1290/eino-agent/session"
)

func TestSummarizeToolCallAllowlistAndStatuses(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		status session.ToolCallStatus
		want   string
		wantOK bool
	}{
		{name: "file_read", input: `{"path":"internal/app/view.go","offset":7,"limit":20}`, status: session.ToolCallPending, want: "internal/app/view.go · line 7, 20 lines", wantOK: true},
		{name: "file_list", input: `{}`, status: session.ToolCallRunning, want: ".", wantOK: true},
		{name: "glob", input: `{"pattern":"**/*.go","path":"internal"}`, status: session.ToolCallCompleted, want: "**/*.go · in internal", wantOK: true},
		{name: "search", input: `{"pattern":"TODO","path":"."}`, status: session.ToolCallFailed, want: "TODO · in .", wantOK: true},
		{name: "shell", input: `{"command":"cat secret"}`, status: session.ToolCallInterrupted, wantOK: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			activity, ok := summarizeToolCall(session.ToolCall{ID: "call", Name: test.name, Input: json.RawMessage(test.input), Status: test.status})
			if ok != test.wantOK || ok && activity.Subject != test.want {
				t.Fatalf("activity=%#v ok=%v", activity, ok)
			}
		})
	}
}

func TestToolSubjectIsBoundedAndTerminalSafe(t *testing.T) {
	input, _ := json.Marshal(map[string]string{"path": "safe\x1b]0;private\a\n\t" + strings.Repeat("界", 200)})
	activity, ok := summarizeToolCall(session.ToolCall{ID: "call", Name: "file_read", Input: input, Status: session.ToolCallCompleted, Output: json.RawMessage(`{"secret":"must-not-project"}`), Error: "/private/error"})
	if !ok || len(activity.Subject) > MaxToolSubjectBytes || !utf8.ValidString(activity.Subject) || strings.Contains(activity.Subject, "private") || strings.Contains(activity.Subject, "\n") || strings.Contains(activity.Subject, "\t") {
		t.Fatalf("unsafe subject=%q", activity.Subject)
	}
	visible := activity.Name + activity.Subject + string(activity.Status)
	if strings.Contains(visible, "must-not-project") || strings.Contains(visible, "/private/error") {
		t.Fatal("tool output or error projected")
	}
}

func TestMalformedToolSubjectUsesFixedFallback(t *testing.T) {
	for _, raw := range []string{"{", `{}`, `{"path":42}`, "{\"path\":\"\xff\"}"} {
		activity, ok := summarizeToolCall(session.ToolCall{ID: "call", Name: "file_read", Input: json.RawMessage(raw), Status: session.ToolCallCompleted})
		if !ok || activity.Subject != unavailableToolSubject {
			t.Fatalf("input=%q activity=%#v ok=%v", raw, activity, ok)
		}
	}
}
