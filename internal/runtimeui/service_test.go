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
	tail    *stream.Tail
	active  atomic.Int64
	drained chan struct{}
}

func (t *countingTail) Subscribe(ctx context.Context, id session.ID) (<-chan session.EventRecord, error) {
	events, err := t.tail.Subscribe(ctx, id)
	if err != nil {
		return nil, err
	}
	t.active.Add(1)
	go func() {
		<-ctx.Done()
		t.active.Add(-1)
		t.drained <- struct{}{}
	}()
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
	tail := &countingTail{tail: stream.NewTail(64), drained: make(chan struct{}, 1)}
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
		select {
		case <-tail.drained:
		case <-time.After(time.Second):
			t.Fatal("tail subscription was not canceled")
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

type orchestratorFunc struct {
	start  func(context.Context, agentruntime.Request) (agentruntime.Handle, error)
	resume func(context.Context, session.RunID) (agentruntime.Handle, error)
}

func (o orchestratorFunc) Start(ctx context.Context, request agentruntime.Request) (agentruntime.Handle, error) {
	return o.start(ctx, request)
}

func (o orchestratorFunc) Resume(ctx context.Context, runID session.RunID) (agentruntime.Handle, error) {
	return o.resume(ctx, runID)
}

func newOrchestratorTestService(t *testing.T, runtime orchestrator) *service {
	t.Helper()
	ctx := context.Background()
	store, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "service.db"))
	if err != nil {
		t.Fatal(err)
	}
	return newService(ctx, store, stream.NewTail(8), runtime, session.ID("service-test"), config.Snapshot{})
}

func TestPendingStartHonorsCallerInterruptAndCloseCancellation(t *testing.T) {
	for _, control := range []string{"caller", "interrupt", "close"} {
		t.Run(control, func(t *testing.T) {
			entered := make(chan struct{})
			runtime := orchestratorFunc{
				start: func(ctx context.Context, _ agentruntime.Request) (agentruntime.Handle, error) {
					close(entered)
					<-ctx.Done()
					return nil, ctx.Err()
				},
				resume: func(context.Context, session.RunID) (agentruntime.Handle, error) {
					return nil, errors.New("unexpected resume")
				},
			}
			chat := newOrchestratorTestService(t, runtime)
			callCtx, cancelCall := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() { _, err := chat.Start(callCtx, "blocked"); result <- err }()
			<-entered
			switch control {
			case "caller":
				cancelCall()
			case "interrupt":
				if err := chat.InterruptActive(context.Background()); err != nil {
					t.Fatal(err)
				}
			case "close":
				closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := chat.Close(closeCtx); err != nil {
					t.Fatal(err)
				}
			}
			if err := <-result; !errors.Is(err, context.Canceled) {
				t.Fatalf("start error=%v", err)
			}
			cancelCall()
			if control != "close" {
				closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := chat.Close(closeCtx); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestCloseCancelsPendingRecover(t *testing.T) {
	entered := make(chan struct{})
	runtime := orchestratorFunc{
		start: func(context.Context, agentruntime.Request) (agentruntime.Handle, error) {
			return nil, errors.New("unexpected start")
		},
		resume: func(ctx context.Context, _ session.RunID) (agentruntime.Handle, error) {
			close(entered)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	chat := newOrchestratorTestService(t, runtime)
	chat.state = stateWaiting
	chat.recoveryRun = session.Run{ID: "stale-run", SessionID: chat.sessionID, Status: session.RunRunning}
	result := make(chan error, 1)
	go func() { _, err := chat.Recover(context.Background()); result <- err }()
	<-entered
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := chat.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("recover error=%v", err)
	}
}

func TestRecoverResumesCurrentExpiredRun(t *testing.T) {
	resumed := make(chan session.RunID, 1)
	runtime := orchestratorFunc{
		start: func(context.Context, agentruntime.Request) (agentruntime.Handle, error) {
			return nil, errors.New("unexpected start")
		},
		resume: func(_ context.Context, id session.RunID) (agentruntime.Handle, error) {
			resumed <- id
			return nil, context.Canceled
		},
	}
	chat := newOrchestratorTestService(t, runtime)
	now := time.Now().UTC()
	if _, err := chat.store.CreateSession(context.Background(), session.Session{ID: chat.sessionID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	current, err := chat.store.AdmitRun(context.Background(), session.Run{ID: "current-run", SessionID: chat.sessionID, OwnerID: "owner", ClaimToken: "claim", Status: session.RunPending, CreatedAt: now}, time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(current.LeaseUntil) {
		time.Sleep(time.Microsecond)
	}
	chat.state = stateWaiting
	chat.recoveryRun = session.Run{ID: "stale-run", SessionID: chat.sessionID, Status: session.RunInterrupted}
	if _, err := chat.Recover(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("recover error=%v", err)
	}
	if got := <-resumed; got != current.ID {
		t.Fatalf("resumed %q, want %q", got, current.ID)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := chat.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}

func TestContentionCannotPublishWaitingAfterCloseStarts(t *testing.T) {
	runtime := orchestratorFunc{
		start: func(context.Context, agentruntime.Request) (agentruntime.Handle, error) {
			return nil, session.ErrSessionBusy
		},
		resume: func(context.Context, session.RunID) (agentruntime.Handle, error) {
			return nil, errors.New("unexpected resume")
		},
	}
	chat := newOrchestratorTestService(t, runtime)
	now := time.Now().UTC()
	if _, err := chat.store.CreateSession(context.Background(), session.Session{ID: chat.sessionID, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := chat.store.AdmitRun(context.Background(), session.Run{ID: "other-run", SessionID: chat.sessionID, OwnerID: "other", ClaimToken: "claim", Status: session.RunPending, CreatedAt: now}, time.Minute); err != nil {
		t.Fatal(err)
	}
	waiting := make(chan struct{})
	release := make(chan struct{})
	closing := make(chan struct{})
	chat.waitingHook = func(string) { close(waiting); <-release }
	chat.shutdownHook = func(string) { close(closing) }
	startResult := make(chan error, 1)
	go func() { _, err := chat.Start(context.Background(), "contended"); startResult <- err }()
	<-waiting
	closeResult := make(chan error, 1)
	go func() { closeResult <- chat.Close(context.Background()) }()
	<-closing
	close(release)
	if err := <-startResult; !errors.Is(err, ErrClosing) {
		t.Fatalf("start error=%v", err)
	}
	if err := <-closeResult; err != nil {
		t.Fatal(err)
	}
}
