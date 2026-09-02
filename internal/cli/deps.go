package cli

import (
	"context"
	"io"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/app"
	"github.com/mattsp1290/eino-tui/internal/platform"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
)

type Program interface {
	Run() (tea.Model, error)
	ReleaseTerminal() error
}

type Dependencies struct {
	WorkingDirectory func() (string, error)
	StateDirectory   func() (string, error)
	PrepareState     func(context.Context, string) (platform.Paths, error)
	OpenService      func(context.Context, string, session.ID, string) (runtimeui.Service, error)
	NewApplication   func(context.Context, runtimeui.Service, context.CancelFunc) (tea.Model, *app.Fatal)
	NewProgram       func(tea.Model, context.Context, io.Reader, io.Writer) Program
}

func ProductionDependencies() Dependencies {
	return Dependencies{
		WorkingDirectory: os.Getwd,
		StateDirectory:   platform.ProductionStateDir,
		PrepareState:     platform.PrepareState,
		OpenService:      runtimeui.Open,
		NewApplication: func(ctx context.Context, service runtimeui.Service, cancel context.CancelFunc) (tea.Model, *app.Fatal) {
			return app.Safe(app.New(ctx, service), cancel)
		},
		NewProgram: func(model tea.Model, ctx context.Context, input io.Reader, output io.Writer) Program {
			return tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(output), tea.WithoutSignalHandler(), tea.WithoutCatchPanics())
		},
	}
}
