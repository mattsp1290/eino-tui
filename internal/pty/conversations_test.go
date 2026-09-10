//go:build darwin || linux

package pty_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// containsDraftTwo reports whether the "unsent draft two" text is present.
// The textinput widget can wrap a long line's render across a horizontal
// scroll boundary, splitting the literal string with an intervening cursor
// reposition escape sequence, so this checks for its distinguishing words
// independently rather than requiring one contiguous substring match.
func containsDraftTwo(text string) bool {
	return strings.Contains(text, "unsent") && strings.Contains(text, "draft") && strings.Contains(text, "two")
}

// TestConversationsCreateSwitchAndRelaunchThroughSymlink exercises the
// multi-conversation lifecycle end to end: creating a second conversation,
// switching between conversations (including through the picker), draft
// retention across switches within one process, and durable state surviving
// a relaunch through a symlinked workspace path while in-memory drafts do
// not survive the relaunch.
//
// Chrome such as the header title, dialog titles, and the conversation
// picker share screen rows with whatever text previously occupied them.
// Bubbletea's renderer only rewrites the bytes that changed, so a plain
// substring match against the raw PTY stream can miss text whose leading
// characters happen to match the prior frame (e.g. "Codex subscription
// ready." and "Conversations · page 1" both start with "Co"). waitStatus
// sends SIGWINCH, which this program's alt-screen renderer always answers
// with a full, non-diffed repaint, so it is used here for every
// chrome/state assertion; plain waitText/waitTextAfter is reserved for
// content that is genuinely appended fresh (echoed keystrokes, streamed
// transcript text).
func TestConversationsCreateSwitchAndRelaunchThroughSymlink(t *testing.T) {
	temp := t.TempDir()
	fixture := filepath.Join(temp, "fixture")
	buildBinary(t, fixture, "./internal/pty/testcmd/eino-tui-fixture")
	workspace := t.TempDir()
	state := filepath.Join(t.TempDir(), "state")

	process := startTerminal(t, fixture, nil, workspace, state)
	process.waitText(t, "Codex subscription ready", 3*time.Second)
	process.waitStatus(t, "Conversation 1")

	process.write(t, "first question\r")
	process.waitText(t, "transport completed.", 3*time.Second)
	process.waitStatus(t, "Conversation 1 — first question")

	process.write(t, "\x1bn")
	process.waitStatus(t, "Conversation 2")
	// waitStatus returns as soon as the matched text is visible, which can
	// be before the trailing bytes of that same rendered frame have been
	// copied from the PTY. Settle, mark a clean boundary, then force one
	// more definitely-fresh full repaint and settle again so `fresh` holds
	// exactly one complete, current frame to inspect for a leak.
	time.Sleep(150 * time.Millisecond)
	clean := len(process.text())
	process.waitStatus(t, "Conversation 2")
	time.Sleep(150 * time.Millisecond)
	created := process.text()[clean:]
	if !strings.Contains(created, "a message…") {
		t.Fatalf("new conversation editor missing placeholder: %q", created)
	}
	if strings.Contains(created, "first question") {
		t.Fatalf("new conversation leaked prior transcript: %q", created)
	}

	afterCreate := len(process.text())
	process.write(t, "second question\r")
	process.waitTextAfter(t, afterCreate, "transport completed.", 3*time.Second)
	process.waitStatus(t, "Conversation 2 — second question")

	// Give the forced-redraw traffic above a moment to drain, type the
	// draft, then settle again before acting on it. All of these bytes are
	// queued for the app's input reader before Alt+S is ever sent, so the
	// captured textarea value is unaffected by timing; only the rendering
	// of individual keystrokes is racy (the textinput widget can echo a
	// long line's tail with an intervening cursor-reposition escape that
	// splits words unpredictably), so correctness is confirmed later by
	// round-tripping the draft through a conversation switch instead of by
	// scanning the raw stream for this exact moment.
	time.Sleep(100 * time.Millisecond)
	process.write(t, "unsent draft two")
	time.Sleep(300 * time.Millisecond)

	process.write(t, "\x1bs")
	process.waitStatus(t, "Conversations · page 1")
	if opened := process.text(); !strings.Contains(opened, "Conversation 2 — second question") || !strings.Contains(opened, "Conversation 1 — first question") {
		t.Fatalf("picker missing both conversations: %q", opened)
	}

	// Conversation 2 (current) is highlighted first; move down to
	// Conversation 1 and open it. The "Opening conversation…" dialog keeps
	// showing the outgoing "Conversation 2 — second question" header while
	// the switch is in flight, so a naive scan from before the keypress
	// would flag that expected transitional text as a leak. Settle onto one
	// definitely-fresh, fully-landed frame (as above) before inspecting it.
	process.write(t, "\x1b[B")
	process.write(t, "\r")
	process.waitStatus(t, "Conversation 1 — first question")
	time.Sleep(150 * time.Millisecond)
	settledAtOne := len(process.text())
	process.waitStatus(t, "Conversation 1 — first question")
	time.Sleep(150 * time.Millisecond)
	fresh := process.text()[settledAtOne:]
	if !strings.Contains(fresh, "first question") {
		t.Fatalf("conversation 1 transcript missing after switch: %q", fresh)
	}
	if strings.Contains(fresh, "second question") {
		t.Fatalf("conversation 1 view leaked conversation 2 transcript: %q", fresh)
	}

	process.write(t, "\x1bs")
	process.waitStatus(t, "Conversations · page 1")
	// Conversation 1 (current) sorts after Conversation 2 (newest first);
	// move up to highlight Conversation 2 and open it.
	switchToTwoAt := len(process.text())
	process.write(t, "\x1b[A")
	process.write(t, "\r")
	process.waitStatus(t, "Conversation 2 — second question")
	if reopened := process.text()[switchToTwoAt:]; !containsDraftTwo(reopened) {
		t.Fatalf("draft not restored on switch back: %q", reopened)
	}

	process.write(t, "\x03")
	if err := process.waitExit(t, 3*time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}

	symlinkPath := filepath.Join(t.TempDir(), "workspace-link")
	if err := os.Symlink(workspace, symlinkPath); err != nil {
		t.Fatal(err)
	}
	replay := startTerminal(t, fixture, nil, symlinkPath, state)
	replay.waitText(t, "Conversation 2 — second question", 5*time.Second)
	replay.waitText(t, "second question", 5*time.Second)
	replay.waitText(t, "Codex subscription ready", 5*time.Second)
	if containsDraftTwo(replay.text()) {
		t.Fatal("draft survived a relaunch")
	}
	replay.write(t, "\x04")
	if err := replay.waitExit(t, 3*time.Second); err != nil {
		t.Fatalf("replay exit: %v", err)
	}
}

