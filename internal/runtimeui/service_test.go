package runtimeui

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	agentmodel "github.com/mattsp1290/eino-agent/model"
	agentruntime "github.com/mattsp1290/eino-agent/runtime"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
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
	service, err := openFixture(ctx, paths.Database, platform.WorkspaceSessionID(workspace), workspace)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Start(ctx, "slow consumer", fixtureStartConfig())
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
	if _, err := service.Start(ctx, "after close", fixtureStartConfig()); !errors.Is(err, ErrClosing) {
		t.Fatalf("post-close start=%v", err)
	}
}

func TestStartFreezesDistinctValidatedSelections(t *testing.T) {
	var requests []agentruntime.Request
	runtime := orchestratorFunc{
		start: func(_ context.Context, request agentruntime.Request) (agentruntime.Handle, error) {
			requests = append(requests, request)
			return nil, errors.New("stop after capture")
		},
		resume: func(context.Context, session.RunID) (agentruntime.Handle, error) { return nil, errors.New("unused") },
	}
	chat := newOrchestratorTestService(t, runtime)
	pairs := []StartConfig{
		{Selection: agentmodel.Selection{ProviderID: codexmodel.ProviderID, ModelID: "gpt-5.5"}, ReasoningEffort: "low"},
		{Selection: agentmodel.Selection{ProviderID: codexmodel.ProviderID, ModelID: "o4-live"}, ReasoningEffort: "high"},
	}
	for _, pair := range pairs {
		if _, err := chat.Start(context.Background(), "capture", pair); err == nil {
			t.Fatal("capture orchestrator failure was hidden")
		}
	}
	if len(requests) != 2 {
		t.Fatalf("requests=%d", len(requests))
	}
	for i, request := range requests {
		if request.Config.Model != pairs[i].Selection || request.Config.Agent.Model != pairs[i].Selection ||
			len(request.Config.Agent.Options) != 1 || request.Config.Agent.Options[codexmodel.ReasoningEffortOptionKey] != pairs[i].ReasoningEffort {
			t.Fatalf("request %d config=%#v", i, request.Config)
		}
	}
	before := len(requests)
	for _, invalid := range []StartConfig{
		{},
		{Selection: agentmodel.Selection{ProviderID: "other", ModelID: "gpt-5.5"}, ReasoningEffort: "medium"},
		{Selection: agentmodel.Selection{ProviderID: codexmodel.ProviderID, ModelID: "bad model"}, ReasoningEffort: "medium"},
		{Selection: agentmodel.Selection{ProviderID: codexmodel.ProviderID, ModelID: "gpt-5.5"}, ReasoningEffort: "xhigh"},
	} {
		if _, err := chat.Start(context.Background(), "invalid", invalid); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("invalid config error=%v", err)
		}
	}
	if len(requests) != before {
		t.Fatalf("invalid config dispatched: %d", len(requests)-before)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := chat.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}

func TestServiceRejectsConcurrentStartAndSupportsSequentialTurns(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := openFixture(ctx, paths.Database, platform.WorkspaceSessionID(workspace), workspace)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Start(ctx, "first", fixtureStartConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(ctx, "duplicate", fixtureStartConfig()); !errors.Is(err, ErrBusy) {
		t.Fatalf("duplicate start=%v", err)
	}
	<-first.Run.Finished()
	second, err := service.Start(ctx, "second", fixtureStartConfig())
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
	opened, err := openFixtureWithResolver(ctx, paths.Database, platform.WorkspaceSessionID(workspace), workspace, demomodel.DynamicResolver(demomodel.TimerWait(10*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	result, err := opened.Start(ctx, "close while active", fixtureStartConfig())
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
