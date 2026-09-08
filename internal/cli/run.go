package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	tea "charm.land/bubbletea/v2"
	codexauth "github.com/mattsp1290/codex-auth-go"
	"github.com/mattsp1290/eino-agent/model"
	"github.com/mattsp1290/eino-tui/internal/app"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
	"github.com/mattsp1290/eino-tui/internal/platform"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
	"github.com/mattsp1290/eino-tui/internal/subscription"
)

const Version = "0.3.0"

const (
	ExitOK             = 0
	ExitStartup        = 2
	ExitProgram        = 3
	ExitFatal          = 4
	ExitForcedShutdown = 5
	ExitAuth           = 6
	ExitInterrupted    = 130
)

const (
	startupDiagnostic = "eino-tui could not start; check workspace and state permissions"
	toolsDiagnostic   = "eino-tui could not load read-only workspace tools; verify required executables"
	programDiagnostic = "eino-tui stopped because the terminal program failed"
	forcedDiagnostic  = "eino-tui forced shutdown; the unfinished turn will be recovered on next launch"
	authDiagnostic    = "eino-tui could not access Codex authentication"
	loginDiagnostic   = "eino-tui device login failed"
	loginInterrupted  = "eino-tui device login interrupted"
	notLoggedIn       = "eino-tui is not logged in; run `eino-tui login`"
	usageText         = "Usage: eino-tui [--model <startup-model>]\n       eino-tui login\n       eino-tui status\n       eino-tui --help\n       eino-tui --version\n\nIn chat, press Alt+M to choose a model and reasoning effort for later turns."
)

func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, deps Dependencies) (code int) {
	options, err := Parse(args)
	if err != nil {
		fmt.Fprintln(stderr, "eino-tui: invalid command or model")
		fmt.Fprintln(stderr, usageText)
		return ExitStartup
	}
	var service runtimeui.Service
	var program Program
	defer func() {
		if recover() == nil {
			return
		}
		if program != nil {
			releaseProgram(program)
		}
		if service != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			_, _ = closeService(service, cleanupCtx)
			cancel()
		}
		fmt.Fprintln(stderr, app.FatalDiagnostic)
		code = ExitFatal
	}()
	switch options.Command {
	case CommandHelp:
		fmt.Fprintln(stdout, usageText)
		return ExitOK
	case CommandVersion:
		fmt.Fprintln(stdout, Version)
		return ExitOK
	case CommandStatus:
		manager := deps.NewSubscription(io.Discard)
		status, statusErr := manager.Status(ctx)
		if statusErr != nil {
			fmt.Fprintln(stderr, authDiagnostic)
			return ExitAuth
		}
		switch status {
		case subscription.LoggedIn:
			fmt.Fprintln(stdout, "logged in")
		case subscription.RefreshRequired:
			fmt.Fprintln(stdout, "logged in; refresh required on next request")
		default:
			fmt.Fprintln(stdout, "not logged in")
		}
		return ExitOK
	}

	commandCtx, stop := deps.SignalContext(ctx)
	defer stop()
	manager := deps.NewSubscription(stdout)
	if options.Command == CommandLogin {
		fmt.Fprintln(stdout, "Starting Codex device authorization…")
		if loginErr := manager.LoginDevice(commandCtx); loginErr != nil {
			if errors.Is(loginErr, context.Canceled) || errors.Is(loginErr, context.DeadlineExceeded) {
				fmt.Fprintln(stderr, loginInterrupted)
				return ExitInterrupted
			}
			fmt.Fprintln(stderr, loginDiagnostic)
			return ExitAuth
		}
		fmt.Fprintln(stdout, "Codex device authorization complete.")
		return ExitOK
	}
	status, err := manager.Status(commandCtx)
	if err != nil {
		fmt.Fprintln(stderr, authDiagnostic)
		return ExitAuth
	}
	if status == subscription.NotLoggedIn {
		fmt.Fprintln(stderr, notLoggedIn)
		return ExitAuth
	}
	httpClient, err := manager.HTTPClient(commandCtx)
	if err != nil {
		if errors.Is(err, codexauth.ErrNotLoggedIn) {
			fmt.Fprintln(stderr, notLoggedIn)
		} else {
			fmt.Fprintln(stderr, authDiagnostic)
		}
		return ExitAuth
	}
	cwd, err := deps.WorkingDirectory()
	if err != nil {
		fmt.Fprintln(stderr, startupDiagnostic)
		return ExitStartup
	}
	workspace, err := platform.CanonicalWorkspace(cwd)
	if err != nil {
		fmt.Fprintln(stderr, startupDiagnostic)
		return ExitStartup
	}
	stateDir, err := deps.StateDirectory()
	if err != nil {
		fmt.Fprintln(stderr, startupDiagnostic)
		return ExitStartup
	}
	paths, err := deps.PrepareState(commandCtx, stateDir)
	if err != nil {
		fmt.Fprintln(stderr, startupDiagnostic)
		return ExitStartup
	}
	resolver, err := deps.NewResolver(httpClient)
	if err != nil {
		fmt.Fprintln(stderr, startupDiagnostic)
		return ExitStartup
	}
	runtimeConfig := runtimeui.Config{
		Resolver:     resolver,
		AgentName:    "codex",
		SystemPrompt: "Be a helpful read-only repository assistant. You may read, list, glob, and search files only inside the workspace. Do not claim write, edit, shell, network, approval, or autonomous execution capabilities.",
	}
	service, err = deps.OpenService(commandCtx, paths.Database, platform.WorkspaceSessionID(workspace), workspace, runtimeConfig)
	if err != nil {
		if errors.Is(err, runtimeui.ErrToolsUnavailable) {
			fmt.Fprintln(stderr, toolsDiagnostic)
		} else {
			fmt.Fprintln(stderr, startupDiagnostic)
		}
		return ExitStartup
	}
	model, fatal := deps.NewApplication(commandCtx, service, stop, app.Config{
		Catalog: manager, InitialSelection: modelSelection(options.Model), InitialReasoningEffort: codexmodel.ReasoningEffortMedium,
	})
	program = deps.NewProgram(model, commandCtx, stdin, stdout)
	if program == nil {
		panic("nil program")
	}
	_, programErr := program.Run()
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	closeErr, closePanic := closeService(service, cleanupCtx)
	cancel()
	if closePanic {
		fmt.Fprintln(stderr, app.FatalDiagnostic)
		return ExitFatal
	}
	if closeErr != nil {
		fmt.Fprintln(stderr, forcedDiagnostic)
		return ExitForcedShutdown
	}
	if fatal.Marked() {
		fmt.Fprintln(stderr, app.FatalDiagnostic)
		return ExitFatal
	}
	if programErr != nil && !errors.Is(programErr, tea.ErrInterrupted) && !errors.Is(programErr, context.Canceled) {
		fmt.Fprintln(stderr, programDiagnostic)
		return ExitProgram
	}
	return ExitOK
}

func modelSelection(modelID string) model.Selection {
	return model.Selection{ProviderID: codexmodel.ProviderID, ModelID: model.ID(modelID)}
}

func closeService(service runtimeui.Service, ctx context.Context) (err error, recovered bool) {
	defer func() {
		if recover() != nil {
			err = nil
			recovered = true
		}
	}()
	return service.Close(ctx), false
}

func releaseProgram(program Program) {
	defer func() { _ = recover() }()
	_ = program.ReleaseTerminal()
}
