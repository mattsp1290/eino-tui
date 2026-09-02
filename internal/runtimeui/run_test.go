package runtimeui

import (
	"context"
	"testing"
)

func TestRunStreamOrdersProvisionalThenOneTerminal(t *testing.T) {
	run := newRun("run")
	if !run.publish(Snapshot{RunID: "run", Version: 2}) {
		t.Fatal("publish failed")
	}
	run.finish(Snapshot{RunID: "run", Version: 3, Terminal: true})
	first, ok := run.Next(context.Background())
	if !ok || first.Version != 2 {
		t.Fatalf("first=%#v %v", first, ok)
	}
	terminal, ok := run.Next(context.Background())
	if !ok || !terminal.Terminal || terminal.Version != 3 {
		t.Fatalf("terminal=%#v %v", terminal, ok)
	}
	if _, ok := run.Next(context.Background()); ok {
		t.Fatal("terminal delivered twice")
	}
}

func TestRunStreamCancellationWinsBeforeBufferedUpdates(t *testing.T) {
	run := newRun("run")
	run.publish(Snapshot{Version: 2})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := run.Next(ctx); ok {
		t.Fatal("canceled consumer received an update")
	}
}
