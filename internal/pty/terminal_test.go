//go:build darwin || linux

package pty_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/mattsp1290/eino-tui/internal/demomodel"
	"github.com/mattsp1290/eino-tui/internal/platform"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
)

type terminalProcess struct {
	cmd     *exec.Cmd
	file    *os.File
	mu      sync.Mutex
	output  bytes.Buffer
	done    chan struct{}
	waitErr error
}

func buildBinary(t *testing.T, output, pkg string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-o", output, pkg)
	cmd.Dir = repositoryRoot(t)
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", pkg, err, data)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func startTerminal(t *testing.T, binary string, args []string, workspace, state string) *terminalProcess {
	t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "EINO_TUI_STATE_DIR="+state)
	file, err := pty.StartWithSize(cmd, &pty.Winsize{Rows: 28, Cols: 100})
	if err != nil {
		t.Fatal(err)
	}
	process := &terminalProcess{cmd: cmd, file: file, done: make(chan struct{})}
	go func() { _, _ = io.Copy(lockedWriter{process}, file) }()
	go func() {
		err := cmd.Wait()
		process.mu.Lock()
		process.waitErr = err
		process.mu.Unlock()
		close(process.done)
	}()
	t.Cleanup(func() {
		_ = file.Close()
		select {
		case <-process.done:
			return
		default:
		}
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			select {
			case <-process.done:
			case <-time.After(time.Second):
			}
		}
	})
	return process
}

type lockedWriter struct{ process *terminalProcess }

func (w lockedWriter) Write(data []byte) (int, error) {
	w.process.mu.Lock()
	defer w.process.mu.Unlock()
	return w.process.output.Write(data)
}

func (p *terminalProcess) text() string { p.mu.Lock(); defer p.mu.Unlock(); return p.output.String() }
func (p *terminalProcess) exitError() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.waitErr
}
func (p *terminalProcess) write(t *testing.T, data string) {
	t.Helper()
	if _, err := io.WriteString(p.file, data); err != nil {
		t.Fatal(err)
	}
}
func (p *terminalProcess) waitText(t *testing.T, needle string, timeout time.Duration) string {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			text := p.text()
			if strings.Contains(text, needle) {
				return text
			}
		case <-p.done:
			t.Fatalf("process exited before %q: %v\n%s", needle, p.exitError(), p.text())
		case <-deadline.C:
			t.Fatalf("timeout waiting for %q\n%s", needle, p.text())
		}
	}
}
func (p *terminalProcess) waitTextAfter(t *testing.T, start int, needle string, timeout time.Duration) string {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-tick.C:
			output := p.text()
			if start < len(output) && strings.Contains(output[start:], needle) {
				return output[start:]
			}
		case <-p.done:
			t.Fatalf("process exited before new %q: %v\n%s", needle, p.exitError(), p.text())
		case <-deadline.C:
			t.Fatalf("timeout waiting for new %q\n%s", needle, p.text())
		}
	}
}
func (p *terminalProcess) waitExit(t *testing.T, timeout time.Duration) error {
	t.Helper()
	select {
	case <-p.done:
		return p.exitError()
	case <-time.After(timeout):
		t.Fatalf("process did not exit\n%s", p.text())
		return nil
	}
}

func TestProductionBinaryNoAuthCommandsNeverAcquireTerminal(t *testing.T) {
	temp := t.TempDir()
	production := filepath.Join(temp, "eino-tui")
	buildBinary(t, production, "./cmd/eino-tui")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "help", args: []string{"--help"}, want: "Usage: eino-tui"},
		{name: "version", args: []string{"--version"}, want: "0.3.0"},
		{name: "invalid model", args: []string{"--model", "gpt-5.5\nTOKEN"}, want: "invalid command or model"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			process := startTerminal(t, production, test.args, t.TempDir(), filepath.Join(t.TempDir(), "state"))
			_ = process.waitExit(t, 3*time.Second)
			output := process.text()
			if !strings.Contains(output, test.want) || strings.Contains(output, "TOKEN") || strings.Contains(output, "\x1b[?1049h") {
				t.Fatalf("production no-auth output = %q", output)
			}
		})
	}
}

