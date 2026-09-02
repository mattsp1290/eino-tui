package runtimeui

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mattsp1290/eino-tui/internal/platform"
)

func TestServiceStreamsPersistsAndReplays(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	sessionID := platform.WorkspaceSessionID(root)
	service, err := Open(ctx, paths.Database, sessionID, root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := service.Load(ctx)
	if err != nil || len(loaded.Messages) != 0 {
		t.Fatalf("initial load = %#v, %v", loaded, err)
	}
	result, err := service.Start(ctx, "hello λ\nsecond line")
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != ActionStarted || result.Run == nil || len(result.Snapshot.Messages) != 1 || result.Snapshot.Messages[0].Role != RoleUser {
		t.Fatalf("start = %#v", result)
	}
	terminal, provisional := drainRun(t, result.Run)
	if provisional < 2 {
		t.Fatalf("provisional updates = %d", provisional)
	}
	if len(terminal.Messages) != 2 || terminal.Messages[0].Content != "hello λ\nsecond line" {
		t.Fatalf("terminal = %#v", terminal)
	}
	closeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := service.Close(closeCtx); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(ctx, paths.Database, sessionID, root)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := reopened.Load(ctx)
	if err != nil || len(replay.Messages) != 2 {
		t.Fatalf("replay = %#v, %v", replay, err)
	}
	if err := reopened.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}

func TestServiceInterruptKeepsAdmittedUserAndOmitsEmptyAssistant(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := Open(ctx, paths.Database, platform.WorkspaceSessionID(root), root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Start(ctx, "interrupt me")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.InterruptActive(ctx); err != nil {
		t.Fatal(err)
	}
	terminal, _ := drainRun(t, result.Run)
	if terminal.Notice != NoticeInterrupted {
		t.Fatalf("notice = %q", terminal.Notice)
	}
	if len(terminal.Messages) != 1 || terminal.Messages[0].Role != RoleUser || terminal.Messages[0].Status != StatusInterrupted {
		t.Fatalf("messages = %#v", terminal.Messages)
	}
	closeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := service.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}

func drainRun(t *testing.T, run Run) (Snapshot, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var terminal Snapshot
	provisional := 0
	for {
		snapshot, ok := run.Next(ctx)
		if !ok {
			if ctx.Err() != nil {
				t.Fatal(ctx.Err())
			}
			break
		}
		if snapshot.Terminal {
			terminal = snapshot
		} else {
			provisional++
		}
	}
	if !terminal.Terminal {
		t.Fatal("terminal snapshot missing")
	}
	return terminal, provisional
}
