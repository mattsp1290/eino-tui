package runtimeui

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/platform"
)

type replayOnlyStore struct {
	session.Store
	batch session.ReplayBatch
	calls map[session.ToolCallID]session.ToolCall
}

func (s replayOnlyStore) ListMessages(context.Context, session.ID, session.ReplayCursor) (session.ReplayBatch, error) {
	return s.batch, nil
}

func (s replayOnlyStore) GetToolCall(_ context.Context, id session.ToolCallID) (session.ToolCall, error) {
	call, ok := s.calls[id]
	if !ok {
		return session.ToolCall{}, session.ErrNotFound
	}
	return call, nil
}

func TestHistoryCandidateToolCapIsExactAndKeepsGroupsWhole(t *testing.T) {
	message := session.Message{ID: "tool-heavy", SessionID: "session", Role: session.RoleAssistant}
	parts := make([]session.Part, MaxTranscriptToolActivities+1)
	for i := range parts {
		parts[i] = session.Part{ID: session.PartID(fmt.Sprintf("part-%d", i)), MessageID: message.ID, Kind: session.PartToolCall, Ordinal: int64(i)}
	}
	store := replayOnlyStore{batch: session.ReplayBatch{Messages: []session.Message{message}, Parts: parts[:MaxTranscriptToolActivities]}}
	candidates, omitted, err := loadRecentCandidates(context.Background(), store, message.SessionID)
	if err != nil || omitted || len(candidates) != 1 || candidates[0].calls != MaxTranscriptToolActivities {
		t.Fatalf("at cap: candidates=%d omitted=%v err=%v", len(candidates), omitted, err)
	}
	store.batch.Parts = parts
	candidates, omitted, err = loadRecentCandidates(context.Background(), store, message.SessionID)
	if err != nil || !omitted || len(candidates) != 0 {
		t.Fatalf("over cap: candidates=%d omitted=%v err=%v", len(candidates), omitted, err)
	}
}

