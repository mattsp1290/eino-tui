package runtimeui

import (
	"context"

	"github.com/mattsp1290/eino-agent/model"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
	"github.com/mattsp1290/eino-tui/internal/demomodel"
)

func openFixture(ctx context.Context, database string, sessionID session.ID, workspace string) (Service, error) {
	return openFixtureWithResolver(ctx, database, sessionID, workspace, demomodel.DynamicResolver(nil))
}

func openFixtureWithResolver(ctx context.Context, database string, sessionID session.ID, workspace string, resolver model.Resolver) (Service, error) {
	return Open(ctx, database, sessionID, workspace, Config{
		Resolver:  resolver,
		AgentName: "fixture", SystemPrompt: "Return only the configured deterministic fixture response.",
	})
}

func fixtureStartConfig() StartConfig {
	return StartConfig{
		Selection:       model.Selection{ProviderID: codexmodel.ProviderID, ModelID: codexmodel.DefaultModel},
		ReasoningEffort: "medium",
	}
}
