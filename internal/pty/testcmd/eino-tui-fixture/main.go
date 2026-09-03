package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mattsp1290/eino-agent/model"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/app"
	"github.com/mattsp1290/eino-tui/internal/cli"
	"github.com/mattsp1290/eino-tui/internal/demomodel"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
	"github.com/mattsp1290/eino-tui/internal/subscription"
)

type fixtureSubscription struct {
	output io.Writer
	status subscription.Status
}

func openFixture(ctx context.Context, database string, id session.ID, workspace string, resolver model.Resolver) (runtimeui.Service, error) {
	return runtimeui.Open(ctx, database, id, workspace, runtimeui.Config{
		Resolver: resolver, Selection: model.Selection{ProviderID: demomodel.ProviderID, ModelID: demomodel.ModelID},
		AgentName: "fixture", SystemPrompt: "Return only the configured deterministic fixture response.",
	})
}

func (s fixtureSubscription) LoginDevice(context.Context) error {
	_, err := fmt.Fprintln(s.output, "Open https://auth.example/device and enter code: SAFE-1234")
	return err
}
func (s fixtureSubscription) Status(context.Context) (subscription.Status, error) {
	return s.status, nil
}
func (fixtureSubscription) HTTPClient(context.Context) (*http.Client, error) {
	return &http.Client{}, nil
}

type panicProgram struct{}

func (panicProgram) Run() (tea.Model, error) { panic("secret prompt /tmp/private\x1b]0;leak\a") }
func (panicProgram) ReleaseTerminal() error  { return nil }

type errorProgram struct{}

func (errorProgram) Run() (tea.Model, error) { return nil, errors.New("secret prompt /tmp/private") }
func (errorProgram) ReleaseTerminal() error  { return nil }

type wedgedService struct{ runtimeui.Service }

func (w wedgedService) Close(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }

type panicModel struct{ where string }

func (p panicModel) Init() tea.Cmd {
	if p.where == "init" {
		panic("secret")
	}
	if p.where == "command" {
		return func() tea.Msg { panic("secret") }
	}
	return nil
}
func (p panicModel) Update(tea.Msg) (tea.Model, tea.Cmd) {
	if p.where == "update" {
		panic("secret")
	}
	return p, nil
}
func (p panicModel) View() tea.View {
	if p.where == "view" {
		panic("secret")
	}
	return tea.NewView("fixture")
}

func main() {
	mode := ""
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	deps := cli.ProductionDependencies()
	deps.NewSubscription = func(output io.Writer) cli.Subscription {
		return fixtureSubscription{output: output, status: subscription.LoggedIn}
	}
	deps.OpenService = func(ctx context.Context, db string, id session.ID, workspace string, _ runtimeui.Config) (runtimeui.Service, error) {
		return openFixture(ctx, db, id, workspace, demomodel.Resolver(nil))
	}
	switch mode {
	case "--logged-out":
		deps.NewSubscription = func(output io.Writer) cli.Subscription {
			return fixtureSubscription{output: output, status: subscription.NotLoggedIn}
		}
	case "--long":
		deps.OpenService = func(ctx context.Context, db string, id session.ID, workspace string, _ runtimeui.Config) (runtimeui.Service, error) {
			return openFixture(ctx, db, id, workspace, demomodel.Resolver(demomodel.TimerWait(10*time.Second)))
		}
	case "--program-panic":
		deps.NewProgram = func(tea.Model, context.Context, io.Reader, io.Writer) cli.Program { return panicProgram{} }
	case "--program-error":
		deps.NewProgram = func(tea.Model, context.Context, io.Reader, io.Writer) cli.Program { return errorProgram{} }
	case "--wedged-close":
		base := deps.OpenService
		deps.OpenService = func(ctx context.Context, db string, id session.ID, workspace string, cfg runtimeui.Config) (runtimeui.Service, error) {
			service, err := base(ctx, db, id, workspace, cfg)
			return wedgedService{service}, err
		}
	case "--model-error":
		deps.OpenService = func(ctx context.Context, db string, id session.ID, workspace string, _ runtimeui.Config) (runtimeui.Service, error) {
			return openFixture(ctx, db, id, workspace, demomodel.ErrorResolver(func(context.Context) error { return nil }, errors.New("secret prompt /tmp/private\x1b]0;leak\a")))
		}
	case "--app-init-panic", "--app-update-panic", "--app-view-panic", "--app-command-panic":
		where := map[string]string{"--app-init-panic": "init", "--app-update-panic": "update", "--app-view-panic": "view", "--app-command-panic": "command"}[mode]
		deps.NewApplication = func(_ context.Context, _ runtimeui.Service, cancel context.CancelFunc, _ app.Config) (tea.Model, *app.Fatal) {
			return app.Safe(panicModel{where: where}, cancel)
		}
	default:
		mode = ""
	}
	var args []string
	if mode == "" && len(os.Args) > 1 {
		args = os.Args[1:]
	}
	os.Exit(cli.Run(context.Background(), args, os.Stdin, os.Stdout, os.Stderr, deps))
}
