package runtimeui

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mattsp1290/eino-agent/config"
	agentruntime "github.com/mattsp1290/eino-agent/runtime"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-agent/stream"
	"github.com/mattsp1290/eino-tui/internal/textsafe"
)

type durableStore interface {
	session.Store
	Close() error
}
type orchestrator interface {
	Start(context.Context, agentruntime.Request) (agentruntime.Handle, error)
	Resume(context.Context, session.RunID) (agentruntime.Handle, error)
}
type tailer interface {
	Subscribe(context.Context, session.ID) (<-chan session.EventRecord, error)
	Close()
}

type lifecycle uint8

const (
	stateIdle lifecycle = iota
	stateWaiting
	stateStarting
	stateRecovering
	stateRunning
	stateClosing
	stateClosed
)

type service struct {
	ctx       context.Context
	cancel    context.CancelFunc
	store     durableStore
	tail      tailer
	runtime   orchestrator
	sessionID session.ID
	config    config.Snapshot

	mu               sync.Mutex
	state            lifecycle
	active           *activeRun
	pendingCancel    context.CancelFunc
	pendingInterrupt bool
	pendingAdmission bool
	recoveryRun      session.Run
	pending          sync.WaitGroup
	shutdownOnce     sync.Once
	shutdownDone     chan struct{}
	shutdownErr      error
	pumpHook         func(string)
	startHook        func(string)
	historyLoader    func(context.Context, session.Store, session.ID) ([]Message, error)
}

type activeRun struct {
	run        *runStream
	handle     agentruntime.Handle
	cancelRun  context.CancelFunc
	cancelTail context.CancelFunc
	interrupt  chan string
}

func newService(ctx context.Context, store durableStore, tail tailer, runtime orchestrator, sessionID session.ID, snapshot config.Snapshot) *service {
	lifetime, cancel := context.WithCancel(context.WithoutCancel(ctx))
	return &service{ctx: lifetime, cancel: cancel, store: store, tail: tail, runtime: runtime, sessionID: sessionID, config: snapshot, state: stateIdle, shutdownDone: make(chan struct{}), historyLoader: loadHistory}
}

func (s *service) projectHistory(ctx context.Context) ([]Message, error) {
	return s.historyLoader(ctx, s.store, s.sessionID)
}

func (s *service) Load(ctx context.Context) (Snapshot, error) {
	s.mu.Lock()
	if s.state >= stateClosing {
		s.mu.Unlock()
		return Snapshot{}, ErrClosing
	}
	s.mu.Unlock()
	messages, err := s.projectHistory(ctx)
	if err != nil && !errors.Is(err, session.ErrNotFound) {
		return Snapshot{}, fmt.Errorf("%w", ErrUnavailable)
	}
	active, activeErr := s.store.ActiveRun(ctx, s.sessionID)
	if activeErr == nil && !active.Terminal() {
		s.mu.Lock()
		if s.state == stateIdle || s.state == stateWaiting {
			s.state = stateWaiting
			s.recoveryRun = active
		}
		s.mu.Unlock()
		return Snapshot{Messages: messages, Phase: PhaseRecoveryWaiting, Notice: NoticeRecoveryWaiting, RecoveryAt: active.LeaseUntil}, nil
	}
	if activeErr != nil && !errors.Is(activeErr, session.ErrNotFound) {
		return Snapshot{}, fmt.Errorf("%w", ErrUnavailable)
	}
	s.mu.Lock()
	if s.state == stateWaiting {
		s.state = stateIdle
		s.recoveryRun = session.Run{}
	}
	s.mu.Unlock()
	return Snapshot{Messages: messages, Phase: PhaseIdle}, nil
}

