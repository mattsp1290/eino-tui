package runtimeui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattsp1290/eino-agent/composition"
	"github.com/mattsp1290/eino-agent/config"
	"github.com/mattsp1290/eino-agent/model"
	agentruntime "github.com/mattsp1290/eino-agent/runtime"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-agent/store/sqlite"
	"github.com/mattsp1290/eino-agent/stream"
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

type countingTail struct {
	tail   *stream.Tail
	active atomic.Int64
}

func (t *countingTail) Subscribe(ctx context.Context, id session.ID) (<-chan session.EventRecord, error) {
	events, err := t.tail.Subscribe(ctx, id)
	if err != nil {
		return nil, err
	}
	t.active.Add(1)
	go func() { <-ctx.Done(); t.active.Add(-1) }()
	return events, nil
}
func (t *countingTail) Emit(ctx context.Context, event session.EventRecord) { t.tail.Emit(ctx, event) }
func (t *countingTail) Close()                                              { t.tail.Close() }

func TestSequentialTurnsCancelEveryTailSubscription(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	tail := &countingTail{tail: stream.NewTail(64)}
	plans, err := composition.NewRegistry(nil)
	if err != nil {
		t.Fatal(err)
	}
	selection := model.Selection{ProviderID: demomodel.ProviderID, ModelID: demomodel.ModelID}
	snapshot := config.Snapshot{Agent: config.Agent{Name: "demo", Model: selection}, Model: selection, Metadata: map[string]string{"workspace_root": workspace}}
	orchestrator, err := agentruntime.NewStreamingOrchestrator(agentruntime.WithStore(store), agentruntime.WithModelResolver(demomodel.Resolver(func(context.Context) error { return nil })), agentruntime.WithEventSink(tail), agentruntime.WithIDGenerator(platform.IDs{}), agentruntime.WithRunPlanProvider(plans), agentruntime.WithOwnerID("subscriber-test"), agentruntime.WithQueueSize(16), agentruntime.WithLease(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	chat := newService(ctx, store, tail, orchestrator, platform.WorkspaceSessionID(workspace), snapshot)
	for index := 0; index < 10; index++ {
		result, err := chat.Start(ctx, "turn")
		if err != nil {
			t.Fatal(err)
		}
		<-result.Run.Finished()
		deadline := time.Now().Add(time.Second)
		for tail.active.Load() != 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if got := tail.active.Load(); got != 0 {
			t.Fatalf("turn %d active subscribers=%d", index, got)
		}
	}
	deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := chat.Close(deadline); err != nil {
		t.Fatal(err)
	}
}

func TestPumpPanicBeforeAndAfterResultCannotStrandDurableRun(t *testing.T) {
	for _, stage := range []string{"before-result", "after-result"} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			workspace := t.TempDir()
			paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			opened, err := Open(ctx, paths.Database, platform.WorkspaceSessionID(workspace), workspace)
			if err != nil {
				t.Fatal(err)
			}
			chat := opened.(*service)
			chat.pumpHook = func(current string) {
				if current == stage {
					panic("secret prompt /tmp/private")
				}
			}
			result, err := chat.Start(ctx, "pump panic user")
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-result.Run.Finished():
			case <-time.After(2 * time.Second):
				t.Fatal("pump panic stranded run")
			}
			snapshot, err := chat.Load(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Messages) == 0 || snapshot.Messages[0].Content != "pump panic user" {
				t.Fatalf("history=%#v", snapshot.Messages)
			}
			if strings.Contains(snapshot.Notice, "secret") || strings.Contains(snapshot.Notice, "/tmp/private") {
				t.Fatalf("panic leaked: %q", snapshot.Notice)
			}
			deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			if err := chat.Close(deadline); err != nil {
				t.Fatal(err)
			}
		})
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

func TestInterruptBeforeAdmissionRetainsNoDurableTurn(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := Open(ctx, paths.Database, platform.WorkspaceSessionID(workspace), workspace)
	if err != nil {
		t.Fatal(err)
	}
	chat := opened.(*service)
	entered := make(chan struct{})
	release := make(chan struct{})
	chat.startHook = func(stage string) {
		if stage == "subscribed" {
			close(entered)
			<-release
		}
	}
	resultCh := make(chan error, 1)
	go func() { _, err := chat.Start(ctx, "unsent draft"); resultCh <- err }()
	<-entered
	if err := chat.InterruptActive(ctx); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-resultCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("start error=%v", err)
	}
	snapshot, err := chat.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 0 || snapshot.Phase != PhaseIdle {
		t.Fatalf("durable state=%#v", snapshot)
	}
	deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := chat.Close(deadline); err != nil {
		t.Fatal(err)
	}
}

func TestPostAdmissionProjectionFailureStillReturnsAdmittedRunAndReconciles(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := Open(ctx, paths.Database, platform.WorkspaceSessionID(workspace), workspace)
	if err != nil {
		t.Fatal(err)
	}
	chat := opened.(*service)
	var calls atomic.Int64
	chat.historyLoader = func(ctx context.Context, store session.Store, id session.ID) ([]Message, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("secret /tmp/private")
		}
		return loadHistory(ctx, store, id)
	}
	result, err := chat.Start(ctx, "durably admitted once")
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != ActionStarted || result.Run == nil || !result.Snapshot.Resync || len(result.Snapshot.Messages) != 1 || result.Snapshot.Messages[0].Content != "durably admitted once" {
		t.Fatalf("initial=%#v", result)
	}
	<-result.Run.Finished()
	var terminal Snapshot
	for {
		snapshot, ok := result.Run.Next(ctx)
		if !ok {
			break
		}
		if snapshot.Terminal {
			terminal = snapshot
		}
	}
	if len(terminal.Messages) != 2 || terminal.Messages[0].Content != "durably admitted once" {
		t.Fatalf("terminal=%#v", terminal)
	}
	deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := chat.Close(deadline); err != nil {
		t.Fatal(err)
	}
}
