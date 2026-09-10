package runtimeui

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mattsp1290/eino-agent/model"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
	"github.com/mattsp1290/eino-tui/internal/demomodel"
	"github.com/mattsp1290/eino-tui/internal/platform"
)

// fixtureWorkspace prepares a private state directory and a canonical
// workspace identity for one test.
func fixtureWorkspace(t *testing.T) (platform.Paths, platform.Workspace) {
	t.Helper()
	paths, err := platform.PrepareState(context.Background(), filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := platform.IdentifyWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return paths, workspace
}

func openFixture(ctx context.Context, paths platform.Paths, workspace platform.Workspace) (Service, error) {
	return openFixtureWithResolver(ctx, paths, workspace, demomodel.DynamicResolver(nil))
}

func openFixtureWithResolver(ctx context.Context, paths platform.Paths, workspace platform.Workspace, resolver model.Resolver) (Service, error) {
	return Open(ctx, paths, workspace, Config{
		Resolver:  resolver,
		AgentName: "fixture", SystemPrompt: "Return only the configured deterministic fixture response.",
	})
}

// openResolvedFixture opens the production wiring and resolves the first
// conversation so Start can be called immediately.
func openResolvedFixture(t *testing.T, ctx context.Context, paths platform.Paths, workspace platform.Workspace) Service {
	t.Helper()
	return openResolvedFixtureWithResolver(t, ctx, paths, workspace, demomodel.DynamicResolver(nil))
}

func openResolvedFixtureWithResolver(t *testing.T, ctx context.Context, paths platform.Paths, workspace platform.Workspace, resolver model.Resolver) Service {
	t.Helper()
	opened, err := openFixtureWithResolver(ctx, paths, workspace, resolver)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := opened.Load(ctx); err != nil {
		t.Fatal(err)
	}
	return opened
}

// openTestStore opens the host-owned pool for direct durable assertions.
func openTestStore(t *testing.T, ctx context.Context, database string) durableStore {
	t.Helper()
	store, err := openDatabase(ctx, database)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// selectForTest installs a synthetic selection on a service built with
// newService, bypassing preference resolution for state-machine tests.
func selectForTest(chat *service, id session.ID) {
	chat.mu.Lock()
	defer chat.mu.Unlock()
	chat.generation++
	chat.selected = &conversation{id: id, number: 1, generation: chat.generation}
	if chat.state == stateUnresolved {
		chat.state = stateIdle
	}
}

func fixtureStartConfig() StartConfig {
	return StartConfig{
		Selection:       model.Selection{ProviderID: codexmodel.ProviderID, ModelID: codexmodel.DefaultModel},
		ReasoningEffort: "medium",
	}
}