func (s *service) Start(ctx context.Context, prompt string) (ActionResult, error) {
	normalized, err := textsafe.Prompt(prompt)
	if err != nil {
		return ActionResult{}, fmt.Errorf("%w", ErrInvalidPrompt)
	}
	s.mu.Lock()
	if s.state >= stateClosing {
		s.mu.Unlock()
		return ActionResult{}, ErrClosing
	}
	if s.state != stateIdle {
		s.mu.Unlock()
		return ActionResult{}, ErrBusy
	}
	s.state = stateStarting
	runCtx, runCancel := context.WithCancel(s.ctx)
	s.pendingCancel = runCancel
	s.pending.Add(1)
	s.mu.Unlock()
	defer s.pending.Done()

	tailCtx, tailCancel := context.WithCancel(s.ctx)
	events, err := s.tail.Subscribe(tailCtx, s.sessionID)
	if err != nil {
		tailCancel()
		runCancel()
		s.resetPending()
		return ActionResult{}, fmt.Errorf("%w", ErrUnavailable)
	}
	if s.startHook != nil {
		s.startHook("subscribed")
	}
	s.mu.Lock()
	if s.state >= stateClosing {
		s.pendingCancel = nil
		s.pendingInterrupt = false
		s.pendingAdmission = false
		s.mu.Unlock()
		tailCancel()
		runCancel()
		return ActionResult{}, ErrClosing
	}
	if s.pendingInterrupt {
		s.pendingCancel = nil
		s.pendingInterrupt = false
		s.pendingAdmission = false
		s.state = stateIdle
		s.mu.Unlock()
		tailCancel()
		runCancel()
		return ActionResult{}, context.Canceled
	}
	s.pendingAdmission = true
	s.mu.Unlock()
	handle, err := s.runtime.Start(runCtx, agentruntime.Request{SessionID: s.sessionID, Message: agentruntime.UserMessage{Content: normalized}, Config: s.config})
	if err != nil {
		tailCancel()
		runCancel()
		if errors.Is(err, session.ErrSessionBusy) {
			return s.toWaiting(ctx)
		}
		s.resetPending()
		if errors.Is(err, context.Canceled) {
			return ActionResult{}, context.Canceled
		}
		return ActionResult{}, fmt.Errorf("%w", ErrUnavailable)
	}
	return s.publishAdmitted(ctx, handle, events, runCancel, tailCancel, normalized)
}

func (s *service) publishAdmitted(ctx context.Context, handle agentruntime.Handle, events <-chan session.EventRecord, runCancel, tailCancel context.CancelFunc, prompt string) (ActionResult, error) {
	s.waitForRunReady(handle.RunID())
	messages, err := s.projectHistory(ctx)
	resync := err != nil
	if resync {
		messages = []Message{{Role: RoleUser, Content: textsafe.Display(prompt), Status: StatusComplete}}
	}
	run := newRun(handle.RunID())
	initial := Snapshot{RunID: handle.RunID(), Version: 1, Messages: messages, Phase: PhaseRunning, Resync: resync}
	active := &activeRun{run: run, handle: handle, cancelRun: runCancel, cancelTail: tailCancel, interrupt: make(chan string, 1)}
	s.mu.Lock()
	closing := s.state >= stateClosing
	interruptRequested := s.pendingInterrupt
	s.pendingCancel = nil
	s.pendingInterrupt = false
	s.pendingAdmission = false
	s.active = active
	if !closing {
		s.state = stateRunning
	}
	s.mu.Unlock()
	go s.pump(active, events, initial)
	if closing || interruptRequested {
		reason := "user interrupt"
		if closing {
			reason = "application closing"
		}
		active.requestInterrupt(reason)
	}
	return ActionResult{Kind: ActionStarted, Run: run, Snapshot: initial}, nil
}

func (s *service) waitForRunReady(runID session.RunID) {
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		durable, err := s.store.GetRun(context.Background(), runID)
		if err == nil && durable.Status != session.RunPending {
			return
		}
		select {
		case <-deadline.C:
			return
		case <-ticker.C:
		}
	}
}

func (s *service) resetPending() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingCancel = nil
	s.pendingInterrupt = false
	s.pendingAdmission = false
	if s.state == stateStarting || s.state == stateRecovering {
		s.state = stateIdle
	}
}

func (s *service) toWaiting(ctx context.Context) (ActionResult, error) {
	active, err := s.store.ActiveRun(ctx, s.sessionID)
	if err != nil {
		s.resetPending()
		return ActionResult{}, fmt.Errorf("%w", ErrUnavailable)
	}
	messages, _ := s.projectHistory(ctx)
	s.mu.Lock()
	s.pendingCancel = nil
	s.pendingInterrupt = false
	s.pendingAdmission = false
	s.state = stateWaiting
	s.recoveryRun = active
	s.mu.Unlock()
	snapshot := Snapshot{Messages: messages, Phase: PhaseRecoveryWaiting, Notice: NoticeRecoveryWaiting, RecoveryAt: active.LeaseUntil}
	return ActionResult{Kind: ActionRecoveryWaiting, Snapshot: snapshot}, nil
}

