package runtimeui

import (
	"context"
	"errors"
	"time"

	codexauth "github.com/mattsp1290/codex-auth-go"
	agentruntime "github.com/mattsp1290/eino-agent/runtime"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/conversationtools"
)

func (s *service) pump(active *activeRun, events <-chan session.EventRecord, initial Snapshot) {
	acc := newEventAccumulator(initial.LiveMessages)
	version := initial.Version
	resync := initial.Resync || acc.resync
	if resync {
		active.cancelTail()
		events = nil
	}
	result := agentruntime.Result{RunID: active.handle.RunID(), Status: session.RunFailed}
	resultConsumed := false
	pendingInterrupt := ""
	refreshedRenames := make(map[string]bool)
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
	for {
		select {
		case event, ok := <-events:
			if !ok {
				active.cancelTail()
				events = nil
				resync = true
				continue
			}
			update := acc.accept(s.ctx, s.store, event, active.conv.id, active.handle.RunID())
			if acc.resync {
				resync = true
				active.cancelTail()
				events = nil
			}
			if update.changed {
				if s.refreshRenamedTitle(active, update.messages, refreshedRenames) {
					initial.Conversation = active.conv.info()
				}
				version++
				snapshot := initial
				snapshot.Version = version
				snapshot.Messages = cloneMessages(initial.Messages)
				snapshot.LiveMessages = cloneMessages(update.messages)
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
			return
		}
	}
}

// refreshRenamedTitle coalesces a title refresh onto the existing tool
// lifecycle event once a rename call settles. No new durable event is needed.
func (s *service) refreshRenamedTitle(active *activeRun, messages []Message, refreshed map[string]bool) bool {
	settled := false
	for _, message := range messages {
		for _, activity := range message.Tools {
			if activity.Name != conversationtools.Name || toolStatusRank(activity.Status) < 3 || refreshed[activity.ID] {
				continue
			}
			refreshed[activity.ID] = true
			settled = true
		}
	}
	if !settled {
		return false
	}
	refreshCtx, cancel := context.WithTimeout(s.ctx, titleRefreshTimeout)
	defer cancel()
	record, err := s.store.GetSession(refreshCtx, active.conv.id)
	if err != nil || record.Title == active.conv.title {
		return false
	}
	active.conv.title = record.Title
	s.updateTitle(active.conv.id, record.Title)
	return true
}

func (a *activeRun) requestInterrupt(reason string) {
	select {
	case a.interrupt <- reason:
	default:
	}
}

func (s *service) finishPump(active *activeRun, initial Snapshot, result agentruntime.Result, version uint64, resync bool) {
	settledCtx := context.WithoutCancel(s.ctx)
	conv := active.conv
	messages, err := loadHistory(settledCtx, s.store, conv.id)
	refreshCtx, cancel := context.WithTimeout(settledCtx, titleInitializationTimeout)
	s.refreshTitle(refreshCtx, &conv)
	cancel()
	active.conv = conv
	active.cancelTail()
	active.cancelRun()
	terminal := Snapshot{RunID: active.handle.RunID(), Version: version, Terminal: true, Messages: messages, Phase: PhaseIdle, Resync: resync, Conversation: conv.info()}
	if err != nil {
		terminal.Messages = initial.Messages
		terminal.Resync = true
		terminal.Notice = NoticeUnavailable
	} else {
		switch result.Status {
		case session.RunInterrupted:
			terminal.Notice = NoticeInterrupted
		case session.RunFailed:
			switch {
			case errors.Is(result.Error, codexauth.ErrPlanNotIncluded):
				terminal.Notice = NoticePlanUnavailable
			case errors.Is(result.Error, codexauth.ErrQuotaExceeded):
				terminal.Notice = NoticeQuotaExceeded
			default:
				terminal.Notice = NoticeProviderFailed
			}
		}
	}
	s.clearActive(active)
	active.run.finish(terminal)
}

func (s *service) finishPumpFallback(active *activeRun, initial Snapshot, version uint64) {
	active.cancelTail()
	active.cancelRun()
	s.clearActive(active)
	active.run.finish(Snapshot{RunID: active.handle.RunID(), Version: version, Terminal: true, Resync: true, Messages: initial.Messages, Phase: PhaseIdle, Notice: NoticeUnavailable, Conversation: active.conv.info()})
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