// TestConversationRenameManualAndByAgent covers both rename paths: the
// manual rename editor (Alt+R) and the agent-driven rename_conversation
// tool, and confirms the resulting title is durable across a relaunch.
func TestConversationRenameManualAndByAgent(t *testing.T) {
	temp := t.TempDir()
	fixture := filepath.Join(temp, "fixture")
	buildBinary(t, fixture, "./internal/pty/testcmd/eino-tui-fixture")
	workspace := t.TempDir()
	state := filepath.Join(t.TempDir(), "state")

	process := startTerminal(t, fixture, []string{"--rename-tool"}, workspace, state)
	process.waitText(t, "Codex subscription ready", 3*time.Second)

	process.write(t, "name me\r")
	process.waitText(t, "rename_conversation", 3*time.Second)
	process.waitText(t, "completed", 3*time.Second)
	process.waitText(t, "Conversation renamed as requested.", 3*time.Second)
	process.waitStatus(t, "Agent renamed conversation")

	process.write(t, "\x1br")
	process.waitText(t, "Rename conversation", 3*time.Second)
	process.write(t, "\x15")
	process.write(t, "Release plan")
	process.write(t, "\r")
	process.waitStatus(t, "Release plan")

	process.write(t, "\x1bs")
	process.waitStatus(t, "Conversations · page 1")
	process.waitStatus(t, "Release plan")
	process.write(t, "\x1b")
	process.waitStatus(t, "Codex subscription ready")

	process.write(t, "\x03")
	if err := process.waitExit(t, 3*time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}

	replay := startTerminal(t, fixture, nil, workspace, state)
	replay.waitText(t, "Release plan", 5*time.Second)
	replay.write(t, "\x04")
	if err := replay.waitExit(t, 3*time.Second); err != nil {
		t.Fatalf("replay exit: %v", err)
	}
}

