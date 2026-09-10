package runtimeui

import (
	"context"
	"fmt"
	"time"

	"github.com/mattsp1290/eino-agent/composition"
	"github.com/mattsp1290/eino-agent/config"
	"github.com/mattsp1290/eino-agent/model"
	agentruntime "github.com/mattsp1290/eino-agent/runtime"
	"github.com/mattsp1290/eino-agent/stream"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
	"github.com/mattsp1290/eino-tui/internal/conversationtools"
	"github.com/mattsp1290/eino-tui/internal/platform"
	"github.com/mattsp1290/eino-tui/internal/workspacetools"
)

type Config struct {
	Resolver     model.Resolver
	AgentName    string
	SystemPrompt string
}

// EnabledToolNames is the exact five-name run allowlist: the four read-only
// workspace leaf tools plus the current-conversation rename tool.
func EnabledToolNames() []string {
	return append(workspacetools.EnabledNames(), conversationtools.Name)
}

// Open builds the in-process runtime around the protected state paths for one
// canonical workspace. The selected conversation is resolved by Load.
func Open(ctx context.Context, paths platform.Paths, workspace platform.Workspace, cfg Config) (Service, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if paths.Database == "" || paths.Workspaces == "" || workspace.Root == "" || !platform.ValidWorkspaceID(workspace.ID) {
		return nil, fmt.Errorf("build runtime: invalid workspace state")
	}
	return open(ctx, paths, workspace, cfg)
}

func validateConfig(cfg Config) error {
	if cfg.Resolver == nil || cfg.AgentName == "" || cfg.SystemPrompt == "" {
		return fmt.Errorf("build runtime: invalid configuration")
	}
	return nil
}

func open(ctx context.Context, paths platform.Paths, workspace platform.Workspace, cfg Config) (Service, error) {
	store, err := openDatabase(ctx, paths.Database)
	if err != nil {
		return nil, fmt.Errorf("open durable store: %w", err)
	}
	tail := stream.NewTail(64)
	plans, err := composition.NewRegistry(nil)
	if err != nil {
		tail.Close()
		_ = store.Close()
		return nil, fmt.Errorf("build runtime: %w", err)
	}
	var mounts []mountCloser
	unwind := func() {
		for i := len(mounts) - 1; i >= 0; i-- {
			mounts[i].Deactivate()
		}
		for i := len(mounts) - 1; i >= 0; i-- {
			_ = mounts[i].Close(context.WithoutCancel(ctx))
		}
		tail.Close()
		_ = store.Close()
	}
	workspaceMount, err := workspacetools.Mount(ctx, plans)
	if err != nil {
		unwind()
		return nil, fmt.Errorf("%w: %v", ErrToolsUnavailable, err)
	}
	mounts = append(mounts, workspaceMount)
	titleMount, err := conversationtools.Mount(ctx, plans)
	if err != nil {
		unwind()
		return nil, fmt.Errorf("%w: %v", ErrToolsUnavailable, err)
	}
	mounts = append(mounts, titleMount)
	snapshot := config.Snapshot{
		Agent:    config.Agent{Name: cfg.AgentName, SystemPrompt: cfg.SystemPrompt},
		Tools:    config.ToolConfig{Enabled: EnabledToolNames()},
		Metadata: map[string]string{"workspace_id": workspace.ID, "workspace_root": workspace.Root},
	}
	orchestrator, err := agentruntime.NewStreamingOrchestrator(
		agentruntime.WithStore(store), agentruntime.WithModelResolver(cfg.Resolver),
		agentruntime.WithEventSink(tail), agentruntime.WithIDGenerator(platform.IDs{}),
		agentruntime.WithRunPlanProvider(plans), agentruntime.WithOwnerID("eino-tui-"+string(platform.IDs{}.NewEventID())),
		agentruntime.WithQueueSize(16), agentruntime.WithLease(5*time.Second),
		agentruntime.WithModelRequestSafeOptions(codexmodel.ReasoningEffortOptionKey),
	)
	if err != nil {
		unwind()
		return nil, fmt.Errorf("build runtime: %w", err)
	}
	return newService(ctx, store, tail, orchestrator, mounts, platform.NewPreferenceStore(paths.Workspaces), workspace, snapshot), nil
}
