package runtimeui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	codexauth "github.com/mattsp1290/codex-auth-go"
	"github.com/mattsp1290/eino-agent/composition"
	"github.com/mattsp1290/eino-agent/config"
	agentruntime "github.com/mattsp1290/eino-agent/runtime"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-agent/stream"
	"github.com/mattsp1290/eino-tui/internal/demomodel"
	"github.com/mattsp1290/eino-tui/internal/platform"
)

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
	paths, workspace := fixtureWorkspace(t)
	store := openTestStore(t, ctx, paths.Database)
	tail := &countingTail{tail: stream.NewTail(64), drained: make(chan struct{}, 1)}
	plans, err := composition.NewRegistry(nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := config.Snapshot{Agent: config.Agent{Name: "demo"}, Metadata: map[string]string{"workspace_id": workspace.ID, "workspace_root": workspace.Root}}
	orchestrator, err := agentruntime.NewStreamingOrchestrator(agentruntime.WithStore(store), agentruntime.WithModelResolver(demomodel.DynamicResolver(func(context.Context) error { return nil })), agentruntime.WithEventSink(tail), agentruntime.WithIDGenerator(platform.IDs{}), agentruntime.WithRunPlanProvider(plans), agentruntime.WithOwnerID("subscriber-test"), agentruntime.WithQueueSize(16), agentruntime.WithLease(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	chat := newService(ctx, store, tail, orchestrator, nil, platform.NewPreferenceStore(paths.Workspaces), workspace, snapshot)
	selectForTest(chat, "sequential-turns")
	now := time.Now().UTC()
	seed := session.Session{
		ID: chat.selected.id, WorkspaceID: workspace.ID, Directory: workspace.Root,
		Metadata: numberMetadata(chat.selected.number), CreatedAt: now, UpdatedAt: now,
	}
	if _, err := chat.store.CreateSession(ctx, seed); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 10; index++ {
		result, err := chat.Start(ctx, "turn", fixtureStartConfig())
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

type panicRunIDHandle struct {
	calls atomic.Int64
	done  chan agentruntime.Result
}

type settledHandle struct{ id session.RunID }

func (h settledHandle) RunID() session.RunID                  { return h.id }
func (settledHandle) Done() <-chan agentruntime.Result        { return nil }
func (settledHandle) Interrupt(context.Context, string) error { return nil }

func TestFinishPumpClassifiesOnlyStableProviderSentinels(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "plan wrapped", err: fmt.Errorf("sensitive TOKEN /tmp/path: %w", codexauth.ErrPlanNotIncluded), want: NoticePlanUnavailable},
		{name: "quota joined", err: errors.Join(errors.New("sensitive TOKEN /tmp/path"), codexauth.ErrQuotaExceeded), want: NoticeQuotaExceeded},
		{name: "generic", err: errors.New("usage_not_included TOKEN /tmp/path\n\x1b]0;leak\a"), want: NoticeProviderFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			chat := newOrchestratorTestService(t, orchestratorFunc{})
			now := time.Now().UTC()
			if _, err := chat.store.CreateSession(context.Background(), session.Session{ID: chat.selected.id, CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			run := newRun("classification-run")
			active := &activeRun{run: run, handle: settledHandle{id: "classification-run"}, conv: *chat.selected, cancelRun: func() {}, cancelTail: func() {}, interrupt: make(chan string, 1)}
			chat.active, chat.state = active, stateRunning
			chat.finishPump(active, Snapshot{}, agentruntime.Result{RunID: "classification-run", Status: session.RunFailed, Error: test.err}, 1, false)
			snapshot, ok := run.Next(context.Background())
			if !ok || snapshot.Notice != test.want {
				t.Fatalf("snapshot = %#v", snapshot)
			}
			visible := fmt.Sprintf("%#v", snapshot)
			if strings.Contains(visible, "TOKEN") || strings.Contains(visible, "/tmp/path") || strings.Contains(visible, "usage_not_included") {
				t.Fatalf("error leaked: %s", visible)
			}
			closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := chat.Close(closeCtx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestFinishPumpHistoryFailureTakesPrecedenceOverProviderClassification(t *testing.T) {
	chat := newOrchestratorTestService(t, orchestratorFunc{})
	failing := &failListStore{durableStore: chat.store}
	failing.remaining.Store(1)
	chat.store = failing
	run := newRun("precedence-run")
	active := &activeRun{run: run, handle: settledHandle{id: "precedence-run"}, cancelRun: func() {}, cancelTail: func() {}, interrupt: make(chan string, 1)}
	chat.active, chat.state = active, stateRunning
	initial := Snapshot{Messages: []Message{{Role: RoleUser, Content: "safe prompt"}}}
	chat.finishPump(active, initial, agentruntime.Result{RunID: "precedence-run", Status: session.RunFailed, Error: codexauth.ErrPlanNotIncluded}, 1, false)
	snapshot, ok := run.Next(context.Background())
	if !ok || snapshot.Notice != NoticeUnavailable || !snapshot.Resync {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := chat.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}

func (h *panicRunIDHandle) RunID() session.RunID {
	if h.calls.Add(1) == 2 {
		panic("secret prompt /tmp/private")
	}
	return "panic-run"
}
func (h *panicRunIDHandle) Done() <-chan agentruntime.Result { return h.done }
func (*panicRunIDHandle) Interrupt(context.Context, string) error {
	return nil
}

type panicListStore struct {
	durableStore
	panicAt int64
	calls   atomic.Int64
}

func (s *panicListStore) ListMessages(ctx context.Context, id session.ID, cursor session.ReplayCursor) (session.ReplayBatch, error) {
	if s.calls.Add(1) == s.panicAt {
		panic("secret prompt /tmp/private")
	}
	return s.durableStore.ListMessages(ctx, id, cursor)
}

func TestPumpPanicsCannotStrandRun(t *testing.T) {
	t.Run("before result", func(t *testing.T) {
		runtime := orchestratorFunc{
			start: func(context.Context, agentruntime.Request) (agentruntime.Handle, error) {
				return nil, errors.New("unused")
			},
			resume: func(context.Context, session.RunID) (agentruntime.Handle, error) { return nil, errors.New("unused") },
		}
		chat := newOrchestratorTestService(t, runtime)
		done := make(chan agentruntime.Result, 1)
		done <- agentruntime.Result{RunID: "panic-run", Status: session.RunFailed}
		close(done)
		handle := &panicRunIDHandle{done: done}
		run := newRun("panic-run")
		active := &activeRun{run: run, handle: handle, cancelRun: func() {}, cancelTail: func() {}, interrupt: make(chan string, 1)}
		chat.active = active
		chat.state = stateRunning
		events := make(chan session.EventRecord, 1)
		events <- session.EventRecord{}
		go chat.pump(active, events, Snapshot{RunID: "panic-run", Version: 1, Phase: PhaseRunning})
		select {
		case <-run.Finished():
		case <-time.After(time.Second):
			t.Fatal("pump panic stranded run")
		}
		closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := chat.Close(closeCtx); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("during terminal projection", func(t *testing.T) {
		ctx := context.Background()
		paths, workspace := fixtureWorkspace(t)
		opened := openResolvedFixture(t, ctx, paths, workspace)
		chat := opened.(*service)
		chat.store = &panicListStore{durableStore: chat.store, panicAt: 2}
		result, err := chat.Start(ctx, "pump panic user", fixtureStartConfig())
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-result.Run.Finished():
		case <-time.After(2 * time.Second):
			t.Fatal("pump panic stranded run")
		}
		deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := chat.Close(deadline); err != nil {
			t.Fatal(err)
		}
	})
}

func TestPostAdmissionProjectionFailureStillReturnsAdmittedRunAndReconciles(t *testing.T) {
	ctx := context.Background()
	paths, workspace := fixtureWorkspace(t)
	opened := openResolvedFixture(t, ctx, paths, workspace)
	chat := opened.(*service)
	failing := &failListStore{durableStore: chat.store}
	failing.remaining.Store(1)
	chat.store = failing
	result, err := chat.Start(ctx, "durably admitted once", fixtureStartConfig())
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

type failListStore struct {
	durableStore
	remaining atomic.Int64
}

func (s *failListStore) ListMessages(ctx context.Context, id session.ID, cursor session.ReplayCursor) (session.ReplayBatch, error) {
	if s.remaining.CompareAndSwap(1, 0) {
		return session.ReplayBatch{}, errors.New("secret /tmp/private")
	}
	return s.durableStore.ListMessages(ctx, id, cursor)
}
