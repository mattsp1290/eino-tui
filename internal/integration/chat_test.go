package integration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mattsp1290/eino-tui/internal/demomodel"
	"github.com/mattsp1290/eino-tui/internal/platform"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
)

func TestProductionWiringDurableJourney(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	id := platform.WorkspaceSessionID(workspace)
	service, err := runtimeui.Open(ctx, paths.Database, id, workspace)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Start(ctx, "Unicode λ\nmultiline")
	if err != nil {
		t.Fatal(err)
	}
	var terminal runtimeui.Snapshot
	updates := 0
	deadline, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for {
		snapshot, ok := result.Run.Next(deadline)
		if !ok {
			break
		}
		if snapshot.Terminal {
			terminal = snapshot
		} else {
			updates++
		}
	}
	if updates < 2 || len(terminal.Messages) != 2 {
		t.Fatalf("updates=%d terminal=%#v", updates, terminal)
	}
	closeCtx, closeCancel := context.WithTimeout(ctx, 2*time.Second)
	defer closeCancel()
	if err := service.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	reopened, err := runtimeui.Open(ctx, paths.Database, id, workspace)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := reopened.Load(ctx)
	if err != nil || len(replay.Messages) != 2 {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	if err := reopened.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}

func TestBackpressuredConsumerReceivesAuthoritativeTerminalReplay(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	chunks := make([]string, 100)
	for index := range chunks {
		chunks[index] = "x"
	}
	release := make(chan struct{})
	waiter := func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil
		}
	}
	service, err := runtimeui.OpenWithResolver(ctx, paths.Database, platform.WorkspaceSessionID(workspace), workspace, demomodel.ScriptedResolver(waiter, chunks))
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Start(ctx, "backpressure")
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-result.Run.Finished():
	case <-time.After(3 * time.Second):
		t.Fatal("run did not settle without consumer")
	}
	var terminal runtimeui.Snapshot
	for {
		snapshot, ok := result.Run.Next(ctx)
		if !ok {
			break
		}
		if snapshot.Terminal {
			terminal = snapshot
		}
	}
	replay, err := service.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !terminal.Terminal || !terminal.Resync || len(terminal.Messages) != len(replay.Messages) || terminal.Messages[1].Content != replay.Messages[1].Content {
		t.Fatalf("terminal=%#v replay=%#v", terminal, replay)
	}
	deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := service.Close(deadline); err != nil {
		t.Fatal(err)
	}
}

func TestLiveLeaseContentionWaitsWithoutStealingThenRecoversTerminalState(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	id := platform.WorkspaceSessionID(workspace)
	owner, err := runtimeui.OpenWithWait(ctx, paths.Database, id, workspace, demomodel.TimerWait(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	active, err := owner.Start(ctx, "owned elsewhere")
	if err != nil {
		t.Fatal(err)
	}
	contender, err := runtimeui.Open(ctx, paths.Database, id, workspace)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := contender.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Phase != runtimeui.PhaseRecoveryWaiting || loaded.RecoveryAt.IsZero() {
		t.Fatalf("load=%#v", loaded)
	}
	waiting, err := contender.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Kind != runtimeui.ActionRecoveryWaiting || waiting.Run != nil {
		t.Fatalf("recover contention=%#v", waiting)
	}
	closeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := owner.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	<-active.Run.Finished()
	recovered, err := contender.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Kind != runtimeui.ActionStarted || recovered.Run == nil {
		t.Fatalf("terminal recover=%#v", recovered)
	}
	select {
	case <-recovered.Run.Finished():
	case <-time.After(2 * time.Second):
		t.Fatal("terminal recovery did not finish")
	}
	if err := contender.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}
