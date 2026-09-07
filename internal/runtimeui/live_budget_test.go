package runtimeui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/mattsp1290/eino-agent/config"
	agentruntime "github.com/mattsp1290/eino-agent/runtime"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-agent/stream"
	"github.com/mattsp1290/eino-tui/internal/textsafe"
)

type activeSeedStore struct{ replayOnlyStore }

func (activeSeedStore) GetRun(context.Context, session.RunID) (session.Run, error) {
	return session.Run{ID: "active", SessionID: "s", Status: session.RunRunning}, nil
}
func (activeSeedStore) Close() error { return nil }

type seedHandle struct{ done chan agentruntime.Result }

func (*seedHandle) RunID() session.RunID                    { return "active" }
func (h *seedHandle) Done() <-chan agentruntime.Result      { return h.done }
func (*seedHandle) Interrupt(context.Context, string) error { return nil }

func TestAdmittedHistoryRespectsLiveBudgetsAndReconciles(t *testing.T) {
	for _, test := range []struct {
		name  string
		texts []int
		tools []int
	}{
		{name: "aggregate text", texts: []int{150 * 1024, 150 * 1024}},
		{name: "aggregate tools", tools: []int{32, 33}},
		{name: "oversized group", tools: []int{65}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			store := makeActiveSeedStore(t, test.texts, test.tools)
			chat := newService(ctx, store, stream.NewTail(8), nil, nil, "s", config.Snapshot{})
			a, err := chat.beginAttempt(ctx, stateIdle, stateStarting)
			if err != nil {
				t.Fatal(err)
			}
			tailCanceled := make(chan struct{})
			a.cancelTail = func() {
				select {
				case <-tailCanceled:
				default:
					close(tailCanceled)
				}
			}
			handle := &seedHandle{done: make(chan agentruntime.Result, 1)}
			t.Cleanup(func() {
				closeCtx, cancel := context.WithTimeout(ctx, time.Second)
				defer cancel()
				select {
				case handle.done <- agentruntime.Result{RunID: "active", Status: session.RunCompleted}:
				default:
				}
				if err := chat.Close(closeCtx); err != nil {
					t.Error(err)
				}
			})
			events := make(chan session.EventRecord, 1)
			events <- session.EventRecord{Kind: agentruntime.EventMessageDelta, SessionID: "s", RunID: "active", MessageID: "buffered", Payload: []byte(`{"content":"must wait for reconciliation"}`)}
			result, err := chat.publishAdmitted(a, handle, events, "user prompt")
			chat.finishPending(a)
			if err != nil {
				t.Fatal(err)
			}
			if !result.Snapshot.Resync || len(result.Snapshot.Messages) != 1 || result.Snapshot.Messages[0].Content != "user prompt" {
				t.Fatal("overflow lost stable history or did not request resync")
			}
			assertLiveBudgets(t, result.Snapshot.LiveMessages)
			if len(result.Snapshot.LiveMessages) != len(store.batch.Messages)-2 {
				t.Fatal("overflow did not preserve whole message groups")
			}
			select {
			case <-tailCanceled:
			case <-time.After(time.Second):
				t.Fatal("over-budget seed did not cancel live events")
			}
			handle.done <- agentruntime.Result{RunID: "active", Status: session.RunCompleted}
			terminal, provisional := drainRun(t, result.Run)
			if provisional != 0 || !terminal.Resync || len(terminal.LiveMessages) != 0 || len(terminal.Messages) != len(store.batch.Messages) {
				t.Fatalf("reconciliation: provisional=%d live=%d durable=%d", provisional, len(terminal.LiveMessages), len(terminal.Messages))
			}
		})
	}
}

func makeActiveSeedStore(t *testing.T, textSizes, toolCounts []int) activeSeedStore {
	t.Helper()
	store := activeSeedStore{replayOnlyStore{calls: make(map[session.ToolCallID]session.ToolCall)}}
	appendText := func(id session.MessageID, content string) {
		payload, err := json.Marshal(map[string]string{"text": content})
		if err != nil {
			t.Fatal(err)
		}
		store.batch.Parts = append(store.batch.Parts, session.Part{ID: session.PartID(string(id) + "-text"), MessageID: id, SessionID: "s", RunID: "active", Kind: session.PartText, Payload: payload})
	}
	store.batch.Messages = append(store.batch.Messages, session.Message{ID: "user", SessionID: "s", RunID: "active", Role: session.RoleUser})
	appendText("user", "user prompt")
	for i := range max(len(textSizes), len(toolCounts)) {
		id := session.MessageID(fmt.Sprintf("message-%d", i))
		store.batch.Messages = append(store.batch.Messages, session.Message{ID: id, SessionID: "s", RunID: "active", Role: session.RoleAssistant})
		if i < len(textSizes) {
			appendText(id, strings.Repeat("x", textSizes[i]))
		}
		if i < len(toolCounts) {
			for j := range toolCounts[i] {
				callID := session.ToolCallID(fmt.Sprintf("call-%d-%d", i, j))
				input := json.RawMessage(`{"path":"fixture.txt"}`)
				payload, err := json.Marshal(map[string]any{"id": callID, "name": "file_read", "arguments": input})
				if err != nil {
					t.Fatal(err)
				}
				store.batch.Parts = append(store.batch.Parts, session.Part{ID: session.PartID(callID), MessageID: id, SessionID: "s", RunID: "active", Kind: session.PartToolCall, Ordinal: int64(j + 1), Payload: payload})
				store.calls[callID] = session.ToolCall{ID: callID, SessionID: "s", RunID: "active", MessageID: id, Name: "file_read", Input: input, Status: session.ToolCallCompleted}
			}
		}
	}
	return store
}

func TestAccumulatorRejectsSeedGroupsBeforeRetainingThem(t *testing.T) {
	for _, test := range []struct {
		name string
		seed Message
	}{
		{name: "text", seed: Message{Content: strings.Repeat("x", textsafe.MaxDisplayBytes+1)}},
		{name: "tool count", seed: Message{Tools: make([]ToolActivity, MaxLiveToolActivities+1)}},
		{name: "tool bytes", seed: Message{Tools: []ToolActivity{{ID: "call", Name: "file_read", Status: ToolPending, Subject: strings.Repeat("x", MaxLiveToolDisplayBytes)}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.seed.ID, test.seed.Role = "oversized", RoleAssistant
			acc := newEventAccumulator([]Message{test.seed})
			if !acc.resync || len(acc.messages) != 0 {
				t.Fatal("over-budget group entered accumulator")
			}
			assertLiveBudgets(t, acc.messages)
		})
	}
}

func assertLiveBudgets(t *testing.T, messages []Message) {
	t.Helper()
	textBytes, toolCount, toolBytes := 0, 0, 0
	for _, message := range messages {
		textBytes += len(message.Content)
		toolCount += len(message.Tools)
		toolBytes += messageDisplayBytes(message) - len(message.Content)
	}
	if textBytes > textsafe.MaxDisplayBytes || toolCount > MaxLiveToolActivities || toolBytes > MaxLiveToolDisplayBytes {
		t.Fatalf("live budgets exceeded: text=%d count=%d tool bytes=%d", textBytes, toolCount, toolBytes)
	}
}
