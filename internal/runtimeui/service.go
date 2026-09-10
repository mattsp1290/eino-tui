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
	"github.com/mattsp1290/eino-tui/internal/platform"
	"github.com/mattsp1290/eino-tui/internal/textsafe"
)

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
	stateUnresolved lifecycle = iota
	stateIdle
	stateWaiting
	stateStarting
	stateRecovering
	stateRunning
	stateMutating
	stateClosing
	stateClosed
)

// service is the one workspace controller: one pool, one tail, one
// orchestrator, the owned mounts, a stable workspace configuration, and
// exactly one selected conversation. No background conversation runs here.
type service struct {
	ctx        context.Context
	cancel     context.CancelFunc
	readCtx    context.Context
	cancelRead context.CancelFunc
	store      durableStore
	tail       tailer
	runtime    orchestrator
	mounts     []mountCloser
	prefs      *platform.PreferenceStore
	workspace  platform.Workspace
	config     config.Snapshot

	mu           sync.Mutex
	state        lifecycle
	selected     *conversation
	generation   uint64
	reconcile    bool
	attempt      *attempt
	active       *activeRun
	recoveryRun  session.Run
	pending      sync.WaitGroup
	shutdownOnce sync.Once
	shutdownDone chan struct{}
	shutdownErr  error
}

// attempt is one exclusive lifecycle transition. It captures the conversation
// it operates on so a later selection change can never redirect it.
type attempt struct {
	from             lifecycle
	phase            lifecycle
	conv             conversation
	ctx              context.Context
	cancel           context.CancelFunc
	stopCallerCancel func() bool
	cancelTail       context.CancelFunc
}

type activeRun struct {
	run        *runStream
	handle     agentruntime.Handle
	conv       conversation
	cancelRun  context.CancelFunc
	cancelTail context.CancelFunc
	interrupt  chan string
}

func newService(ctx context.Context, store durableStore, tail tailer, runtime orchestrator, mounts []mountCloser, prefs *platform.PreferenceStore, workspace platform.Workspace, snapshot config.Snapshot) *service {
	lifetime, cancel := context.WithCancel(context.WithoutCancel(ctx))
	readCtx, cancelRead := context.WithCancel(lifetime)
	return &service{
		ctx: lifetime, cancel: cancel, readCtx: readCtx, cancelRead: cancelRead,
		store: store, tail: tail, runtime: runtime, mounts: mounts, prefs: prefs, workspace: workspace, config: snapshot,
		state: stateUnresolved, shutdownDone: make(chan struct{}),
	}
}

// Load resolves the selected conversation on first use (or after a
// reconciliation-required failure) and otherwise projects the current one.
func (s *service) Load(ctx context.Context) (Snapshot, error) {
	s.mu.Lock()
	closing := s.state >= stateClosing
	resolve := s.selected == nil || s.reconcile
	s.mu.Unlock()
	if closing {
		return Snapshot{}, ErrClosing
	}
	if resolve {
		return s.resolveSelection(ctx)
	}
	return s.loadSelected(ctx)
}

// loadSelected projects the captured selection as a counted, cancellable
// read. The returned identity is always the conversation whose history was
// read; a selection that changed underneath is reported as stale.
func (s *service) loadSelected(caller context.Context) (Snapshot, error) {
	ctx, release, err := s.beginRead(caller)
	if err != nil {
		return Snapshot{}, err
	}
	defer release()
	s.mu.Lock()
	if s.selected == nil {
		s.mu.Unlock()
		return Snapshot{}, ErrNoConversation
	}
	conv := *s.selected
	s.mu.Unlock()
	s.retryDefaultTitle(ctx, &conv)
	messages, err := loadHistory(ctx, s.store, conv.id)
	if err != nil && !errors.Is(err, session.ErrNotFound) {
		if ctx.Err() != nil {
			return Snapshot{}, ctx.Err()
		}
		return Snapshot{}, fmt.Errorf("%w", ErrUnavailable)
	}
	active, activeErr := s.store.ActiveRun(ctx, conv.id)
	if activeErr != nil && !errors.Is(activeErr, session.ErrNotFound) {
		if ctx.Err() != nil {
			return Snapshot{}, ctx.Err()
		}
		return Snapshot{}, fmt.Errorf("%w", ErrUnavailable)
	}
	waiting := activeErr == nil && !active.Terminal()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.selected == nil || s.selected.id != conv.id || s.selected.generation != conv.generation {
		return Snapshot{}, ErrStaleGeneration
	}
	info := s.selected.info()
	if waiting {
		if s.state == stateIdle || s.state == stateWaiting {
			s.state = stateWaiting
			s.recoveryRun = active
		}
		return Snapshot{Messages: messages, Phase: PhaseRecoveryWaiting, Notice: NoticeRecoveryWaiting, RecoveryAt: active.LeaseUntil, Conversation: info}, nil
	}
	if s.state == stateWaiting {
		s.state = stateIdle
		s.recoveryRun = session.Run{}
	}
	return Snapshot{Messages: messages, Phase: PhaseIdle, Conversation: info}, nil
}

