package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mattsp1290/eino-agent/model"
	"github.com/mattsp1290/eino-tui/internal/app"
	"github.com/mattsp1290/eino-tui/internal/cli"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
	"github.com/mattsp1290/eino-tui/internal/demomodel"
	"github.com/mattsp1290/eino-tui/internal/platform"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
	"github.com/mattsp1290/eino-tui/internal/subscription"
)

type fixtureSubscription struct {
	output io.Writer
	status subscription.Status
	mode   string
	calls  *atomic.Int32
}

func openFixture(ctx context.Context, paths platform.Paths, workspace platform.Workspace, resolver model.Resolver) (runtimeui.Service, error) {
	service, err := runtimeui.Open(ctx, paths, workspace, runtimeui.Config{
		Resolver:  resolver,
		AgentName: "fixture", SystemPrompt: "Return only the configured deterministic fixture response.",
	})
	if err != nil {
		return nil, err
	}
	if _, err := service.Load(ctx); err != nil {
		return nil, err
	}
	return service, nil
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
func (s fixtureSubscription) ListModels(ctx context.Context) ([]codexmodel.CatalogEntry, error) {
	call := s.calls.Add(1)
	if (s.mode == "--block-first-catalog" && call == 1) || (s.mode == "--block-catalog-refresh" && call > 1) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return []codexmodel.CatalogEntry{
		{
			ModelID: "gpt-5.5", DisplayName: "GPT-5.5 Fixture", Priority: 1, DefaultEffort: "medium",
			SupportedEfforts: []codexmodel.ReasoningEffort{{ID: "low", Description: "Fast"}, {ID: "medium", Description: "Balanced"}, {ID: "high", Description: "Deep"}},
		},
		{
			ModelID: "gpt-5.6", DisplayName: "GPT-5.6 Fixture", Priority: 2, DefaultEffort: "high",
			SupportedEfforts: []codexmodel.ReasoningEffort{{ID: "medium", Description: "Balanced"}, {ID: "high", Description: "Deep"}},
		},
	}, nil
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
	catalogCalls := &atomic.Int32{}
	deps.NewSubscription = func(output io.Writer) cli.Subscription {
		return fixtureSubscription{output: output, status: subscription.LoggedIn, mode: mode, calls: catalogCalls}
	}
	deps.OpenService = func(ctx context.Context, paths platform.Paths, workspace platform.Workspace, _ runtimeui.Config) (runtimeui.Service, error) {
		return openFixture(ctx, paths, workspace, demomodel.DynamicResolver(nil))
	}
	switch mode {
	case "--block-first-catalog", "--block-catalog-refresh":
		// Catalog behavior is provided by fixtureSubscription; keep chat defaults.
	case "--logged-out":
		deps.NewSubscription = func(output io.Writer) cli.Subscription {
			return fixtureSubscription{output: output, status: subscription.NotLoggedIn, mode: mode, calls: catalogCalls}
		}
	case "--long":
		var waits atomic.Int32
		deps.OpenService = func(ctx context.Context, paths platform.Paths, workspace platform.Workspace, _ runtimeui.Config) (runtimeui.Service, error) {
			return openFixture(ctx, paths, workspace, demomodel.DynamicResolver(func(ctx context.Context) error {
				if waits.Add(1) == 1 {
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(10 * time.Second):
					return nil
				}
			}))
		}
	case "--tool-read":
		deps.OpenService = func(ctx context.Context, paths platform.Paths, workspace platform.Workspace, _ runtimeui.Config) (runtimeui.Service, error) {
			return openFixture(ctx, paths, workspace, demomodel.DynamicToolResolver(nil))
		}
	case "--rename-tool":
		deps.OpenService = func(ctx context.Context, paths platform.Paths, workspace platform.Workspace, _ runtimeui.Config) (runtimeui.Service, error) {
			return openFixture(ctx, paths, workspace, demomodel.DynamicRenameToolResolver(nil))
		}
	case "--program-panic":
		deps.NewProgram = func(tea.Model, context.Context, io.Reader, io.Writer) cli.Program { return panicProgram{} }
	case "--program-error":
		deps.NewProgram = func(tea.Model, context.Context, io.Reader, io.Writer) cli.Program { return errorProgram{} }
	case "--wedged-close":
		base := deps.OpenService
		deps.OpenService = func(ctx context.Context, paths platform.Paths, workspace platform.Workspace, cfg runtimeui.Config) (runtimeui.Service, error) {
			service, err := base(ctx, paths, workspace, cfg)
			return wedgedService{service}, err
		}
	case "--model-error":
		deps.OpenService = func(ctx context.Context, paths platform.Paths, workspace platform.Workspace, _ runtimeui.Config) (runtimeui.Service, error) {
			return openFixture(ctx, paths, workspace, demomodel.DynamicErrorResolver(func(context.Context) error { return nil }, errors.New("secret prompt /tmp/private\x1b]0;leak\a")))
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
