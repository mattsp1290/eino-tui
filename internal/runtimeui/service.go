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
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
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
type mountCloser interface {
	Deactivate()
	Close(context.Context) error
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
	mount     mountCloser
	sessionID session.ID
	config    config.Snapshot

	mu           sync.Mutex
	state        lifecycle
	attempt      *attempt
	active       *activeRun
	recoveryRun  session.Run
	pending      sync.WaitGroup
	shutdownOnce sync.Once
	shutdownDone chan struct{}
	shutdownErr  error
}

type attempt struct {
	phase            lifecycle
	ctx              context.Context
	cancel           context.CancelFunc
	stopCallerCancel func() bool
	cancelTail       context.CancelFunc
}

type activeRun struct {
	run        *runStream
	handle     agentruntime.Handle
	cancelRun  context.CancelFunc
	cancelTail context.CancelFunc
	interrupt  chan string
}

func newService(ctx context.Context, store durableStore, tail tailer, runtime orchestrator, mount mountCloser, sessionID session.ID, snapshot config.Snapshot) *service {
	lifetime, cancel := context.WithCancel(context.WithoutCancel(ctx))
	return &service{ctx: lifetime, cancel: cancel, store: store, tail: tail, runtime: runtime, mount: mount, sessionID: sessionID, config: snapshot, state: stateIdle, shutdownDone: make(chan struct{})}
}

func (s *service) projectHistory(ctx context.Context) ([]Message, error) {
	return loadHistory(ctx, s.store, s.sessionID)
}

