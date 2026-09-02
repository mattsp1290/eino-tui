package runtimeui

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-agent/store/sqlite"
	"github.com/mattsp1290/eino-tui/internal/platform"
)

func TestHistoryProjectionOmitsInterruptedEmptyAssistant(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	chat, err := Open(ctx, paths.Database, platform.WorkspaceSessionID(workspace), workspace)
	if err != nil {
		t.Fatal(err)
	}
	result, err := chat.Start(ctx, "retained user")
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
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
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
	for index := 0; index < 105; index++ {
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
	if len(messages) != 105 {
		t.Fatalf("messages=%d", len(messages))
	}
	for index, message := range messages {
		want := fmt.Sprintf("visible-%03d", index)
		if message.Content != want {
			t.Fatalf("message %d=%q", index, message.Content)
		}
	}
}