func (s *service) Recover(ctx context.Context) (ActionResult, error) {
	s.mu.Lock()
	if s.state >= stateClosing {
		s.mu.Unlock()
		return ActionResult{}, ErrClosing
	}
	if s.state != stateWaiting {
		s.mu.Unlock()
		return ActionResult{}, ErrBusy
	}
	target := s.recoveryRun
	s.state = stateRecovering
	s.pending.Add(1)
	s.mu.Unlock()
	defer s.pending.Done()
	current, activeErr := s.store.ActiveRun(ctx, s.sessionID)
	if activeErr == nil && !current.Terminal() && time.Now().Before(current.LeaseUntil) {
		messages, _ := s.projectHistory(ctx)
		s.mu.Lock()
		if s.state == stateRecovering {
			s.state = stateWaiting
			s.recoveryRun = current
		}
		s.mu.Unlock()
		snapshot := Snapshot{Messages: messages, Phase: PhaseRecoveryWaiting, Notice: NoticeRecoveryWaiting, RecoveryAt: current.LeaseUntil}
		return ActionResult{Kind: ActionRecoveryWaiting, Snapshot: snapshot}, nil
	}
	if activeErr != nil && !errors.Is(activeErr, session.ErrNotFound) {
		s.resetPending()
		return ActionResult{}, fmt.Errorf("%w", ErrUnavailable)
	}
	s.mu.Lock()
	if s.state >= stateClosing {
		s.mu.Unlock()
		return ActionResult{}, ErrClosing
	}
	runCtx, runCancel := context.WithCancel(s.ctx)
	s.pendingCancel = runCancel
	s.pendingAdmission = true
	s.mu.Unlock()
	tailCtx, tailCancel := context.WithCancel(s.ctx)
	events, err := s.tail.Subscribe(tailCtx, s.sessionID)
	if err != nil {
		tailCancel()
		runCancel()
		s.resetPending()
		return ActionResult{}, fmt.Errorf("%w", ErrUnavailable)
	}
	handle, err := s.runtime.Resume(runCtx, target.ID)
	if errors.Is(err, session.ErrSessionBusy) {
		tailCancel()
		runCancel()
		return s.toWaiting(ctx)
	}
	if err != nil {
		tailCancel()
		runCancel()
		s.resetPending()
		return ActionResult{}, fmt.Errorf("%w", ErrUnavailable)
	}
	return s.publishAdmitted(ctx, handle, events, runCancel, tailCancel, "")
}

func (s *service) pump(active *activeRun, events <-chan session.EventRecord, initial Snapshot) {
	acc := eventAccumulator{}
	version := initial.Version
	resync := initial.Resync
	result := agentruntime.Result{RunID: active.handle.RunID(), Status: session.RunFailed}
	resultConsumed := false
	pendingInterrupt := ""
	var interruptTicker *time.Ticker
	var interruptTicks <-chan time.Time
	stopInterruptTicker := func() {
		if interruptTicker != nil {
			interruptTicker.Stop()
			interruptTicker = nil
			interruptTicks = nil
		}
	}
	tryInterrupt := func() {
		if pendingInterrupt == "" {
			stopInterruptTicker()
			return
		}
		checkCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		durable, err := s.store.GetRun(checkCtx, active.handle.RunID())
		cancel()
		if err == nil && durable.Status == session.RunRunning {
			_ = active.handle.Interrupt(context.Background(), pendingInterrupt)
			pendingInterrupt = ""
			stopInterruptTicker()
			return
		}
		if err == nil && durable.Terminal() {
			pendingInterrupt = ""
			stopInterruptTicker()
			return
		}
		if interruptTicker == nil {
			interruptTicker = time.NewTicker(time.Millisecond)
			interruptTicks = interruptTicker.C
		}
	}
	defer func() {
		stopInterruptTicker()
		if recover() != nil && !resultConsumed {
			control, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = active.handle.Interrupt(control, "runtime bridge recovery")
			cancel()
			settled, ok := <-active.handle.Done()
			if ok {
				result = settled
			}
			resultConsumed = true
		}
		func() {
			defer func() {
				if recover() != nil {
					s.finishPumpFallback(active, initial, version+1)
				}
			}()
			s.finishPump(active, initial, result, version+1, resync)
		}()
	}()
	if s.pumpHook != nil {
		s.pumpHook("before-result")
	}
	for {
		select {
		case event, ok := <-events:
			if !ok {
				active.cancelTail()
				events = nil
				resync = true
				continue
			}
			content, changed := acc.accept(event, s.sessionID, active.handle.RunID())
			if acc.resync {
				resync = true
				active.cancelTail()
				events = nil
			}
			if changed {
				version++
				snapshot := initial
				snapshot.Version = version
				snapshot.LiveAssistant = content
				snapshot.Resync = resync
				if !active.run.publish(snapshot) {
					resync = true
				}
			}
		case reason := <-active.interrupt:
			pendingInterrupt = reason
			tryInterrupt()
		case <-interruptTicks:
			tryInterrupt()
		case settled, ok := <-active.handle.Done():
			if !ok {
				settled = agentruntime.Result{RunID: active.handle.RunID(), Status: session.RunFailed}
			}
			result, resultConsumed = settled, true
			if s.pumpHook != nil {
				s.pumpHook("after-result")
			}
			return
		}
	}
}