func TestFixtureAuthCommandsStayOutsideAlternateScreen(t *testing.T) {
	temp := t.TempDir()
	fixture := filepath.Join(temp, "fixture")
	buildBinary(t, fixture, "./internal/pty/testcmd/eino-tui-fixture")
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "login", args: []string{"login"}, want: "SAFE-1234"},
		{name: "status", args: []string{"status"}, want: "logged in"},
		{name: "logged out chat", args: []string{"--logged-out"}, want: "run `eino-tui login`"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			process := startTerminal(t, fixture, test.args, t.TempDir(), filepath.Join(t.TempDir(), "state"))
			_ = process.waitExit(t, 3*time.Second)
			output := process.text()
			if !strings.Contains(output, test.want) || strings.Contains(output, "\x1b[?1049h") {
				t.Fatalf("auth output = %q", output)
			}
		})
	}
}

func TestFixtureTerminalStreamingReplayAndRestoration(t *testing.T) {
	temp := t.TempDir()
	fixture := filepath.Join(temp, "fixture")
	buildBinary(t, fixture, "./internal/pty/testcmd/eino-tui-fixture")
	workspace := t.TempDir()
	state := filepath.Join(t.TempDir(), "state")
	process := startTerminal(t, fixture, nil, workspace, state)
	process.waitText(t, "Codex subscription", 3*time.Second)
	process.write(t, "hello λ\r")
	process.waitText(t, "response:", 3*time.Second)
	process.waitText(t, "transport completed.", 3*time.Second)
	process.write(t, "\x03")
	if err := process.waitExit(t, 3*time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
	output := process.text()
	if !strings.Contains(output, "\x1b[?1049h") || !strings.Contains(output, "\x1b[?1049l") {
		t.Fatalf("alternate screen not restored: %q", output)
	}
	if !strings.Contains(output, "\x1b[?2004h") || !strings.Contains(output, "\x1b[?2004l") || !strings.Contains(output, "\x1b[?25h") {
		t.Fatalf("paste/cursor modes not restored: %q", output)
	}

	replay := startTerminal(t, fixture, nil, workspace, state)
	replay.waitText(t, "hello λ", 3*time.Second)
	replay.waitText(t, "Codex fixture response:", 3*time.Second)
	replay.write(t, "\x04")
	if err := replay.waitExit(t, 3*time.Second); err != nil {
		t.Fatalf("replay exit: %v", err)
	}
}

func TestFixtureReadOnlyToolActivityAndReplay(t *testing.T) {
	temp := t.TempDir()
	fixtureBinary := filepath.Join(temp, "fixture")
	buildBinary(t, fixtureBinary, "./internal/pty/testcmd/eino-tui-fixture")
	workspace := t.TempDir()
	const privateOutput = "FIXTURE_CONTENT_MUST_NOT_BE_DIRECTLY_RENDERED"
	if err := os.WriteFile(filepath.Join(workspace, "fixture.txt"), []byte(privateOutput), 0o600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "state")
	process := startTerminal(t, fixtureBinary, []string{"--tool-read"}, workspace, state)
	process.waitText(t, "Codex subscription", 3*time.Second)
	if err := pty.Setsize(process.file, &pty.Winsize{Rows: 8, Cols: 18}); err != nil {
		t.Fatal(err)
	}
	process.write(t, "inspect fixture\r")
	process.waitText(t, "file_read", 3*time.Second)
	process.waitText(t, "completed", 3*time.Second)
	if strings.Contains(process.text(), privateOutput) {
		t.Fatal("tool output was directly rendered")
	}
	if err := pty.Setsize(process.file, &pty.Winsize{Rows: 28, Cols: 100}); err != nil {
		t.Fatal(err)
	}
	process.waitText(t, "read successfully", 3*time.Second)
	process.write(t, "\x03")
	if err := process.waitExit(t, 3*time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
	if output := process.text(); !strings.Contains(output, "\x1b[?1049l") || strings.Contains(output, privateOutput) {
		t.Fatalf("terminal was not restored or output leaked: %q", output)
	}

	replay := startTerminal(t, fixtureBinary, []string{"--tool-read"}, workspace, state)
	replay.waitText(t, "file_read", 3*time.Second)
	replay.waitText(t, "completed", 3*time.Second)
	if strings.Contains(replay.text(), privateOutput) {
		t.Fatal("replayed tool output was directly rendered")
	}
	replay.write(t, "\x04")
	if err := replay.waitExit(t, 3*time.Second); err != nil {
		t.Fatalf("replay exit: %v", err)
	}
}

func TestModelSelectorLoadsLazilyAndCancelKeepsPromptUsable(t *testing.T) {
	temp := t.TempDir()
	fixture := filepath.Join(temp, "fixture")
	buildBinary(t, fixture, "./internal/pty/testcmd/eino-tui-fixture")
	process := startTerminal(t, fixture, []string{"--block-first-catalog"}, t.TempDir(), filepath.Join(t.TempDir(), "state"))
	process.waitText(t, "Codex subscription ready", 3*time.Second)
	if strings.Contains(process.text(), "Loading account model catalog") {
		t.Fatal("catalog loaded before selector open")
	}
	process.write(t, "\x1bm")
	process.waitText(t, "Loading account model catalog", 3*time.Second)
	closedAt := len(process.text())
	process.write(t, "\x1b")
	process.waitTextAfter(t, closedAt, "Codex subscription ready", 3*time.Second)
	process.write(t, "usable after catalog cancel\r")
	process.waitText(t, "selection gpt-5.5 · medium", 3*time.Second)
	process.write(t, "\x03")
	if err := process.waitExit(t, 3*time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
}

func TestModelSelectorAppliesNextTurnAndRefreshCancelPreservesSecondDraft(t *testing.T) {
	temp := t.TempDir()
	fixture := filepath.Join(temp, "fixture")
	buildBinary(t, fixture, "./internal/pty/testcmd/eino-tui-fixture")
	process := startTerminal(t, fixture, []string{"--block-catalog-refresh"}, t.TempDir(), filepath.Join(t.TempDir(), "state"))
	process.waitText(t, "gpt-5.5 (gpt-5.5) · medium", 3*time.Second)
	process.write(t, "\x1b[200~first line\nsecond line\x1b[201~")
	process.write(t, "\x1bm")
	process.waitText(t, "GPT-5.5 Fixture", 3*time.Second)
	process.waitText(t, "GPT-5.6 Fixture", 3*time.Second)
	process.write(t, "\x1b[B")
	process.write(t, "\t")
	process.write(t, "\r")
	process.waitText(t, "GPT-5.6 Fixture (gpt-5.6) · high", 3*time.Second)
	process.waitText(t, "first line", 3*time.Second)
	process.write(t, "\r")
	process.waitText(t, "selection gpt-5.6 · high", 3*time.Second)
	process.waitText(t, "transport completed.", 3*time.Second)

	draftAt := len(process.text())
	process.write(t, "\x1b[200~next draft\nkept intact\x1b[201~")
	process.waitTextAfter(t, draftAt, "kept intact", 3*time.Second)
	openAt := len(process.text())
	process.write(t, "\x1bm")
	process.waitTextAfter(t, openAt, "Model & reasoning", 3*time.Second)
	start := len(process.text())
	process.write(t, "r")
	process.waitTextAfter(t, start, "Loading account model catalog", 3*time.Second)
	closeAt := len(process.text())
	process.write(t, "\x1b")
	process.waitTextAfter(t, closeAt, "Codex subscription ready", 3*time.Second)
	process.waitTextAfter(t, start, "next draft", 3*time.Second)
	if !strings.Contains(process.text(), "GPT-5.6 Fixture (gpt-5.6) · high") {
		t.Fatal("refresh cancel lost applied selection")
	}
	process.write(t, "\x03")
	if err := process.waitExit(t, 3*time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
	output := process.text()
	if !strings.Contains(output, "\x1b[?1049h") || !strings.Contains(output, "\x1b[?1049l") || !strings.Contains(output, "\x1b[?2004l") || !strings.Contains(output, "\x1b[?25h") {
		t.Fatalf("terminal modes not restored: %q", output)
	}
}

func TestModelSelectorIsSuppressedDuringBusyTurn(t *testing.T) {
	temp := t.TempDir()
	fixture := filepath.Join(temp, "fixture")
	buildBinary(t, fixture, "./internal/pty/testcmd/eino-tui-fixture")
	process := startTerminal(t, fixture, []string{"--long"}, t.TempDir(), filepath.Join(t.TempDir(), "state"))
	process.waitText(t, "Codex subscription ready", 3*time.Second)
	process.write(t, "busy selector test\r")
	process.waitText(t, "selection gpt-5.5 · medium", 3*time.Second)
	start := len(process.text())
	process.write(t, "\x1bm")
	time.Sleep(100 * time.Millisecond)
	if output := process.text(); start < len(output) && strings.Contains(output[start:], "Model & reasoning") {
		t.Fatal("selector opened during active turn")
	}
	process.write(t, "\x1b")
	process.waitText(t, "[interrupted]", 3*time.Second)
	process.write(t, "\x03")
	if err := process.waitExit(t, 3*time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
}

func TestInterruptHardKillRecoveryAndPanicPolicies(t *testing.T) {
	temp := t.TempDir()
	fixture := filepath.Join(temp, "fixture")
	buildBinary(t, fixture, "./internal/pty/testcmd/eino-tui-fixture")
	workspace := t.TempDir()
	state := filepath.Join(t.TempDir(), "state")

	interrupted := startTerminal(t, fixture, []string{"--long"}, workspace, state)
	interrupted.waitText(t, "Codex subscription", 3*time.Second)
	interrupted.write(t, "interrupt this\r")
	interrupted.waitText(t, "interrupt this", 3*time.Second)
	interrupted.write(t, "\x04")
	time.Sleep(100 * time.Millisecond)
	select {
	case <-interrupted.done:
		t.Fatal("ctrl+d quit during an active run")
	default:
	}
	interrupted.write(t, "\x1b")
	interrupted.waitText(t, "[interrupted]", 3*time.Second)
	interrupted.write(t, "another prompt\r")
	interrupted.waitText(t, "another prompt", 3*time.Second)
	interrupted.write(t, "\x1b")
	time.Sleep(100 * time.Millisecond)
	interrupted.write(t, "\x03")
	if err := interrupted.waitExit(t, 3*time.Second); err != nil {
		t.Fatalf("interrupt exit: %v", err)
	}

	killed := startTerminal(t, fixture, []string{"--long"}, workspace, state)
	killed.waitText(t, "Codex subscription", 3*time.Second)
	killed.write(t, "hard kill turn\r")
	killed.waitText(t, "hard kill turn", 3*time.Second)
	if err := killed.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = killed.waitExit(t, 2*time.Second)

	recovered := startTerminal(t, fixture, nil, workspace, state)
	recovered.waitText(t, "Waiting to recover", 3*time.Second)
	recovered.waitText(t, "Response interrupted.", 8*time.Second)
	recovered.write(t, "after recovery\r")
	recovered.waitText(t, "transport completed.", 3*time.Second)
	recovered.write(t, "\x03")
	if err := recovered.waitExit(t, 3*time.Second); err != nil {
		t.Fatalf("recovered exit: %v", err)
	}

	for _, mode := range []string{"--app-init-panic", "--app-update-panic", "--app-view-panic", "--app-command-panic", "--program-panic", "--program-error"} {
		t.Run(mode, func(t *testing.T) {
			process := startTerminal(t, fixture, []string{mode}, t.TempDir(), filepath.Join(t.TempDir(), "state"))
			_ = process.waitExit(t, 4*time.Second)
			output := process.text()
			if strings.Contains(output, "secret") || strings.Contains(output, "/tmp/private") {
				t.Fatalf("panic leaked: %q", output)
			}
			if !strings.Contains(output, "eino-tui stopped") {
				t.Fatalf("fixed diagnostic missing: %q", output)
			}
		})
	}
	modelFailure := startTerminal(t, fixture, []string{"--model-error"}, t.TempDir(), filepath.Join(t.TempDir(), "state"))
	modelFailure.waitText(t, "Codex subscription", 3*time.Second)
	modelFailure.write(t, "do not leak this\r")
	modelFailure.waitText(t, "Codex provider could not complete", 3*time.Second)
	modelFailure.write(t, "\x03")
	_ = modelFailure.waitExit(t, 3*time.Second)
	if output := modelFailure.text(); strings.Contains(output, "secret prompt") || strings.Contains(output, "/tmp/private") {
		t.Fatalf("model error leaked: %q", output)
	}

	wedged := startTerminal(t, fixture, []string{"--wedged-close"}, t.TempDir(), filepath.Join(t.TempDir(), "state"))
	wedged.waitText(t, "Codex subscription", 3*time.Second)
	wedged.write(t, "\x03")
	err := wedged.waitExit(t, 4*time.Second)
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 5 {
		t.Fatalf("wedged exit=%v", err)
	}
	if output := wedged.text(); !strings.Contains(output, "forced shutdown") || strings.Contains(output, "secret") {
		t.Fatalf("wedged output=%q", output)
	}
}

func TestActiveCtrlCAndSIGTERMSettleBeforeExit(t *testing.T) {
	temp := t.TempDir()
	fixture := filepath.Join(temp, "fixture")
	buildBinary(t, fixture, "./internal/pty/testcmd/eino-tui-fixture")
	for _, scenario := range []struct {
		name string
		stop func(*terminalProcess) error
	}{
		{name: "ctrl-c", stop: func(process *terminalProcess) error { _, err := io.WriteString(process.file, "\x03"); return err }},
		{name: "sigterm", stop: func(process *terminalProcess) error { return process.cmd.Process.Signal(syscall.SIGTERM) }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			workspace := t.TempDir()
			state := filepath.Join(t.TempDir(), "state")
			process := startTerminal(t, fixture, []string{"--long"}, workspace, state)
			process.waitText(t, "Codex subscription", 3*time.Second)
			prompt := "active " + scenario.name
			process.write(t, prompt+"\r")
			process.waitText(t, prompt, 3*time.Second)
			started := time.Now()
			if err := scenario.stop(process); err != nil {
				t.Fatal(err)
			}
			if err := process.waitExit(t, 3*time.Second); err != nil {
				t.Fatalf("signal exit: %v", err)
			}
			if time.Since(started) > 2500*time.Millisecond {
				t.Fatal("orderly shutdown exceeded budget")
			}
			replay := startTerminal(t, fixture, nil, workspace, state)
			replay.waitText(t, prompt, 3*time.Second)
			replay.waitText(t, "[interrupted]", 3*time.Second)
			if strings.Contains(replay.text(), "Waiting to recover") {
				t.Fatal("graceful signal left recovery-waiting state")
			}
			replay.write(t, "\x04")
			if err := replay.waitExit(t, 3*time.Second); err != nil {
				t.Fatalf("replay exit: %v", err)
			}
		})
	}
}

func TestResizeAndBracketedMultilinePaste(t *testing.T) {
	temp := t.TempDir()
	fixture := filepath.Join(temp, "fixture")
	buildBinary(t, fixture, "./internal/pty/testcmd/eino-tui-fixture")
	workspace := t.TempDir()
	state := filepath.Join(t.TempDir(), "state")
	process := startTerminal(t, fixture, nil, workspace, state)
	process.waitText(t, "Codex subscription", 3*time.Second)
	if err := pty.Setsize(process.file, &pty.Winsize{Rows: 8, Cols: 18}); err != nil {
		t.Fatal(err)
	}
	process.write(t, "\x1b[200~first\n界 second\x1b[201~")
	time.Sleep(100 * time.Millisecond)
	if strings.Contains(process.text(), "Codex fixture response:") {
		t.Fatal("paste submitted implicitly")
	}
	process.write(t, "\r")
	process.waitText(t, "response:", 3*time.Second)
	time.Sleep(300 * time.Millisecond)
	process.write(t, "alt one\x1b\ralt two\r")
	time.Sleep(500 * time.Millisecond)
	if err := pty.Setsize(process.file, &pty.Winsize{Rows: 30, Cols: 120}); err != nil {
		t.Fatal(err)
	}
	process.write(t, "\x03")
	if err := process.waitExit(t, 3*time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
	canonical, err := platform.CanonicalWorkspace(workspace)
	if err != nil {
		t.Fatal(err)
	}
	chat, err := runtimeui.Open(context.Background(), filepath.Join(state, "sessions.db"), platform.WorkspaceSessionID(canonical), canonical, runtimeui.Config{
		Resolver:  demomodel.DynamicResolver(nil),
		AgentName: "fixture", SystemPrompt: "Return only the configured deterministic fixture response.",
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := chat.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Messages) != 4 || snapshot.Messages[0].Content != "first\n界 second" || snapshot.Messages[2].Content != "alt one\nalt two" {
		t.Fatalf("durable multiline history=%#v", snapshot.Messages)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := chat.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}
