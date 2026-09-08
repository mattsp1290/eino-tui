package runtimeui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	agentruntime "github.com/mattsp1290/eino-agent/runtime"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/textsafe"
)

type eventToolStore struct {
	call session.ToolCall
	err  error
}

type blockingEventToolStore struct{}

func (blockingEventToolStore) GetToolCall(ctx context.Context, _ session.ToolCallID) (session.ToolCall, error) {
	<-ctx.Done()
	return session.ToolCall{}, ctx.Err()
}

func (s eventToolStore) GetToolCall(context.Context, session.ToolCallID) (session.ToolCall, error) {
	return s.call, s.err
}

func TestEventAccumulatorSanitizesAcrossChunks(t *testing.T) {
	var accumulator eventAccumulator
	events := []session.EventRecord{
		{Kind: agentruntime.EventMessageDelta, SessionID: "s", RunID: "r", MessageID: "m", Payload: []byte(`{"content":"safe\u001b]0;secret"}`)},
		{Kind: agentruntime.EventMessageDelta, SessionID: "s", RunID: "r", MessageID: "m", Payload: []byte(`{"content":"\u0007text"}`)},
	}
	for _, event := range events {
		accumulator.accept(context.Background(), nil, event, "s", "r")
	}
	update := accumulator.accept(context.Background(), nil, session.EventRecord{Kind: agentruntime.EventMessageDelta, SessionID: "s", RunID: "r", MessageID: "m", Payload: []byte(`{"content":"!"}`)}, "s", "r")
	if !update.changed || len(update.messages) != 1 || update.messages[0].Content != "safetext!" {
		t.Fatalf("got %#v", update)
	}
}

func TestEventAccumulatorANSISequenceAtEveryBoundary(t *testing.T) {
	raw := "safe\x1b]8;;https://secret.example\aRED\x1b]8;;\adone"
	for boundary := 1; boundary < len(raw); boundary++ {
		t.Run(fmt.Sprintf("split-%d", boundary), func(t *testing.T) {
			var accumulator eventAccumulator
			chunks := []string{raw[:boundary], raw[boundary:]}
			got := ""
			for _, chunk := range chunks {
				payload, _ := json.Marshal(map[string]string{"content": chunk})
				if update := accumulator.accept(context.Background(), nil, session.EventRecord{Kind: agentruntime.EventMessageDelta, SessionID: "s", RunID: "r", MessageID: "m", Payload: payload}, "s", "r"); update.changed {
					got = update.messages[0].Content
				}
			}
			if got != "safeREDdone" {
				t.Fatalf("got %q", got)
			}
		})
	}
}

func TestEventAccumulatorHandlesOverflowBeforeRunFilter(t *testing.T) {
	var accumulator eventAccumulator
	accumulator.accept(context.Background(), nil, session.EventRecord{Kind: agentruntime.EventTailOverflow, SessionID: "s"}, "s", "r")
	if !accumulator.resync {
		t.Fatal("overflow did not request resync")
	}
}

func TestEventAccumulatorFoldsToolStatusInPlace(t *testing.T) {
	call := session.ToolCall{ID: "call", SessionID: "s", RunID: "r", MessageID: "m", Name: "file_read", Input: json.RawMessage(`{"path":"safe.txt"}`), Status: session.ToolCallPending}
	a := newEventAccumulator(nil)
	event := session.EventRecord{ID: "event", Kind: agentruntime.EventToolCallUpdated, SessionID: "s", RunID: "r", MessageID: "m", ToolCallID: "call", ToolTransition: session.ToolTransitionPending}
	update := a.accept(context.Background(), eventToolStore{call: call}, event, "s", "r")
	if !update.changed || len(update.messages) != 1 || len(update.messages[0].Tools) != 1 || update.messages[0].Tools[0].Status != ToolPending {
		t.Fatalf("pending update=%#v", update)
	}
	call.Status = session.ToolCallCompleted
	event.ID = "terminal"
	event.ToolTransition = session.ToolTransitionTerminal
	update = a.accept(context.Background(), eventToolStore{call: call}, event, "s", "r")
	if !update.changed || len(update.messages[0].Tools) != 1 || update.messages[0].Tools[0].Status != ToolCompleted {
		t.Fatalf("terminal update=%#v", update)
	}
	duplicate := a.accept(context.Background(), eventToolStore{call: call}, event, "s", "r")
	if duplicate.changed || a.resync {
		t.Fatalf("duplicate=%#v resync=%v", duplicate, a.resync)
	}
}