func (a *activeRun) requestInterrupt(reason string) {
	select {
	case a.interrupt <- reason:
	default:
	}
}

func (s *service) finishPump(active *activeRun, initial Snapshot, result agentruntime.Result, version uint64, resync bool) {
	messages, err := s.projectHistory(context.WithoutCancel(s.ctx))
	active.cancelTail()
	active.cancelRun()
	terminal := Snapshot{RunID: active.handle.RunID(), Version: version, Terminal: true, Messages: messages, Phase: PhaseIdle, Resync: resync}
	if err != nil {
		terminal.Messages = initial.Messages
		terminal.Resync = true
		terminal.Notice = NoticeUnavailable
	}
	switch result.Status {
	case session.RunInterrupted:
		terminal.Notice = NoticeInterrupted
	case session.RunFailed:
		terminal.Notice = NoticeFailed
	}
	s.clearActive(active)
	active.run.finish(terminal)
}

func (s *service) finishPumpFallback(active *activeRun, initial Snapshot, version uint64) {
	active.cancelTail()
	active.cancelRun()
	s.clearActive(active)
	active.run.finish(Snapshot{RunID: active.handle.RunID(), Version: version, Terminal: true, Resync: true, Messages: initial.Messages, Phase: PhaseIdle, Notice: NoticeUnavailable})
}

func (s *service) clearActive(active *activeRun) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == active {
		s.active = nil
		if s.state < stateClosing {
			s.state = stateIdle
		}
	}
}

func (s *service) InterruptActive(ctx context.Context) error {
	s.mu.Lock()
	if s.state >= stateClosing {
		s.mu.Unlock()
		return ErrClosing
	}
	if s.pendingCancel != nil {
		s.pendingInterrupt = true
		s.mu.Unlock()
		return nil
	}
	active := s.active
	s.mu.Unlock()
	if active != nil {
		active.requestInterrupt("user interrupt")
	}
	return nil
}

func (s *service) Close(ctx context.Context) error {
	s.shutdownOnce.Do(func() { go s.shutdown() })
	select {
	case <-ctx.Done():
		return fmt.Errorf("%w", ErrCloseTimeout)
	case <-s.shutdownDone:
		return s.shutdownErr
	}
}

func (s *service) shutdown() {
	s.mu.Lock()
	if s.state != stateClosed {
		s.state = stateClosing
	}
	pendingCancel := s.pendingCancel
	if pendingCancel != nil {
		s.pendingInterrupt = true
	}
	s.mu.Unlock()
	s.pending.Wait()
	s.mu.Lock()
	active := s.active
	s.mu.Unlock()
	if active != nil {
		active.requestInterrupt("application closing")
		<-active.run.Finished()
	}
	s.cancel()
	s.tail.Close()
	s.shutdownErr = s.store.Close()
	s.mu.Lock()
	s.state = stateClosed
	s.mu.Unlock()
	close(s.shutdownDone)
}

var _ Service = (*service)(nil)
var _ tailer = (*stream.Tail)(nil)
