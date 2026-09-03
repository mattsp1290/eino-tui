package runtimeui

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/mattsp1290/eino-agent/composition"
	"github.com/mattsp1290/eino-agent/config"
	"github.com/mattsp1290/eino-agent/model"
	agentruntime "github.com/mattsp1290/eino-agent/runtime"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-agent/store/sqlite"
	"github.com/mattsp1290/eino-agent/stream"
	"github.com/mattsp1290/eino-tui/internal/platform"
)

type Config struct {
	Resolver     model.Resolver
	Selection    model.Selection
	AgentName    string
	SystemPrompt string
}

// Open builds the in-process runtime around the protected SQLite database.
func Open(ctx context.Context, database string, sessionID session.ID, workspace string, cfg Config) (Service, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	return open(ctx, database, sessionID, workspace, cfg)
}

func validateConfig(cfg Config) error {
	if cfg.Resolver == nil || cfg.Selection.ProviderID == "" || cfg.Selection.ModelID == "" || cfg.Selection.Variant != "" ||
		cfg.AgentName == "" || cfg.SystemPrompt == "" {
		return fmt.Errorf("build runtime: invalid configuration")
	}
	return nil
}

func open(ctx context.Context, database string, sessionID session.ID, workspace string, cfg Config) (Service, error) {
	store, err := sqlite.Open(ctx, sqliteDSN(database))
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
	snapshot := config.Snapshot{
		Agent:    config.Agent{Name: cfg.AgentName, SystemPrompt: cfg.SystemPrompt, Model: cfg.Selection},
		Model:    cfg.Selection,
		Metadata: map[string]string{"workspace_id": string(sessionID), "workspace_root": workspace},
	}
	orchestrator, err := agentruntime.NewStreamingOrchestrator(
		agentruntime.WithStore(store), agentruntime.WithModelResolver(cfg.Resolver),
		agentruntime.WithEventSink(tail), agentruntime.WithIDGenerator(platform.IDs{}),
		agentruntime.WithRunPlanProvider(plans), agentruntime.WithOwnerID("eino-tui-"+string(platform.IDs{}.NewEventID())),
		agentruntime.WithQueueSize(16), agentruntime.WithLease(5*time.Second),
	)
	if err != nil {
		tail.Close()
		_ = store.Close()
		return nil, fmt.Errorf("build runtime: %w", err)
	}
	return newService(ctx, store, tail, orchestrator, sessionID, snapshot), nil
}

func sqliteDSN(database string) string {
	location := url.URL{Scheme: "file", Path: database}
	query := url.Values{}
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "busy_timeout(5000)")
	location.RawQuery = query.Encode()
	return location.String()
}