func TestEventAccumulatorRecoveryInterleavingAndCloneIsolation(t *testing.T) {
	eventFor := func(eventID, callID, messageID string, phase session.ToolTransitionPhase) session.EventRecord {
		return session.EventRecord{ID: session.EventID(eventID), Kind: agentruntime.EventToolCallUpdated, SessionID: "s", RunID: "r", MessageID: session.MessageID(messageID), ToolCallID: session.ToolCallID(callID), ToolTransition: phase}
	}
	callFor := func(id, messageID, path string, status session.ToolCallStatus) session.ToolCall {
		input, _ := json.Marshal(map[string]string{"path": path})
		return session.ToolCall{ID: session.ToolCallID(id), SessionID: "s", RunID: "r", MessageID: session.MessageID(messageID), Name: "file_read", Input: input, Status: status}
	}

	seed := []Message{{ID: "request", Role: RoleAssistant, Content: "preamble", Tools: []ToolActivity{{ID: "one", Name: "file_read", Subject: "one.txt", Status: ToolPending}}}}
	a := newEventAccumulator(seed)
	update := a.accept(context.Background(), eventToolStore{call: callFor("one", "request", "one.txt", session.ToolCallRunning)}, eventFor("one-running", "one", "request", session.ToolTransitionRunning), "s", "r")
	if !update.changed || a.resync || update.messages[0].Tools[0].Status != ToolRunning {
		t.Fatalf("seed overlay=%#v resync=%v", update, a.resync)
	}
	update.messages[0].Tools[0].Subject = "MUTATED RETURN VALUE"

	update = a.accept(context.Background(), eventToolStore{call: callFor("two", "request", "two.txt", session.ToolCallCompleted)}, eventFor("two-pending-buffered", "two", "request", session.ToolTransitionPending), "s", "r")
	if !update.changed || a.resync || len(update.messages) != 1 || len(update.messages[0].Tools) != 2 {
		t.Fatalf("interleaved terminal seed=%#v resync=%v", update, a.resync)
	}
	if update.messages[0].Tools[0].ID != "one" || update.messages[0].Tools[0].Subject != "one.txt" || update.messages[0].Tools[1].ID != "two" || update.messages[0].Tools[1].Status != ToolCompleted {
		t.Fatalf("tool order or clone isolation failed: %#v", update.messages[0].Tools)
	}

	payload, _ := json.Marshal(map[string]string{"content": "continuation"})
	update = a.accept(context.Background(), nil, session.EventRecord{Kind: agentruntime.EventMessageDelta, SessionID: "s", RunID: "r", MessageID: "continuation", Payload: payload}, "s", "r")
	if !update.changed || len(update.messages) != 2 || update.messages[1].Content != "continuation" {
		t.Fatalf("message order=%#v", update)
	}

	duplicate := a.accept(context.Background(), eventToolStore{call: callFor("two", "request", "two.txt", session.ToolCallCompleted)}, eventFor("two-stale", "two", "request", session.ToolTransitionPending), "s", "r")
	if duplicate.changed || a.resync {
		t.Fatalf("stale duplicate=%#v resync=%v", duplicate, a.resync)
	}
	_ = a.accept(context.Background(), eventToolStore{call: callFor("two", "request", "two.txt", session.ToolCallFailed)}, eventFor("two-conflict", "two", "request", session.ToolTransitionTerminal), "s", "r")
	if !a.resync {
		t.Fatal("conflicting terminal status did not request resync")
	}
}

func TestEventAccumulatorRejectsIncompleteToolEnvelope(t *testing.T) {
	base := session.EventRecord{ID: "event", Kind: agentruntime.EventToolCallUpdated, SessionID: "s", RunID: "r", MessageID: "m", ToolCallID: "call", ToolTransition: session.ToolTransitionPending}
	for _, mutate := range []func(*session.EventRecord){
		func(event *session.EventRecord) { event.ID = "" },
		func(event *session.EventRecord) { event.MessageID = "" },
		func(event *session.EventRecord) { event.ToolCallID = "" },
		func(event *session.EventRecord) { event.ToolTransition = "" },
	} {
		event := base
		mutate(&event)
		a := newEventAccumulator(nil)
		a.accept(context.Background(), eventToolStore{}, event, "s", "r")
		if !a.resync || len(a.messages) != 0 {
			t.Fatalf("incomplete event accepted: %#v", event)
		}
	}
}

func TestEventAccumulatorRejectsUnknownMismatchAndLookupFailure(t *testing.T) {
	base := session.EventRecord{ID: "event", Kind: agentruntime.EventToolCallUpdated, SessionID: "s", RunID: "r", MessageID: "m", ToolCallID: "call", ToolTransition: session.ToolTransitionPending}
	for _, test := range []struct {
		name  string
		store eventToolStore
	}{
		{name: "unknown", store: eventToolStore{call: session.ToolCall{ID: "call", SessionID: "s", RunID: "r", MessageID: "m", Name: "shell", Input: json.RawMessage(`{}`), Status: session.ToolCallPending}}},
		{name: "mismatch", store: eventToolStore{call: session.ToolCall{ID: "call", SessionID: "other", RunID: "r", MessageID: "m", Name: "file_read", Input: json.RawMessage(`{"path":"a"}`), Status: session.ToolCallPending}}},
		{name: "lookup", store: eventToolStore{err: errors.New("private store error")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := newEventAccumulator(nil)
			a.accept(context.Background(), test.store, base, "s", "r")
			if !a.resync || len(a.messages) != 0 {
				t.Fatalf("resync=%v messages=%#v", a.resync, a.messages)
			}
		})
	}
}