func TestHistoryToolProjectionUsesPartOrderAndIgnoresUnavailableRecords(t *testing.T) {
	message := session.Message{ID: "request", SessionID: "session", Role: session.RoleAssistant}
	part := func(id session.PartID, ordinal int64, payload string) session.Part {
		return session.Part{ID: id, MessageID: message.ID, SessionID: message.SessionID, Kind: session.PartToolCall, Ordinal: ordinal, Payload: json.RawMessage(payload)}
	}
	store := replayOnlyStore{
		batch: session.ReplayBatch{Messages: []session.Message{message}, Parts: []session.Part{
			part("two-part", 2, `{"id":"two","name":"file_read","arguments":{"path":"two.txt"}}`),
			part("malformed", 1, `{not-json`),
			part("one-part", 0, `{"id":"one","name":"file_read","arguments":{"path":"one.txt"}}`),
			part("missing-part", 3, `{"id":"missing","name":"file_read","arguments":{"path":"missing.txt"}}`),
			part("mismatch-part", 4, `{"id":"mismatch","name":"file_read","arguments":{"path":"private.txt"}}`),
		}},
		calls: map[session.ToolCallID]session.ToolCall{
			"one":      {ID: "one", SessionID: message.SessionID, MessageID: message.ID, Name: "file_read", Input: json.RawMessage(`{"path":"one.txt"}`), Status: session.ToolCallPending, Output: json.RawMessage(`"SECRET_OUTPUT"`), Error: "SECRET_ERROR /private/path"},
			"two":      {ID: "two", SessionID: message.SessionID, MessageID: message.ID, Name: "file_read", Input: json.RawMessage(`{"path":"two.txt"}`), Status: session.ToolCallCompleted},
			"mismatch": {ID: "mismatch", SessionID: message.SessionID, MessageID: "other-message", Name: "file_read", Input: json.RawMessage(`{"path":"private.txt"}`), Status: session.ToolCallFailed},
		},
	}
	messages, err := loadHistory(context.Background(), store, message.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Content != "" || len(messages[0].Tools) != 2 || messages[0].Tools[0].ID != "one" || messages[0].Tools[1].ID != "two" {
		t.Fatalf("projection=%#v", messages)
	}
	visible := fmt.Sprintf("%#v", messages)
	for _, forbidden := range []string{"SECRET_OUTPUT", "SECRET_ERROR", "/private/path", "private.txt", "missing.txt", "not-json"} {
		if strings.Contains(visible, forbidden) {
			t.Fatalf("unavailable/private record leaked %q: %s", forbidden, visible)
		}
	}
}

func TestHistoryProjectsAllDurableToolStatusesWithoutResultOrError(t *testing.T) {
	ctx := context.Background()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	store := openTestStore(t, ctx, paths.Database)
	defer store.Close()
	now := time.Now().UTC()
	sessionID := session.ID("tool-history")
	if _, err := store.CreateSession(ctx, session.Session{ID: sessionID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	run, err := store.AdmitRun(ctx, session.Run{ID: "tool-run", SessionID: sessionID, OwnerID: "owner", ClaimToken: "run-claim", Status: session.RunPending, CreatedAt: now}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	execution := store.Execution(session.RunFence{RunID: run.ID, ClaimToken: run.ClaimToken})
	message := session.Message{ID: "tool-message", SessionID: sessionID, RunID: run.ID, Role: session.RoleAssistant, CreatedAt: now, UpdatedAt: now}
	if _, err := execution.AppendMessage(ctx, message); err != nil {
		t.Fatal(err)
	}
	statuses := []session.ToolCallStatus{session.ToolCallPending, session.ToolCallRunning, session.ToolCallCompleted, session.ToolCallFailed, session.ToolCallInterrupted}
	for i, status := range statuses {
		id := session.ToolCallID(fmt.Sprintf("call-%d", i))
		requestPartID := session.PartID(fmt.Sprintf("request-%d", i))
		resultMessageID := session.MessageID(fmt.Sprintf("result-message-%d", i))
		resultPartID := session.PartID(fmt.Sprintf("result-part-%d", i))
		input := json.RawMessage(fmt.Sprintf(`{"path":"fixture-%d.txt"}`, i))
		call := session.ToolCall{
			ID: id, SessionID: sessionID, RunID: run.ID, MessageID: message.ID,
			RequestPartID: requestPartID, ResultMessageID: resultMessageID, ResultPartID: resultPartID,
			Name: "file_read", Input: input, Status: session.ToolCallPending,
		}
		payload, _ := json.Marshal(map[string]any{"id": id, "name": call.Name, "arguments": input})
		createdAt := now.Add(time.Duration(i+1) * time.Nanosecond)
		_, err := execution.CreateToolCall(ctx, session.CreateToolCallRequest{
			Call:        call,
			RequestPart: session.Part{ID: requestPartID, MessageID: message.ID, SessionID: sessionID, RunID: run.ID, Kind: session.PartToolCall, Ordinal: int64(i), Payload: payload, CreatedAt: createdAt, UpdatedAt: createdAt},
			Event:       session.ToolTransitionEvent{ID: session.EventID(fmt.Sprintf("pending-%d", i)), CreatedAt: createdAt},
		})
		if err != nil {
			t.Fatal(err)
		}
		if status == session.ToolCallPending {
			continue
		}
		startedAt := createdAt.Add(time.Nanosecond)
		claimed, err := execution.ClaimToolCall(ctx, session.ClaimToolCallRequest{
			ID: id, ClaimedBy: "worker", ClaimToken: fmt.Sprintf("tool-claim-%d", i), StartedAt: startedAt,
			LeaseDuration: time.Minute, Event: session.ToolTransitionEvent{ID: session.EventID(fmt.Sprintf("running-%d", i)), CreatedAt: startedAt},
		})
		if err != nil {
			t.Fatal(err)
		}
		if status == session.ToolCallRunning {
			continue
		}
		completedAt := startedAt.Add(time.Nanosecond)
		output := json.RawMessage(fmt.Sprintf(`{"tool_call_id":%q,"content":"SECRET_OUTPUT_%d"}`, id, i))
		settlement := session.ToolSettlement{
			ID: id, ClaimedBy: claimed.Call.ClaimedBy, ClaimToken: claimed.Call.ClaimToken, Status: status,
			Output: output, Error: fmt.Sprintf("SECRET_ERROR_%d /private/path", i), CompletedAt: completedAt,
			ResultMessage: session.Message{ID: resultMessageID, SessionID: sessionID, RunID: run.ID, ParentID: message.ID, Role: session.RoleTool, CreatedAt: completedAt, UpdatedAt: completedAt},
			ResultPart:    session.Part{ID: resultPartID, MessageID: resultMessageID, SessionID: sessionID, RunID: run.ID, Kind: session.PartToolResult, Payload: output, CreatedAt: completedAt, UpdatedAt: completedAt},
		}
		if _, err := execution.SettleToolCall(ctx, session.SettleToolCallRequest{Settlement: settlement, Event: session.ToolTransitionEvent{ID: session.EventID(fmt.Sprintf("terminal-%d", i)), CreatedAt: completedAt}}); err != nil {
			t.Fatal(err)
		}
	}
	messages, err := loadHistory(ctx, store, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].ID != string(message.ID) || len(messages[0].Tools) != len(statuses) {
		t.Fatalf("messages=%#v", messages)
	}
	want := []ToolStatus{ToolPending, ToolRunning, ToolCompleted, ToolFailed, ToolInterrupted}
	for i, activity := range messages[0].Tools {
		if activity.Status != want[i] || activity.Subject != fmt.Sprintf("fixture-%d.txt", i) {
			t.Fatalf("activity %d=%#v", i, activity)
		}
	}
	visible := fmt.Sprintf("%#v", messages)
	if strings.Contains(visible, "SECRET_OUTPUT") || strings.Contains(visible, "SECRET_ERROR") || strings.Contains(visible, "/private/path") {
		t.Fatal("tool output or error entered history projection")
	}
}

func TestHistoryProjectionOmitsInterruptedEmptyAssistant(t *testing.T) {
	ctx := context.Background()
	paths, workspace := fixtureWorkspace(t)
	chat := openResolvedFixture(t, ctx, paths, workspace)
	result, err := chat.Start(ctx, "retained user", fixtureStartConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := chat.InterruptActive(ctx); err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	<-result.Run.Finished()
	snapshot, err := chat.Load(wait)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 1 || snapshot.Messages[0].Role != RoleUser || snapshot.Messages[0].Status != StatusInterrupted {
		durable, _ := chat.(*service).store.GetRun(ctx, result.Run.ID())
		t.Fatalf("history=%#v durable_status=%s durable_error=%q", snapshot.Messages, durable.Status, durable.Error)
	}
	if err := chat.Close(wait); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryProjectionPagesAndExcludesReasoningAndState(t *testing.T) {
	ctx := context.Background()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	store := openTestStore(t, ctx, paths.Database)
	defer store.Close()
	now := time.Now().UTC()
	sessionID := session.ID("session-history")
	if _, err := store.CreateSession(ctx, session.Session{ID: sessionID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	run, err := store.AdmitRun(ctx, session.Run{ID: "run-history", SessionID: sessionID, OwnerID: "owner", ClaimToken: "claim", Status: session.RunPending, CreatedAt: now}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	execution := store.Execution(session.RunFence{RunID: run.ID, ClaimToken: run.ClaimToken})
	for index := 0; index < MaxTranscriptCandidates+5; index++ {
		id := session.MessageID(fmt.Sprintf("message-%03d", index))
		role := session.RoleUser
		if index%2 == 1 {
			role = session.RoleAssistant
		}
		at := now.Add(time.Duration(index) * time.Nanosecond)
		if _, err := execution.AppendMessage(ctx, session.Message{ID: id, SessionID: sessionID, RunID: run.ID, Role: role, CreatedAt: at, UpdatedAt: at}); err != nil {
			t.Fatal(err)
		}
		parts := []struct {
			kind session.PartKind
			text string
		}{{session.PartText, fmt.Sprintf("visible-%03d", index)}, {session.PartReasoning, "SECRET_REASONING"}, {session.PartState, "SECRET_STATE"}}
		for ordinal, part := range parts {
			payload, _ := json.Marshal(map[string]string{"text": part.text})
			if _, err := execution.AppendPart(ctx, session.Part{ID: session.PartID(fmt.Sprintf("part-%03d-%d", index, ordinal)), MessageID: id, SessionID: sessionID, RunID: run.ID, Kind: part.kind, Ordinal: int64(ordinal), Payload: payload, CreatedAt: at, UpdatedAt: at}); err != nil {
				t.Fatal(err)
			}
		}
	}
	messages, err := loadHistory(ctx, store, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != MaxTranscriptCandidates+1 {
		t.Fatalf("messages=%d", len(messages))
	}
	if messages[0].Role != RoleNotice || messages[0].Content != NoticeHistoryOmitted {
		t.Fatalf("missing candidate-cap notice: %#v", messages[0])
	}
	for index, message := range messages[1:] {
		want := fmt.Sprintf("visible-%03d", index+5)
		if message.Content != want {
			t.Fatalf("message %d=%q", index, message.Content)
		}
	}
}

func TestTranscriptWindowMarksOmittedHistory(t *testing.T) {
	messages := []Message{
		{ID: "one", Role: RoleUser, Content: "1111"},
		{ID: "two", Role: RoleAssistant, Content: "2222"},
		{ID: "three", Role: RoleUser, Content: "3333"},
	}
	window := transcriptWindow{limit: 8}
	for _, message := range messages {
		window.Push(message)
	}
	bounded := window.Items()
	if len(bounded) != 3 || bounded[0].Role != RoleNotice || bounded[0].Content != NoticeHistoryOmitted {
		t.Fatalf("bounded=%#v", bounded)
	}
	if bounded[1].ID != "two" || bounded[2].ID != "three" {
		t.Fatalf("newest messages not retained: %#v", bounded)
	}
	window = transcriptWindow{limit: 12}
	for _, message := range messages {
		window.Push(message)
	}
	if got := window.Items(); len(got) != len(messages) || got[0].ID != "one" {
		t.Fatalf("unexpected truncation: %#v", got)
	}
}