// TestConversationSwitchingIsRefusedWhileBusy confirms Alt+S/Alt+N are
// refused with a hint while a turn is streaming, and are available again
// once the turn is interrupted.
//
// The --long fixture only lets the very first streamed chunk of the whole
// process return immediately; every chunk after that (including the 2nd and
// 3rd chunks of the very first turn) blocks for 10s. So the first turn sent
// in this test is used directly as the "busy" turn rather than warming up
// with a turn that is expected to fully complete quickly.
func TestConversationSwitchingIsRefusedWhileBusy(t *testing.T) {
	temp := t.TempDir()
	fixture := filepath.Join(temp, "fixture")
	buildBinary(t, fixture, "./internal/pty/testcmd/eino-tui-fixture")
	process := startTerminal(t, fixture, []string{"--long"}, t.TempDir(), filepath.Join(t.TempDir(), "state"))
	process.waitText(t, "Codex subscription ready", 3*time.Second)

	process.write(t, "busy one\r")
	process.waitStatus(t, "Streaming Codex response")

	process.write(t, "\x1bs")
	process.waitStatus(t, "Finish the response or press Esc to interrupt it before changing conversations.")

	start := len(process.text())
	time.Sleep(100 * time.Millisecond)
	if output := process.text(); start < len(output) && strings.Contains(output[start:], "Conversations · page 1") {
		t.Fatal("conversation picker opened during an active turn")
	}

	process.write(t, "\x1b")
	process.waitText(t, "[interrupted]", 3*time.Second)

	// The status line keeps showing "Response interrupted." (it is only
	// cleared by submitting the next turn), so Alt+N's availability is
	// exercised directly rather than waiting for the idle status text to
	// return.
	process.write(t, "\x1bn")
	process.waitStatus(t, "Conversation 2")

	process.write(t, "\x03")
	if err := process.waitExit(t, 3*time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
}

// TestSeparateWorkspacesHaveIndependentConversations confirms conversation
// state is scoped per workspace even when the state directory is shared.
func TestSeparateWorkspacesHaveIndependentConversations(t *testing.T) {
	temp := t.TempDir()
	fixture := filepath.Join(temp, "fixture")
	buildBinary(t, fixture, "./internal/pty/testcmd/eino-tui-fixture")
	state := filepath.Join(t.TempDir(), "state")

	workspaceA := t.TempDir()
	first := startTerminal(t, fixture, nil, workspaceA, state)
	first.waitText(t, "Codex subscription ready", 3*time.Second)
	first.write(t, "alpha prompt\r")
	first.waitText(t, "transport completed.", 3*time.Second)
	first.write(t, "\x03")
	if err := first.waitExit(t, 3*time.Second); err != nil {
		t.Fatalf("first exit: %v", err)
	}

	workspaceB := t.TempDir()
	second := startTerminal(t, fixture, nil, workspaceB, state)
	second.waitText(t, "Codex subscription ready", 3*time.Second)
	second.waitStatus(t, "Conversation 1")
	if strings.Contains(second.text(), "alpha prompt") {
		t.Fatalf("workspace B saw workspace A conversation: %q", second.text())
	}

	pickerOpenAt := len(second.text())
	second.write(t, "\x1bs")
	second.waitStatus(t, "Conversations · page 1")
	if opened := second.text()[pickerOpenAt:]; strings.Contains(opened, "alpha prompt") {
		t.Fatalf("workspace B directory listed workspace A conversation: %q", opened)
	}
	second.write(t, "\x1b")
	second.waitStatus(t, "Codex subscription ready")
	second.write(t, "\x04")
	if err := second.waitExit(t, 3*time.Second); err != nil {
		t.Fatalf("second exit: %v", err)
	}
}

// TestConversationDialogsRestoreTerminalOnQuitAndPanicFreeResize confirms the
// conversation picker survives extreme resizes without panicking and that
// terminal modes are restored on quit.
func TestConversationDialogsRestoreTerminalOnQuitAndPanicFreeResize(t *testing.T) {
	temp := t.TempDir()
	fixture := filepath.Join(temp, "fixture")
	buildBinary(t, fixture, "./internal/pty/testcmd/eino-tui-fixture")
	process := startTerminal(t, fixture, nil, t.TempDir(), filepath.Join(t.TempDir(), "state"))
	process.waitText(t, "Codex subscription ready", 3*time.Second)

	process.write(t, "\x1bs")
	process.waitStatus(t, "Conversations · page 1")

	if err := pty.Setsize(process.file, &pty.Winsize{Rows: 8, Cols: 18}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	select {
	case <-process.done:
		t.Fatalf("process exited after 8x18 resize: %v\n%s", process.exitError(), process.text())
	default:
	}

	if err := pty.Setsize(process.file, &pty.Winsize{Rows: 1, Cols: 20}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	select {
	case <-process.done:
		t.Fatalf("process exited after 1x20 resize: %v\n%s", process.exitError(), process.text())
	default:
	}
	if strings.Contains(process.text(), "panic") {
		t.Fatalf("panic during extreme resize: %q", process.text())
	}

	if err := pty.Setsize(process.file, &pty.Winsize{Rows: 28, Cols: 100}); err != nil {
		t.Fatal(err)
	}
	process.write(t, "\x1b")
	process.waitStatus(t, "Codex subscription ready")

	process.write(t, "\x03")
	if err := process.waitExit(t, 3*time.Second); err != nil {
		t.Fatalf("exit: %v", err)
	}
	output := process.text()
	if !strings.Contains(output, "\x1b[?1049l") {
		t.Fatalf("alternate screen not restored: %q", output)
	}
	if strings.Contains(output, "panic") || strings.Contains(output, "eino-tui stopped") {
		t.Fatalf("panic-free resize regressed: %q", output)
	}
}
