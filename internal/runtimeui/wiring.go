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
	"github.com/mattsp1290/eino-tui/internal/demomodel"
	"github.com/mattsp1290/eino-tui/internal/platform"
)

// Open builds the production in-process runtime around the protected SQLite database.
func Open(ctx context.Context, database string, sessionID session.ID, workspace string) (Service, error) {
	return open(ctx, database, sessionID, workspace, demomodel.Resolver(nil))
}

// OpenWithWait is an injection seam used only by the repository's PTY fixture.
func OpenWithWait(ctx context.Context, database string, sessionID session.ID, workspace string, wait demomodel.Waiter) (Service, error) {
	return open(ctx, database, sessionID, workspace, demomodel.Resolver(wait))
}

// OpenWithResolver is an injection seam used only by repository integration fixtures.
func OpenWithResolver(ctx context.Context, database string, sessionID session.ID, workspace string, resolver model.Resolver) (Service, error) {
	return open(ctx, database, sessionID, workspace, resolver)
}

func open(ctx context.Context, database string, sessionID session.ID, workspace string, resolver model.Resolver) (Service, error) {
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
	selection := model.Selection{ProviderID: demomodel.ProviderID, ModelID: demomodel.ModelID}
	snapshot := config.Snapshot{
		Agent:    config.Agent{Name: "demo", SystemPrompt: "Return only the configured scripted demo response.", Model: selection},
		Model:    selection,
		Metadata: map[string]string{"workspace_id": string(sessionID), "workspace_root": workspace},
	}
	orchestrator, err := agentruntime.NewStreamingOrchestrator(
		agentruntime.WithStore(store), agentruntime.WithModelResolver(resolver),
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
