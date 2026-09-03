package cli

import (
	"context"
	"io"
	"net/http"
	"os"
	"os/signal"

	tea "charm.land/bubbletea/v2"
	agentmodel "github.com/mattsp1290/eino-agent/model"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/app"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
	"github.com/mattsp1290/eino-tui/internal/platform"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
	"github.com/mattsp1290/eino-tui/internal/subscription"
)

type Program interface {
	Run() (tea.Model, error)
	ReleaseTerminal() error
}

type Subscription interface {
	LoginDevice(context.Context) error
	Status(context.Context) (subscription.Status, error)
	HTTPClient(context.Context) (*http.Client, error)
}

type Dependencies struct {
	WorkingDirectory func() (string, error)
	StateDirectory   func() (string, error)
	PrepareState     func(context.Context, string) (platform.Paths, error)
	NewSubscription  func(io.Writer) Subscription
	SignalContext    func(context.Context) (context.Context, context.CancelFunc)
	NewResolver      func(context.Context, *http.Client, string) (agentmodel.Resolver, error)
	OpenService      func(context.Context, string, session.ID, string, runtimeui.Config) (runtimeui.Service, error)
	NewApplication   func(context.Context, runtimeui.Service, context.CancelFunc, app.Config) (tea.Model, *app.Fatal)
	NewProgram       func(tea.Model, context.Context, io.Reader, io.Writer) Program
}

func ProductionDependencies() Dependencies {
	return Dependencies{
		WorkingDirectory: os.Getwd,
		StateDirectory:   platform.ProductionStateDir,
		PrepareState:     platform.PrepareState,
		NewSubscription: func(output io.Writer) Subscription {
			return subscription.New(output)
		},
		SignalContext: func(ctx context.Context) (context.Context, context.CancelFunc) {
			return signal.NotifyContext(ctx, platform.Signals()...)
		},
		NewResolver: codexmodel.NewResolver,
		OpenService: runtimeui.Open,
		NewApplication: func(ctx context.Context, service runtimeui.Service, cancel context.CancelFunc, cfg app.Config) (tea.Model, *app.Fatal) {
			return app.Safe(app.New(ctx, service, cfg), cancel)
		},
		NewProgram: func(model tea.Model, ctx context.Context, input io.Reader, output io.Writer) Program {
			return tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(output), tea.WithoutSignalHandler(), tea.WithoutCatchPanics())
		},
	}
}
