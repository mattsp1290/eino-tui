package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/app"
	"github.com/mattsp1290/eino-tui/internal/platform"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
)

type testProgram struct {
	err        error
	panicValue any
	released   bool
}

func (p *testProgram) Run() (tea.Model, error) {
	if p.panicValue != nil {
		panic(p.panicValue)
	}
	return nil, p.err
}
func (p *testProgram) ReleaseTerminal() error { p.released = true; return nil }

type testService struct {
	close  func(context.Context) error
	closed bool
}

func (*testService) Load(context.Context) (runtimeui.Snapshot, error) {
	return runtimeui.Snapshot{Phase: runtimeui.PhaseIdle}, nil
}
func (*testService) Start(context.Context, string) (runtimeui.ActionResult, error) {
	return runtimeui.ActionResult{}, nil
}
func (*testService) InterruptActive(context.Context) error { return nil }
func (*testService) Recover(context.Context) (runtimeui.ActionResult, error) {
	return runtimeui.ActionResult{}, nil
}
func (s *testService) Close(ctx context.Context) error {
	s.closed = true
	if s.close != nil {
		return s.close(ctx)
	}
	return nil
}

func TestRunApplicationFactoryPanicIsRedactedAndClosesService(t *testing.T) {
	service := &testService{}
	program := &testProgram{}
	deps := testDeps(t, service, program)
	deps.NewApplication = func(context.Context, runtimeui.Service, context.CancelFunc) (tea.Model, *app.Fatal) {
		panic("secret /tmp/private")
	}
	var stderr bytes.Buffer
	code := Run(context.Background(), nil, strings.NewReader(""), io.Discard, &stderr, deps)
	if code != ExitFatal || !service.closed {
		t.Fatalf("code=%d closed=%v", code, service.closed)
	}
	if strings.Contains(stderr.String(), "secret") || strings.Contains(stderr.String(), "/tmp/private") {
		t.Fatalf("leak=%q", stderr.String())
	}
}

func TestRunLifecyclePanicsAreRedacted(t *testing.T) {
	for _, stage := range []string{"prepare state", "open service", "close service"} {
		t.Run(stage, func(t *testing.T) {
			service := &testService{}
			deps := testDeps(t, service, &testProgram{})
			switch stage {
			case "prepare state":
				deps.PrepareState = func(context.Context, string) (platform.Paths, error) {
					panic("secret /tmp/prepare")
				}
			case "open service":
				deps.OpenService = func(context.Context, string, session.ID, string) (runtimeui.Service, error) {
					panic("secret /tmp/open")
				}
			case "close service":
				service.close = func(context.Context) error { panic("secret /tmp/close") }
			}
			var stderr bytes.Buffer
			code := Run(context.Background(), nil, strings.NewReader(""), io.Discard, &stderr, deps)
			if code != ExitFatal || !strings.Contains(stderr.String(), app.FatalDiagnostic) {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
			if strings.Contains(stderr.String(), "secret") || strings.Contains(stderr.String(), "/tmp/") {
				t.Fatalf("panic leaked: %q", stderr.String())
			}
		})
	}
}

func testDeps(t *testing.T, service runtimeui.Service, program *testProgram) Dependencies {
	t.Helper()
	root := t.TempDir()
	return Dependencies{
		WorkingDirectory: func() (string, error) { return root, nil },
		StateDirectory:   func() (string, error) { return filepath.Join(root, "state"), nil },
		PrepareState:     platform.PrepareState,
		OpenService:      func(context.Context, string, session.ID, string) (runtimeui.Service, error) { return service, nil },
		NewApplication: func(ctx context.Context, service runtimeui.Service, cancel context.CancelFunc) (tea.Model, *app.Fatal) {
			return app.Safe(app.New(ctx, service), cancel)
		},
		NewProgram: func(tea.Model, context.Context, io.Reader, io.Writer) Program { return program },
	}
}

func TestRunExitPoliciesAndRedaction(t *testing.T) {
	tests := []struct {
		name       string
		program    *testProgram
		service    *testService
		want       int
		diagnostic string
	}{
		{name: "normal", program: &testProgram{}, service: &testService{}, want: ExitOK},
		{name: "program error", program: &testProgram{err: errors.New("secret /tmp/path")}, service: &testService{}, want: ExitProgram, diagnostic: programDiagnostic},
		{name: "panic", program: &testProgram{panicValue: "secret /tmp/path"}, service: &testService{}, want: ExitFatal, diagnostic: "internal application error"},
		{name: "forced close", program: &testProgram{}, service: &testService{close: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }}, want: ExitForcedShutdown, diagnostic: forcedDiagnostic},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errout bytes.Buffer
			got := Run(context.Background(), nil, strings.NewReader(""), &out, &errout, testDeps(t, tt.service, tt.program))
			if got != tt.want {
				t.Fatalf("exit=%d stderr=%q", got, errout.String())
			}
			if tt.diagnostic != "" && !strings.Contains(errout.String(), tt.diagnostic) {
				t.Fatalf("stderr=%q", errout.String())
			}
			if strings.Contains(errout.String(), "secret") || strings.Contains(errout.String(), "/tmp/path") {
				t.Fatalf("leak=%q", errout.String())
			}
			if tt.want == ExitFatal && !tt.program.released {
				t.Fatal("terminal not released")
			}
		})
	}
}

func TestRunHelpAndVersionDoNotInitialize(t *testing.T) {
	for _, arg := range []string{"--help", "--version"} {
		var out bytes.Buffer
		code := Run(context.Background(), []string{arg}, strings.NewReader(""), &out, &out, Dependencies{})
		if code != ExitOK || out.Len() == 0 {
			t.Fatalf("%s: %d %q", arg, code, out.String())
		}
	}
}
