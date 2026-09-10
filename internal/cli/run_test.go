package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	agentmodel "github.com/mattsp1290/eino-agent/model"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/app"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
	"github.com/mattsp1290/eino-tui/internal/platform"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
	"github.com/mattsp1290/eino-tui/internal/subscription"
)

type testSubscription struct {
	status subscription.Status
}

type scriptedSubscription struct {
	login   func(context.Context) error
	status  func(context.Context) (subscription.Status, error)
	http    func(context.Context) (*http.Client, error)
	catalog func(context.Context) ([]codexmodel.CatalogEntry, error)
}

func (s scriptedSubscription) LoginDevice(ctx context.Context) error { return s.login(ctx) }
func (s scriptedSubscription) Status(ctx context.Context) (subscription.Status, error) {
	return s.status(ctx)
}
func (s scriptedSubscription) HTTPClient(ctx context.Context) (*http.Client, error) {
	return s.http(ctx)
}
func (s scriptedSubscription) ListModels(ctx context.Context) ([]codexmodel.CatalogEntry, error) {
	if s.catalog == nil {
		return nil, nil
	}
	return s.catalog(ctx)
}

func (s testSubscription) LoginDevice(context.Context) error { return nil }
func (s testSubscription) Status(context.Context) (subscription.Status, error) {
	return s.status, nil
}
func (testSubscription) HTTPClient(context.Context) (*http.Client, error) {
	return &http.Client{}, nil
}
func (testSubscription) ListModels(context.Context) ([]codexmodel.CatalogEntry, error) {
	return nil, nil
}

type testProgram struct {
	err          error
	panicValue   any
	releasePanic any
	released     bool
}

func (p *testProgram) Run() (tea.Model, error) {
	if p.panicValue != nil {
		panic(p.panicValue)
	}
	return nil, p.err
}
func (p *testProgram) ReleaseTerminal() error {
	p.released = true
	if p.releasePanic != nil {
		panic(p.releasePanic)
	}
	return nil
}

type testService struct {
	close  func(context.Context) error
	closed bool
}

