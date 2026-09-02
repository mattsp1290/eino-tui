package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/signal"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/mattsp1290/eino-tui/internal/app"
	"github.com/mattsp1290/eino-tui/internal/platform"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
)

const Version = "0.1.0-demo"

const (
	ExitOK             = 0
	ExitStartup        = 2
	ExitProgram        = 3
	ExitFatal          = 4
	ExitForcedShutdown = 5
)

const (
	startupDiagnostic = "eino-tui could not start; check workspace and state permissions"
	programDiagnostic = "eino-tui stopped because the terminal program failed"
	forcedDiagnostic  = "eino-tui forced shutdown; the unfinished turn will be recovered on next launch"
)

func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, deps Dependencies) (code int) {
	var service runtimeui.Service
	var program Program
	defer func() {
		if recover() == nil {
			return
		}
		if program != nil {
			_ = program.ReleaseTerminal()
		}
		if service != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			_, _ = closeService(service, cleanupCtx)
			cancel()
		}
		fmt.Fprintln(stderr, app.FatalDiagnostic)
		code = ExitFatal
	}()
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help":
			fmt.Fprintln(stdout, "Usage: eino-tui [--help|--version]")
			return ExitOK
		case "-v", "--version":
			fmt.Fprintln(stdout, Version)
			return ExitOK
		default:
			fmt.Fprintln(stderr, "eino-tui: unsupported argument")
			return ExitStartup
		}
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
	paths, err := deps.PrepareState(ctx, stateDir)
	if err != nil {
		fmt.Fprintln(stderr, startupDiagnostic)
		return ExitStartup
	}
	appCtx, stop := signal.NotifyContext(ctx, platform.Signals()...)
	defer stop()
	service, err = deps.OpenService(appCtx, paths.Database, platform.WorkspaceSessionID(workspace), workspace)
	if err != nil {
		fmt.Fprintln(stderr, startupDiagnostic)
		return ExitStartup
	}
	var fatal *app.Fatal
	var constructionPanic bool
	program, fatal, constructionPanic = buildProgram(deps, appCtx, service, stop, stdin, stdout)
	if constructionPanic {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		_, _ = closeService(service, cleanupCtx)
		cancel()
		fmt.Fprintln(stderr, app.FatalDiagnostic)
		return ExitFatal
	}
	_, programErr, recovered := runProgram(program)
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
	if recovered || fatal.Marked() {
		fmt.Fprintln(stderr, app.FatalDiagnostic)
		return ExitFatal
	}
	if programErr != nil && !errors.Is(programErr, tea.ErrInterrupted) && !errors.Is(programErr, context.Canceled) {
		fmt.Fprintln(stderr, programDiagnostic)
		return ExitProgram
	}
	return ExitOK
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

func buildProgram(deps Dependencies, ctx context.Context, service runtimeui.Service, cancel context.CancelFunc, stdin io.Reader, stdout io.Writer) (program Program, fatal *app.Fatal, recovered bool) {
	defer func() {
		if recover() != nil {
			program = nil
			fatal = nil
			recovered = true
		}
	}()
	model, fatal := deps.NewApplication(ctx, service, cancel)
	program = deps.NewProgram(model, ctx, stdin, stdout)
	if program == nil {
		panic("nil program")
	}
	return program, fatal, false
}

func runProgram(program Program) (model tea.Model, err error, recovered bool) {
	defer func() {
		if recover() != nil {
			recovered = true
			_ = program.ReleaseTerminal()
		}
	}()
	model, err = program.Run()
	return model, err, false
}
