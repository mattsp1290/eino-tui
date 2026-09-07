package runtimeui

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/mattsp1290/eino-agent/composition"
	"github.com/mattsp1290/eino-agent/config"
	"github.com/mattsp1290/eino-agent/extension"
	agentruntime "github.com/mattsp1290/eino-agent/runtime"
	"github.com/mattsp1290/eino-agent/store/sqlite"
	"github.com/mattsp1290/eino-agent/stream"
	agenttools "github.com/mattsp1290/eino-agent/tools"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
	"github.com/mattsp1290/eino-tui/internal/demomodel"
	"github.com/mattsp1290/eino-tui/internal/platform"
)

func TestServiceInterruptSettlesRunningToolBeforeTerminalSnapshot(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := sqlite.Open(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	tail := stream.NewTail(64)
	registry, err := composition.NewRegistry(nil)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	exited := make(chan struct{})
	component := extension.Component{InstanceID: "interrupt-fixture", Artifact: extension.Artifact{
		Name: "interrupt-fixture", Version: "1", Hash: "fixture-hash", ConfigHash: "fixture-config", SourceKind: extension.SourceNative,
	}}
	mount, err := registry.Mount(ctx, component, composition.InstallerFunc(func(_ context.Context, registrar *composition.Registrar) error {
		return registrar.Tool(composition.ToolRegistration{ID: "file-read", Scope: extension.GlobalScope(), Definition: agenttools.Definition{
			Name: "file_read", Description: "blocking read fixture", Retention: agentruntime.RetentionPolicy{MaxInlineBytes: 4096},
			Execute: func(ctx context.Context, _ agenttools.Execution) (json.RawMessage, error) {
				close(started)
				defer close(exited)
				<-ctx.Done()
				return nil, ctx.Err()
			},
		}})
	}))
	if err != nil {
		t.Fatal(err)
	}
	orchestrator, err := agentruntime.NewStreamingOrchestrator(
		agentruntime.WithStore(store), agentruntime.WithModelResolver(demomodel.DynamicToolResolver(func(context.Context) error { return nil })),
		agentruntime.WithEventSink(tail), agentruntime.WithIDGenerator(platform.IDs{}), agentruntime.WithRunPlanProvider(registry),
		agentruntime.WithOwnerID("tool-interrupt-fixture"), agentruntime.WithQueueSize(16), agentruntime.WithLease(5*time.Second),
		agentruntime.WithModelRequestSafeOptions(codexmodel.ReasoningEffortOptionKey),
	)
	if err != nil {
		mount.Deactivate()
		_ = mount.Close(ctx)
		tail.Close()
		_ = store.Close()
		t.Fatal(err)
	}
	snapshot := config.Snapshot{
		Agent:    config.Agent{Name: "fixture", SystemPrompt: "Use the fixture tool."},
		Tools:    config.ToolConfig{Enabled: []string{"file_read"}},
		Metadata: map[string]string{"workspace_root": workspace},
	}
	chat := newService(ctx, store, tail, orchestrator, mount, platform.WorkspaceSessionID(workspace), snapshot)
	result, err := chat.Start(ctx, "inspect the fixture", fixtureStartConfig())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("tool executor did not start")
	}
	if err := chat.InterruptActive(ctx); err != nil {
		t.Fatal(err)
	}
	terminal, _ := drainRun(t, result.Run)
	select {
	case <-exited:
	default:
		t.Fatal("terminal snapshot arrived before tool executor exited")
	}
	if terminal.Notice != NoticeInterrupted {
		t.Fatalf("notice=%q", terminal.Notice)
	}
	var activity *ToolActivity
	for i := range terminal.Messages {
		for j := range terminal.Messages[i].Tools {
			if terminal.Messages[i].Tools[j].ID == "fixture-read-call" {
				activity = &terminal.Messages[i].Tools[j]
			}
		}
	}
	if activity == nil || activity.Status != ToolInterrupted {
		t.Fatalf("terminal tool activity=%#v", activity)
	}
	closeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := chat.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}

func TestServiceStreamsPersistsAndReplays(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	sessionID := platform.WorkspaceSessionID(root)
	service, err := openFixture(ctx, paths.Database, sessionID, root)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := service.Load(ctx)
	if err != nil || len(loaded.Messages) != 0 {
		t.Fatalf("initial load = %#v, %v", loaded, err)
	}
	result, err := service.Start(ctx, "hello λ\nsecond line", fixtureStartConfig())
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != ActionStarted || result.Run == nil || len(result.Snapshot.Messages) != 1 || result.Snapshot.Messages[0].Role != RoleUser {
		t.Fatalf("start = %#v", result)
	}
	terminal, provisional := drainRun(t, result.Run)
	if provisional < 2 {
		t.Fatalf("provisional updates = %d", provisional)
	}
	if len(terminal.Messages) != 2 || terminal.Messages[0].Content != "hello λ\nsecond line" {
		t.Fatalf("terminal = %#v", terminal)
	}
	closeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := service.Close(closeCtx); err != nil {
		t.Fatal(err)
	}

	reopened, err := openFixture(ctx, paths.Database, sessionID, root)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := reopened.Load(ctx)
	if err != nil || len(replay.Messages) != 2 {
		t.Fatalf("replay = %#v, %v", replay, err)
	}
	if err := reopened.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}

func TestServiceInterruptKeepsAdmittedUserAndOmitsEmptyAssistant(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := openFixture(ctx, paths.Database, platform.WorkspaceSessionID(root), root)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Start(ctx, "interrupt me", fixtureStartConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := service.InterruptActive(ctx); err != nil {
		t.Fatal(err)
	}
	terminal, _ := drainRun(t, result.Run)
	if terminal.Notice != NoticeInterrupted {
		t.Fatalf("notice = %q", terminal.Notice)
	}
	if len(terminal.Messages) != 1 || terminal.Messages[0].Role != RoleUser || terminal.Messages[0].Status != StatusInterrupted {
		t.Fatalf("messages = %#v", terminal.Messages)
	}
	closeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := service.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}

func drainRun(t *testing.T, run Run) (Snapshot, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var terminal Snapshot
	provisional := 0
	for {
		snapshot, ok := run.Next(ctx)
		if !ok {
			if ctx.Err() != nil {
				t.Fatal(ctx.Err())
			}
			break
		}
		if snapshot.Terminal {
			terminal = snapshot
		} else {
			provisional++
		}
	}
	if !terminal.Terminal {
		t.Fatal("terminal snapshot missing")
	}
	return terminal, provisional
}
