package runtimeui

import (
	"context"
	"sync"

	"github.com/mattsp1290/eino-agent/session"
)

type runStream struct {
	id                session.RunID
	updates           chan Snapshot
	finished          chan struct{}
	mu                sync.Mutex
	terminal          Snapshot
	terminalReady     bool
	terminalDelivered bool
}

func newRun(id session.RunID) *runStream {
	return &runStream{id: id, updates: make(chan Snapshot, 8), finished: make(chan struct{})}
}

func (r *runStream) ID() session.RunID         { return r.id }
func (r *runStream) Finished() <-chan struct{} { return r.finished }

func (r *runStream) publish(snapshot Snapshot) bool {
	select {
	case r.updates <- snapshot:
		return true
	default:
		return false
	}
}

// finish stores the terminal snapshot and releases consumers in one step.
func (r *runStream) finish(snapshot Snapshot) {
	r.store(snapshot)
	r.release()
}

// store makes the terminal snapshot available before consumers are released.
func (r *runStream) store(snapshot Snapshot) {
	r.mu.Lock()
	r.terminal = snapshot
	r.terminalReady = true
	r.mu.Unlock()
}

// release closes the update stream and the Finished signal, in that order.
func (r *runStream) release() {
	close(r.updates)
	close(r.finished)
}

func (r *runStream) Next(ctx context.Context) (Snapshot, bool) {
	if ctx.Err() != nil {
		return Snapshot{}, false
	}
	select {
	case <-ctx.Done():
		return Snapshot{}, false
	case snapshot, ok := <-r.updates:
		if ok {
			return snapshot, true
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.terminalReady && !r.terminalDelivered {
			r.terminalDelivered = true
			return r.terminal, true
		}
		return Snapshot{}, false
	}
}