func (s *service) Start(ctx context.Context, prompt string, startConfig StartConfig) (ActionResult, error) {
	normalized, err := textsafe.Prompt(prompt)
	if err != nil {
		return ActionResult{}, fmt.Errorf("%w", ErrInvalidPrompt)
	}
	a, err := s.beginAttempt(ctx, stateIdle, stateStarting, true)
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
	events, err := s.tail.Subscribe(tailCtx, a.conv.id)
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
	handle, err := s.runtime.Start(a.ctx, agentruntime.Request{
		SessionID: a.conv.id, Message: agentruntime.UserMessage{Content: normalized}, Config: snapshot, Metadata: a.conv.requestMetadata(),
	})
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

// beginAttempt starts one exclusive transition. requireResolved rejects work
// on a missing or reconciliation-pending selection with a fixed error.
func (s *service) beginAttempt(caller context.Context, from, to lifecycle, requireResolved bool) (*attempt, error) {
	if err := caller.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state >= stateClosing {
		return nil, ErrClosing
	}
	if requireResolved {
		if s.selected == nil {
			return nil, ErrNoConversation
		}
		if s.reconcile {
			return nil, ErrReconciliationRequired
		}
	}
	if s.state != from || s.attempt != nil {
		return nil, ErrBusy
	}
	attemptCtx, cancel := context.WithCancel(s.ctx)
	a := &attempt{from: from, phase: to, ctx: attemptCtx, cancel: cancel}
	if s.selected != nil {
		a.conv = *s.selected
	}
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
			s.state = a.from
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
	conv := a.conv
	notice := ""
	if prompt != "" && conv.title == "" {
		// The message is durably admitted. A title failure is metadata-only:
		// keep the handle, consume the draft, and retry from history later.
		titleCtx, cancel := context.WithTimeout(s.readCtx, titleInitializationTimeout)
		title, err := ensureDefaultTitle(titleCtx, s.store, s.workspace.ID, conv.id, conv.number)
		cancel()
		if err != nil {
			notice = NoticeTitleUnavailable
		} else if title != "" {
			conv.title = title
			s.updateTitle(conv.id, title)
		}
	}
	projection, err := loadHistoryProjection(a.ctx, s.store, conv.id, handle.RunID())
	resync := err != nil || projection.Resync
	if err != nil {
		projection.Messages = []Message{{Role: RoleUser, Content: textsafe.Display(prompt), Status: StatusComplete}}
		projection.LiveMessages = nil
	}
	run := newRun(handle.RunID())
	initial := Snapshot{
		RunID: handle.RunID(), Version: 1, Messages: cloneMessages(projection.Messages), LiveMessages: cloneMessages(projection.LiveMessages),
		Phase: PhaseRunning, Resync: resync, Notice: notice, Conversation: conv.info(),
	}
	active := &activeRun{run: run, handle: handle, conv: conv, cancelRun: a.cancel, cancelTail: a.cancelTail, interrupt: make(chan string, 1)}
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
	active, err := s.store.ActiveRun(a.ctx, a.conv.id)
	if err != nil {
		attemptErr := s.attemptError(a)
		s.abortAttempt(a)
		if attemptErr != nil {
			return ActionResult{}, attemptErr
		}
		return ActionResult{}, fmt.Errorf("%w", ErrUnavailable)
	}
	messages, _ := loadHistory(a.ctx, s.store, a.conv.id)
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
	info := s.currentInfoLocked()
	s.mu.Unlock()
	a.cancel()
	if a.cancelTail != nil {
		a.cancelTail()
	}
	snapshot := Snapshot{Messages: messages, Phase: PhaseRecoveryWaiting, Notice: NoticeRecoveryWaiting, RecoveryAt: active.LeaseUntil, Conversation: info}
	return ActionResult{Kind: ActionRecoveryWaiting, Snapshot: snapshot}, nil
}

func (s *service) Recover(ctx context.Context) (ActionResult, error) {
	a, err := s.beginAttempt(ctx, stateWaiting, stateRecovering, true)
	if err != nil {
		return ActionResult{}, err
	}
	defer s.finishPending(a)
	s.mu.Lock()
	target := s.recoveryRun
	s.mu.Unlock()
	current, activeErr := s.store.ActiveRun(a.ctx, a.conv.id)
	if activeErr == nil && !current.Terminal() {
		target = current
	}
	if activeErr == nil && !current.Terminal() && time.Now().Before(current.LeaseUntil) {
		messages, _ := loadHistory(a.ctx, s.store, a.conv.id)
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
	events, err := s.tail.Subscribe(tailCtx, a.conv.id)
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
	s.cancelRead()
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
	for i := len(s.mounts) - 1; i >= 0; i-- {
		s.mounts[i].Deactivate()
	}
	for i := len(s.mounts) - 1; i >= 0; i-- {
		cleanupErr = errors.Join(cleanupErr, s.mounts[i].Close(context.Background()))
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
