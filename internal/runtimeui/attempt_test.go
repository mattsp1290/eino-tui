package runtimeui

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/mattsp1290/eino-agent/config"
	agentruntime "github.com/mattsp1290/eino-agent/runtime"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-agent/store/sqlite"
	"github.com/mattsp1290/eino-agent/stream"
	"github.com/mattsp1290/eino-tui/internal/platform"
)

type subscribeBarrierTail struct {
	tailer
	entered chan struct{}
	release chan struct{}
}

func (t *subscribeBarrierTail) Subscribe(ctx context.Context, id session.ID) (<-chan session.EventRecord, error) {
	close(t.entered)
	<-t.release
	return t.tailer.Subscribe(ctx, id)
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

func TestAttemptTransitionsAreIdentityCheckedAndMonotonic(t *testing.T) {
	runtime := orchestratorFunc{
		start: func(context.Context, agentruntime.Request) (agentruntime.Handle, error) {
			return nil, errors.New("unused")
		},
		resume: func(context.Context, session.RunID) (agentruntime.Handle, error) { return nil, errors.New("unused") },
	}
	chat := newOrchestratorTestService(t, runtime)

	aborted, err := chat.beginAttempt(context.Background(), stateIdle, stateStarting)
	if err != nil {
		t.Fatal(err)
	}
	chat.abortAttempt(aborted)
	chat.finishPending(aborted)
	if chat.state != stateIdle || chat.attempt != nil || aborted.ctx.Err() == nil {
		t.Fatalf("abort state=%v attempt=%v err=%v", chat.state, chat.attempt, aborted.ctx.Err())
	}

	committed, err := chat.beginAttempt(context.Background(), stateIdle, stateStarting)
	if err != nil {
		t.Fatal(err)
	}
	active := session.Run{ID: "active", SessionID: chat.sessionID, LeaseUntil: time.Now().Add(time.Minute)}
	result, err := chat.commitWaiting(committed, active, nil)
	chat.finishPending(committed)
	if err != nil || result.Kind != ActionRecoveryWaiting || chat.state != stateWaiting || chat.attempt != nil {
		t.Fatalf("commit result=%#v err=%v state=%v attempt=%v", result, err, chat.state, chat.attempt)
	}

	closing, err := chat.beginAttempt(context.Background(), stateWaiting, stateRecovering)
	if err != nil {
		t.Fatal(err)
	}
	chat.mu.Lock()
	chat.state = stateClosing
	chat.mu.Unlock()
	closing.cancel()
	if _, err := chat.commitWaiting(closing, active, nil); !errors.Is(err, ErrClosing) {
		t.Fatalf("closing commit error=%v", err)
	}
	chat.finishPending(closing)
	if chat.state != stateClosing {
		t.Fatalf("closing transition regressed to %v", chat.state)
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := chat.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}

func TestInterruptBeforeAdmissionRetainsNoDurableTurn(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	opened, err := openFixture(ctx, paths.Database, platform.WorkspaceSessionID(workspace), workspace)
	if err != nil {
		t.Fatal(err)
	}
	chat := opened.(*service)
	entered := make(chan struct{})
	release := make(chan struct{})
	chat.tail = &subscribeBarrierTail{tailer: chat.tail, entered: entered, release: release}
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
			if err := <-result; !errors.Is(err, context.Canceled) && !errors.Is(err, ErrClosing) {
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
	if err := <-result; !errors.Is(err, context.Canceled) && !errors.Is(err, ErrClosing) {
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
	chat.store = &blockingListStore{durableStore: chat.store, entered: waiting}
	startResult := make(chan error, 1)
	go func() { _, err := chat.Start(context.Background(), "contended"); startResult <- err }()
	<-waiting
	closeResult := make(chan error, 1)
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { closeResult <- chat.Close(closeCtx) }()
	if err := <-startResult; !errors.Is(err, ErrClosing) {
		t.Fatalf("start error=%v", err)
	}
	if err := <-closeResult; err != nil {
		t.Fatal(err)
	}
}

type blockingListStore struct {
	durableStore
	entered chan struct{}
}

func (s *blockingListStore) ListMessages(ctx context.Context, _ session.ID, _ session.ReplayCursor) (session.ReplayBatch, error) {
	close(s.entered)
	<-ctx.Done()
	return session.ReplayBatch{}, ctx.Err()
}