func (*testService) Load(context.Context) (runtimeui.Snapshot, error) {
	return runtimeui.Snapshot{Phase: runtimeui.PhaseIdle}, nil
}
func (*testService) Start(context.Context, string, runtimeui.StartConfig) (runtimeui.ActionResult, error) {
	return runtimeui.ActionResult{}, nil
}
func (*testService) InterruptActive(context.Context) error { return nil }
func (*testService) Recover(context.Context) (runtimeui.ActionResult, error) {
	return runtimeui.ActionResult{}, nil
}
func (*testService) ListConversations(context.Context, string) (runtimeui.ConversationPage, error) {
	return runtimeui.ConversationPage{}, nil
}
func (*testService) CreateConversation(context.Context, uint64) (runtimeui.SelectionResult, error) {
	return runtimeui.SelectionResult{}, runtimeui.ErrUnavailable
}
func (*testService) SelectConversation(context.Context, session.ID, uint64) (runtimeui.SelectionResult, error) {
	return runtimeui.SelectionResult{}, runtimeui.ErrUnavailable
}
func (*testService) RenameConversation(context.Context, session.ID, uint64, string) (runtimeui.ConversationInfo, error) {
	return runtimeui.ConversationInfo{}, runtimeui.ErrUnavailable
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
	deps.NewApplication = func(context.Context, runtimeui.Service, context.CancelFunc, app.Config) (tea.Model, *app.Fatal) {
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
				deps.OpenService = func(context.Context, platform.Paths, platform.Workspace, runtimeui.Config) (runtimeui.Service, error) {
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
		NewSubscription:  func(io.Writer) Subscription { return testSubscription{status: subscription.LoggedIn} },
		SignalContext:    context.WithCancel,
		NewResolver:      codexmodel.NewResolver,
		OpenService: func(context.Context, platform.Paths, platform.Workspace, runtimeui.Config) (runtimeui.Service, error) {
			return service, nil
		},
		NewApplication: func(ctx context.Context, service runtimeui.Service, cancel context.CancelFunc, cfg app.Config) (tea.Model, *app.Fatal) {
			return app.Safe(app.New(ctx, service, cfg), cancel)
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
		{name: "panic during terminal release", program: &testProgram{panicValue: "secret /tmp/path", releasePanic: "secret release /tmp/path"}, service: &testService{}, want: ExitFatal, diagnostic: "internal application error"},
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

func TestRunToolCatalogFailureUsesFixedDiagnostic(t *testing.T) {
	service := &testService{}
	deps := testDeps(t, service, &testProgram{})
	deps.OpenService = func(context.Context, platform.Paths, platform.Workspace, runtimeui.Config) (runtimeui.Service, error) {
		return nil, fmt.Errorf("%w: TOKEN /private/workspace/missing-rg", runtimeui.ErrToolsUnavailable)
	}
	var stderr bytes.Buffer
	code := Run(context.Background(), nil, strings.NewReader(""), io.Discard, &stderr, deps)
	if code != ExitStartup || stderr.String() != toolsDiagnostic+"\n" || strings.Contains(stderr.String(), "TOKEN") || strings.Contains(stderr.String(), "/private/") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
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

func TestRunInvalidArgumentsDoNotInitialize(t *testing.T) {
	var stderr bytes.Buffer
	code := Run(context.Background(), []string{"--model", "gpt-5.5\nTOKEN"}, strings.NewReader(""), io.Discard, &stderr, Dependencies{})
	if code != ExitStartup || strings.Contains(stderr.String(), "TOKEN") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestRunStatusIsLocalAndPreSignal(t *testing.T) {
	for _, test := range []struct {
		status subscription.Status
		want   string
	}{
		{subscription.NotLoggedIn, "not logged in\n"},
		{subscription.LoggedIn, "logged in\n"},
		{subscription.RefreshRequired, "logged in; refresh required on next request\n"},
	} {
		var signalCalls int
		deps := Dependencies{
			NewSubscription: func(io.Writer) Subscription {
				return scriptedSubscription{
					status: func(context.Context) (subscription.Status, error) { return test.status, nil },
					login:  func(context.Context) error { panic("network login") },
					http:   func(context.Context) (*http.Client, error) { panic("refresh") },
				}
			},
			SignalContext: func(context.Context) (context.Context, context.CancelFunc) {
				signalCalls++
				panic("signal ownership")
			},
		}
		var stdout bytes.Buffer
		if code := Run(context.Background(), []string{"status"}, strings.NewReader(""), &stdout, io.Discard, deps); code != ExitOK || stdout.String() != test.want || signalCalls != 0 {
			t.Fatalf("status=%v code=%d stdout=%q signal=%d", test.status, code, stdout.String(), signalCalls)
		}
	}
}

func TestRunLoginUsesSignalContextAndFixedOutput(t *testing.T) {
	var signaled bool
	deps := Dependencies{
		SignalContext: func(ctx context.Context) (context.Context, context.CancelFunc) {
			signaled = true
			return context.WithCancel(ctx)
		},
		NewSubscription: func(io.Writer) Subscription {
			return scriptedSubscription{
				login: func(context.Context) error {
					if !signaled {
						t.Fatal("device login preceded signal context")
					}
					return nil
				},
				status: func(context.Context) (subscription.Status, error) { panic("status") },
				http:   func(context.Context) (*http.Client, error) { panic("http") },
			}
		},
	}
	var stdout bytes.Buffer
	if code := Run(context.Background(), []string{"login"}, strings.NewReader(""), &stdout, io.Discard, deps); code != ExitOK || !strings.Contains(stdout.String(), "authorization complete") {
		t.Fatalf("code=%d stdout=%q", code, stdout.String())
	}
}

func TestRunAuthFailuresUseFixedCodesAndDiagnostics(t *testing.T) {
	tests := []struct {
		name string
		args []string
		sub  scriptedSubscription
		want int
		text string
	}{
		{
			name: "status read", args: []string{"status"}, want: ExitAuth, text: authDiagnostic,
			sub: scriptedSubscription{status: func(context.Context) (subscription.Status, error) { return 0, errors.New("TOKEN /tmp/private") }},
		},
		{
			name: "login failure", args: []string{"login"}, want: ExitAuth, text: loginDiagnostic,
			sub: scriptedSubscription{login: func(context.Context) error { return errors.New("TOKEN /tmp/private") }},
		},
		{
			name: "login cancellation", args: []string{"login"}, want: ExitInterrupted, text: loginInterrupted,
			sub: scriptedSubscription{login: func(context.Context) error { return fmt.Errorf("wrapped: %w", context.Canceled) }},
		},
		{
			name: "authenticated client", want: ExitAuth, text: authDiagnostic,
			sub: scriptedSubscription{
				status: func(context.Context) (subscription.Status, error) { return subscription.LoggedIn, nil },
				http:   func(context.Context) (*http.Client, error) { return nil, errors.New("TOKEN /tmp/private") },
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			deps := Dependencies{SignalContext: context.WithCancel, NewSubscription: func(io.Writer) Subscription { return test.sub }}
			var stdout, stderr bytes.Buffer
			code := Run(context.Background(), test.args, strings.NewReader(""), &stdout, &stderr, deps)
			if code != test.want || !strings.Contains(stderr.String(), test.text) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			combined := stdout.String() + stderr.String()
			if strings.Contains(combined, "TOKEN") || strings.Contains(combined, "/tmp/private") {
				t.Fatalf("auth error leaked: %q", combined)
			}
		})
	}
}

func TestRunLoggedOutStopsBeforeTransportAndFilesystem(t *testing.T) {
	deps := Dependencies{
		SignalContext: context.WithCancel,
		NewSubscription: func(io.Writer) Subscription {
			return scriptedSubscription{
				status: func(context.Context) (subscription.Status, error) { return subscription.NotLoggedIn, nil },
				http:   func(context.Context) (*http.Client, error) { panic("HTTP client created") },
				login:  func(context.Context) error { panic("login") },
			}
		},
		WorkingDirectory: func() (string, error) { panic("filesystem touched") },
	}
	var stderr bytes.Buffer
	if code := Run(context.Background(), nil, strings.NewReader(""), io.Discard, &stderr, deps); code != ExitAuth || stderr.String() != notLoggedIn+"\n" {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestRunChatCompositionOrderAndIdentity(t *testing.T) {
	root := t.TempDir()
	client := &http.Client{}
	service := &testService{}
	program := &testProgram{}
	var calls []string
	var captured runtimeui.Config
	deps := Dependencies{
		SignalContext: func(ctx context.Context) (context.Context, context.CancelFunc) {
			calls = append(calls, "signal")
			return context.WithCancel(ctx)
		},
		NewSubscription: func(io.Writer) Subscription {
			calls = append(calls, "subscription")
			return scriptedSubscription{
				status: func(context.Context) (subscription.Status, error) {
					calls = append(calls, "status")
					return subscription.LoggedIn, nil
				},
				http:  func(context.Context) (*http.Client, error) { calls = append(calls, "http"); return client, nil },
				login: func(context.Context) error { panic("login") },
				catalog: func(context.Context) ([]codexmodel.CatalogEntry, error) {
					t.Fatal("catalog loaded during startup")
					return nil, nil
				},
			}
		},
		WorkingDirectory: func() (string, error) { calls = append(calls, "cwd"); return root, nil },
		StateDirectory:   func() (string, error) { calls = append(calls, "state"); return filepath.Join(root, "state"), nil },
		PrepareState: func(ctx context.Context, path string) (platform.Paths, error) {
			calls = append(calls, "prepare")
			return platform.PrepareState(ctx, path)
		},
		NewResolver: func(got *http.Client) (agentmodel.Resolver, error) {
			calls = append(calls, "resolver")
			if got != client {
				t.Fatalf("resolver input = %p", got)
			}
			return codexmodel.NewResolver(got)
		},
		OpenService: func(_ context.Context, _ platform.Paths, _ platform.Workspace, cfg runtimeui.Config) (runtimeui.Service, error) {
			calls = append(calls, "open")
			captured = cfg
			return service, nil
		},
		NewApplication: func(ctx context.Context, got runtimeui.Service, cancel context.CancelFunc, cfg app.Config) (tea.Model, *app.Fatal) {
			calls = append(calls, "app")
			if got != service || cfg.InitialSelection.ModelID != "gpt-5.6" || cfg.InitialSelection.ProviderID != codexmodel.ProviderID ||
				cfg.InitialReasoningEffort != codexmodel.ReasoningEffortMedium || cfg.Catalog == nil {
				t.Fatalf("application inputs = %T %#v", got, cfg)
			}
			return app.Safe(app.New(ctx, got, cfg), cancel)
		},
		NewProgram: func(tea.Model, context.Context, io.Reader, io.Writer) Program {
			calls = append(calls, "program")
			return program
		},
	}
	if code := Run(context.Background(), []string{"--model", "gpt-5.6"}, strings.NewReader(""), io.Discard, io.Discard, deps); code != ExitOK {
		t.Fatalf("code=%d", code)
	}
	want := []string{"signal", "subscription", "status", "http", "cwd", "state", "prepare", "resolver", "open", "app", "program"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls=%v want=%v", calls, want)
	}
	if captured.AgentName != "codex" {
		t.Fatalf("runtime config = %#v", captured)
	}
}