func (s *service) projectActiveHistory(ctx context.Context, runID session.RunID) (historyProjection, error) {
	return loadHistoryProjection(ctx, s.store, s.sessionID, runID)
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

func (s *service) Start(ctx context.Context, prompt string, startConfig StartConfig) (ActionResult, error) {
	normalized, err := textsafe.Prompt(prompt)
	if err != nil {
		return ActionResult{}, fmt.Errorf("%w", ErrInvalidPrompt)
	}
	a, err := s.beginAttempt(ctx, stateIdle, stateStarting)
	if err != nil {
		return ActionResult{}, err
	}
	defer s.finishPending(a)
	if !validStartConfig(startConfig) {
		s.abortAttempt(a)
		return ActionResult{}, ErrInvalidConfig
	}
	snapshot := s.config.Clone()
	snapshot.Model = startConfig.Selection
	snapshot.Agent.Model = startConfig.Selection
	snapshot.Agent.Options = map[string]string{codexmodel.ReasoningEffortOptionKey: startConfig.ReasoningEffort}

	tailCtx, tailCancel := context.WithCancel(a.ctx)
	a.cancelTail = tailCancel
	events, err := s.tail.Subscribe(tailCtx, s.sessionID)
	if err != nil {
		attemptErr := s.attemptError(a)
		s.abortAttempt(a)
		if attemptErr != nil {
			return ActionResult{}, attemptErr
		}
		return ActionResult{}, fmt.Errorf("%w", ErrUnavailable)
	}
	if err := s.attemptError(a); err != nil {
		s.abortAttempt(a)
		return ActionResult{}, err
	}
	handle, err := s.runtime.Start(a.ctx, agentruntime.Request{SessionID: s.sessionID, Message: agentruntime.UserMessage{Content: normalized}, Config: snapshot})
	if err != nil {
		if errors.Is(err, session.ErrSessionBusy) {
			return s.toWaiting(a)
		}
		attemptErr := s.attemptError(a)
		s.abortAttempt(a)
		if errors.Is(err, context.Canceled) {
			if attemptErr != nil {
				return ActionResult{}, attemptErr
			}
			return ActionResult{}, context.Canceled
		}
		return ActionResult{}, fmt.Errorf("%w", ErrUnavailable)
	}
	a.stopCallerCancel()
	return s.publishAdmitted(a, handle, events, normalized)
}

func validStartConfig(cfg StartConfig) bool {
	return codexmodel.ValidateSelection(cfg.Selection) == nil &&
		codexmodel.ValidReasoningEffort(cfg.ReasoningEffort)
}

func (s *service) beginAttempt(caller context.Context, from, to lifecycle) (*attempt, error) {
	if err := caller.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state >= stateClosing {
		return nil, ErrClosing
	}
	if s.state != from || s.attempt != nil {
		return nil, ErrBusy
	}
	attemptCtx, cancel := context.WithCancel(s.ctx)
	a := &attempt{phase: to, ctx: attemptCtx, cancel: cancel}
	a.stopCallerCancel = context.AfterFunc(caller, cancel)
	s.attempt = a
	s.state = to
	s.pending.Add(1)
	return a, nil
}

func (s *service) finishPending(a *attempt) {
	a.stopCallerCancel()
	s.pending.Done()
}

func (s *service) abortAttempt(a *attempt) {
	a.cancel()
	if a.cancelTail != nil {
		a.cancelTail()
	}
	s.mu.Lock()
	if s.attempt == a {
		s.attempt = nil
		if s.state == a.phase {
			s.state = stateIdle
		}
	}
	s.mu.Unlock()
}

func (s *service) attemptError(a *attempt) error {
	s.mu.Lock()
	closing := s.state >= stateClosing
	current := s.attempt == a
	s.mu.Unlock()
	if closing {
		return ErrClosing
	}
	if !current || a.ctx.Err() != nil {
		return context.Canceled
	}
	return nil
}

func (s *service) publishAdmitted(a *attempt, handle agentruntime.Handle, events <-chan session.EventRecord, prompt string) (ActionResult, error) {
	projection, err := s.projectActiveHistory(a.ctx, handle.RunID())
	resync := err != nil || projection.Resync
	if err != nil {
		projection.Messages = []Message{{Role: RoleUser, Content: textsafe.Display(prompt), Status: StatusComplete}}
		projection.LiveMessages = nil
	}
	run := newRun(handle.RunID())
	initial := Snapshot{RunID: handle.RunID(), Version: 1, Messages: cloneMessages(projection.Messages), LiveMessages: cloneMessages(projection.LiveMessages), Phase: PhaseRunning, Resync: resync}
	active := &activeRun{run: run, handle: handle, cancelRun: a.cancel, cancelTail: a.cancelTail, interrupt: make(chan string, 1)}
	s.mu.Lock()
	closing := s.state >= stateClosing
	interruptRequested := a.ctx.Err() != nil
	if s.attempt == a {
		s.attempt = nil
	}
	s.active = active
	if !closing {
		s.state = stateRunning
	}
	s.mu.Unlock()
	go s.pump(active, events, cloneSnapshot(initial))
	if closing || interruptRequested {
		reason := "user interrupt"
		if closing {
			reason = "application closing"
		}
		active.requestInterrupt(reason)
	}
	return ActionResult{Kind: ActionStarted, Run: run, Snapshot: cloneSnapshot(initial)}, nil
}

func (s *service) toWaiting(a *attempt) (ActionResult, error) {
	active, err := s.store.ActiveRun(a.ctx, s.sessionID)
	if err != nil {
		attemptErr := s.attemptError(a)
		s.abortAttempt(a)
		if attemptErr != nil {
			return ActionResult{}, attemptErr
		}
		return ActionResult{}, fmt.Errorf("%w", ErrUnavailable)
	}
	messages, _ := s.projectHistory(a.ctx)
	return s.commitWaiting(a, active, messages)
}

func (s *service) commitWaiting(a *attempt, active session.Run, messages []Message) (ActionResult, error) {
	s.mu.Lock()
	if s.state >= stateClosing || s.attempt != a || a.ctx.Err() != nil {
		s.mu.Unlock()
		err := s.attemptError(a)
		s.abortAttempt(a)
		return ActionResult{}, err
	}
	s.attempt = nil
	s.state = stateWaiting
	s.recoveryRun = active
	s.mu.Unlock()
	a.cancel()
	if a.cancelTail != nil {
		a.cancelTail()
	}
	snapshot := Snapshot{Messages: messages, Phase: PhaseRecoveryWaiting, Notice: NoticeRecoveryWaiting, RecoveryAt: active.LeaseUntil}
	return ActionResult{Kind: ActionRecoveryWaiting, Snapshot: snapshot}, nil
}

func (s *service) Recover(ctx context.Context) (ActionResult, error) {
	a, err := s.beginAttempt(ctx, stateWaiting, stateRecovering)
	if err != nil {
		return ActionResult{}, err
	}
	defer s.finishPending(a)
	s.mu.Lock()
	target := s.recoveryRun
	s.mu.Unlock()
	current, activeErr := s.store.ActiveRun(a.ctx, s.sessionID)
	if activeErr == nil && !current.Terminal() {
		target = current
	}
	if activeErr == nil && !current.Terminal() && time.Now().Before(current.LeaseUntil) {
		messages, _ := s.projectHistory(a.ctx)
		return s.commitWaiting(a, current, messages)
	}
	if activeErr != nil && !errors.Is(activeErr, session.ErrNotFound) {
		attemptErr := s.attemptError(a)
		s.abortAttempt(a)
		if errors.Is(activeErr, context.Canceled) {
			if attemptErr != nil {
				return ActionResult{}, attemptErr
			}
			return ActionResult{}, context.Canceled
		}
		return ActionResult{}, fmt.Errorf("%w", ErrUnavailable)
	}
	if err := s.attemptError(a); err != nil {
		s.abortAttempt(a)
		return ActionResult{}, err
	}
	tailCtx, tailCancel := context.WithCancel(a.ctx)
	a.cancelTail = tailCancel
	events, err := s.tail.Subscribe(tailCtx, s.sessionID)
	if err != nil {
		attemptErr := s.attemptError(a)
		s.abortAttempt(a)
		if attemptErr != nil {
			return ActionResult{}, attemptErr
		}
		return ActionResult{}, fmt.Errorf("%w", ErrUnavailable)
	}
	handle, err := s.runtime.Resume(a.ctx, target.ID)
	if errors.Is(err, session.ErrSessionBusy) {
		return s.toWaiting(a)
	}
	if err != nil {
		attemptErr := s.attemptError(a)
		s.abortAttempt(a)
		if errors.Is(err, context.Canceled) {
			if attemptErr != nil {
				return ActionResult{}, attemptErr
			}
			return ActionResult{}, context.Canceled
		}
		return ActionResult{}, fmt.Errorf("%w", ErrUnavailable)
	}
	a.stopCallerCancel()
	return s.publishAdmitted(a, handle, events, "")
}

func (s *service) InterruptActive(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	if s.state >= stateClosing {
		s.mu.Unlock()
		return ErrClosing
	}
	a := s.attempt
	if a != nil {
		s.mu.Unlock()
		a.cancel()
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
	a := s.attempt
	s.mu.Unlock()
	if a != nil {
		a.cancel()
	}
	s.pending.Wait()
	s.mu.Lock()
	active := s.active
	s.mu.Unlock()
	if active != nil {
		active.requestInterrupt("application closing")
		<-active.run.Finished()
	}
	s.cancel()
	var cleanupErr error
	if s.mount != nil {
		s.mount.Deactivate()
		cleanupErr = s.mount.Close(context.Background())
	}
	s.tail.Close()
	s.shutdownErr = errors.Join(cleanupErr, s.store.Close())
	s.mu.Lock()
	s.state = stateClosed
	s.mu.Unlock()
	close(s.shutdownDone)
}

var _ Service = (*service)(nil)
var _ tailer = (*stream.Tail)(nil)
