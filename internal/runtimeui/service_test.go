package runtimeui

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/mattsp1290/eino-tui/internal/demomodel"
	"github.com/mattsp1290/eino-tui/internal/platform"
)

func TestServiceSlowConsumerCannotBlockSettlementOrClose(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := Open(ctx, paths.Database, platform.WorkspaceSessionID(workspace), workspace)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Start(ctx, "slow consumer")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-result.Run.Finished():
	case <-time.After(2 * time.Second):
		t.Fatal("run did not settle without consumer")
	}
	deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := service.Close(deadline); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(deadline); err != nil {
		t.Fatal("idempotent close failed")
	}
	if _, err := service.Start(ctx, "after close"); !errors.Is(err, ErrClosing) {
		t.Fatalf("post-close start=%v", err)
	}
}

func TestServiceRejectsConcurrentStartAndSupportsSequentialTurns(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := Open(ctx, paths.Database, platform.WorkspaceSessionID(workspace), workspace)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Start(ctx, "first")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(ctx, "duplicate"); !errors.Is(err, ErrBusy) {
		t.Fatalf("duplicate start=%v", err)
	}
	<-first.Run.Finished()
	second, err := service.Start(ctx, "second")
	if err != nil {
		t.Fatal(err)
	}
	<-second.Run.Finished()
	snapshot, err := service.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 4 || snapshot.Messages[0].Content != "first" || snapshot.Messages[2].Content != "second" {
		t.Fatalf("history=%#v", snapshot.Messages)
	}
	deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := service.Close(deadline); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentCloseDuringActiveRunIsIdempotent(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := OpenWithWait(ctx, paths.Database, platform.WorkspaceSessionID(workspace), workspace, demomodel.TimerWait(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	result, err := opened.Start(ctx, "close while active")
	if err != nil {
		t.Fatal(err)
	}
	errorsCh := make(chan error, 2)
	for range 2 {
		go func() {
			deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			errorsCh <- opened.Close(deadline)
		}()
	}
	for range 2 {
		if err := <-errorsCh; err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-result.Run.Finished():
	default:
		t.Fatal("close returned before durable run finished")
	}
}
