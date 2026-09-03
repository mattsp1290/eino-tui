package runtimeui

import (
	"context"

	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/demomodel"
)

func openFixture(ctx context.Context, database string, sessionID session.ID, workspace string) (Service, error) {
	return OpenWithResolver(ctx, database, sessionID, workspace, demomodel.Resolver(nil))
}
