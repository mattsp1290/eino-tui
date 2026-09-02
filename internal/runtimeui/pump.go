package runtimeui

import (
	"context"
	"time"

	agentruntime "github.com/mattsp1290/eino-agent/runtime"
	"github.com/mattsp1290/eino-agent/session"
)

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