func TestEventAccumulatorToolCapIsExact(t *testing.T) {
	a := newEventAccumulator(nil)
	for i := 0; i < MaxLiveToolActivities; i++ {
		id := session.ToolCallID(fmt.Sprintf("call-%d", i))
		messageID := session.MessageID(fmt.Sprintf("message-%d", i))
		call := session.ToolCall{ID: id, SessionID: "s", RunID: "r", MessageID: messageID, Name: "file_list", Input: json.RawMessage(`{}`), Status: session.ToolCallPending}
		event := session.EventRecord{ID: session.EventID(fmt.Sprintf("event-%d", i)), Kind: agentruntime.EventToolCallUpdated, SessionID: "s", RunID: "r", MessageID: messageID, ToolCallID: id, ToolTransition: session.ToolTransitionPending}
		if update := a.accept(context.Background(), eventToolStore{call: call}, event, "s", "r"); !update.changed || a.resync {
			t.Fatalf("call %d update=%#v resync=%v", i, update, a.resync)
		}
	}
	call := session.ToolCall{ID: "overflow", SessionID: "s", RunID: "r", MessageID: "overflow-message", Name: "file_list", Input: json.RawMessage(`{}`), Status: session.ToolCallPending}
	event := session.EventRecord{ID: "overflow-event", Kind: agentruntime.EventToolCallUpdated, SessionID: "s", RunID: "r", MessageID: "overflow-message", ToolCallID: "overflow", ToolTransition: session.ToolTransitionPending}
	before := a.budget
	a.accept(context.Background(), eventToolStore{call: call}, event, "s", "r")
	if !a.resync || a.budget != before || len(a.messages) != MaxLiveToolActivities || len(a.toolIndex) != MaxLiveToolActivities {
		t.Fatalf("resync=%v count=%d", a.resync, a.budget.toolCount)
	}
}

func TestEventAccumulatorTextOverflowPreservesSeedAndLiveState(t *testing.T) {
	for _, messageID := range []session.MessageID{"live", "new-message"} {
		t.Run(string(messageID), func(t *testing.T) {
			a := newEventAccumulator([]Message{{ID: "seed", Role: RoleAssistant, Content: strings.Repeat("s", textsafe.MaxDisplayBytes-1)}})
			event := session.EventRecord{Kind: agentruntime.EventMessageDelta, SessionID: "s", RunID: "r", MessageID: "live", Payload: []byte(`{"content":"x"}`)}
			if update := a.accept(context.Background(), nil, event, "s", "r"); !update.changed || a.resync {
				t.Fatal("text at the aggregate limit was rejected")
			}
			before := a.budget
			event.MessageID = messageID
			event.Payload = []byte(`{"content":"y"}`)
			if update := a.accept(context.Background(), nil, event, "s", "r"); update.changed || !a.resync {
				t.Fatal("text over the aggregate limit was accepted")
			}
			if a.budget != before || len(a.messages) != 2 || a.messages[1].Content != "x" || len(a.rawByMessage) != 1 || a.rawByMessage["live"] != "x" {
				t.Fatal("rejected text partially mutated live state")
			}
		})
	}
}

func TestEventAccumulatorDisplayByteCapsAreExact(t *testing.T) {
	toolBase := ToolActivity{ID: "call", Name: "x", Status: ToolPending}
	toolBase.Subject = strings.Repeat("s", MaxLiveToolDisplayBytes-len(toolBase.Name)-len(toolBase.Status))
	toolSeed := []Message{{ID: "message", Role: RoleAssistant, Tools: []ToolActivity{toolBase}}}
	if atCap := newEventAccumulator(toolSeed); atCap.resync || atCap.budget.toolBytes != MaxLiveToolDisplayBytes {
		t.Fatalf("tool bytes at cap: bytes=%d resync=%v", atCap.budget.toolBytes, atCap.resync)
	}
	toolSeed[0].Tools[0].Subject += "x"
	if overCap := newEventAccumulator(toolSeed); !overCap.resync {
		t.Fatal("tool bytes over cap did not request resync")
	}

	textSeed := []Message{{ID: "message", Role: RoleAssistant, Content: strings.Repeat("t", textsafe.MaxDisplayBytes)}}
	if atCap := newEventAccumulator(textSeed); atCap.resync || atCap.budget.textBytes != textsafe.MaxDisplayBytes {
		t.Fatalf("text bytes at cap: bytes=%d resync=%v", atCap.budget.textBytes, atCap.resync)
	}
	textSeed[0].Content += "x"
	if overCap := newEventAccumulator(textSeed); !overCap.resync {
		t.Fatal("text bytes over cap did not request resync")
	}
}

func TestEventAccumulatorBoundsToolLookup(t *testing.T) {
	a := newEventAccumulator(nil)
	event := session.EventRecord{ID: "event", Kind: agentruntime.EventToolCallUpdated, SessionID: "s", RunID: "r", MessageID: "m", ToolCallID: "call", ToolTransition: session.ToolTransitionPending}
	started := time.Now()
	a.accept(context.Background(), blockingEventToolStore{}, event, "s", "r")
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond || !a.resync {
		t.Fatalf("lookup elapsed=%v resync=%v", elapsed, a.resync)
	}
}
