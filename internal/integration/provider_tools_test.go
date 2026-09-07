package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattsp1290/eino-agent/session"
	agentsqlite "github.com/mattsp1290/eino-agent/store/sqlite"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
	"github.com/mattsp1290/eino-tui/internal/platform"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
)

func TestCodexFileReadToolRoundTripAndSQLiteReplay(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	const fixture = "hello from a Unicode fixture: 雀\n"
	if err := os.WriteFile(filepath.Join(workspace, "fixture.txt"), []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	transport := newFileReadToolTransport()
	sessionID := platform.WorkspaceSessionID(workspace)
	service := openCodexFixture(t, ctx, paths.Database, sessionID, workspace, &http.Client{Transport: transport})
	started, err := service.Start(ctx, "read the fixture", codexStartConfig(codexmodel.DefaultModel, codexmodel.ReasoningEffortMedium))
	if err != nil {
		t.Fatal(err)
	}
	terminal := drainProviderRun(t, started.Run)
	if terminal.Notice != "" || len(terminal.LiveMessages) != 0 || len(terminal.Messages) != 3 {
		t.Fatalf("terminal shape = %#v", terminal)
	}
	requestMessage := terminal.Messages[1]
	if requestMessage.Content != "I’ll inspect it." || len(requestMessage.Tools) != 1 {
		t.Fatalf("tool request projection = %#v", requestMessage)
	}
	activity := requestMessage.Tools[0]
	if activity.ID != "call_read_1" || activity.Name != "file_read" || activity.Subject != "fixture.txt" || activity.Status != runtimeui.ToolCompleted {
		t.Fatalf("activity = %#v", activity)
	}
	if terminal.Messages[2].Content != "The fixture says hello." {
		t.Fatalf("assistant continuation = %#v", terminal.Messages[2])
	}
	if visible := fmt.Sprintf("%#v", terminal); strings.Contains(visible, fixture) || strings.Contains(visible, `"outcome"`) {
		t.Fatal("tool result entered public snapshot")
	}
	requests := transport.snapshot()
	if len(requests) != 2 {
		t.Fatalf("provider requests = %d", len(requests))
	}
	for i, raw := range requests {
		var payload struct {
			Tools             []json.RawMessage `json:"tools"`
			ParallelToolCalls bool              `json:"parallel_tool_calls"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			t.Fatalf("decode provider request %d: %v", i, err)
		}
		if names := providerToolNames(t, payload.Tools); strings.Join(names, ",") != "file_read,file_list,glob,search" || payload.ParallelToolCalls {
			t.Fatalf("request %d tools=%v parallel=%v", i, names, payload.ParallelToolCalls)
		}
	}
	var second struct {
		Input []struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Output string `json:"output"`
		} `json:"input"`
	}
	if err := json.Unmarshal(requests[1], &second); err != nil {
		t.Fatal(err)
	}
	foundOutput := false
	var inputTypes []string
	for _, item := range second.Input {
		inputTypes = append(inputTypes, item.Type)
		if item.Type == "function_call_output" && item.CallID == "call_read_1" {
			var result struct {
				Structured struct {
					Content string `json:"content"`
				} `json:"structured"`
			}
			if json.Unmarshal([]byte(item.Output), &result) == nil && result.Structured.Content == fixture {
				foundOutput = true
			}
		}
	}
	if !foundOutput {
		t.Fatalf("file_read result did not return to provider; input types=%v", inputTypes)
	}
	closeRuntime(t, service)

	store, err := agentsqlite.Open(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	call, err := store.GetToolCall(ctx, "call_read_1")
	if err != nil || call.Status != session.ToolCallCompleted || call.ResultPartID == "" {
		_ = store.Close()
		t.Fatalf("durable call status=%q result=%q err=%v", call.Status, call.ResultPartID, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openCodexFixture(t, ctx, paths.Database, sessionID, workspace, &http.Client{Transport: transport})
	replay, err := reopened.Load(ctx)
	if err != nil || len(replay.Messages) != 3 || len(replay.Messages[1].Tools) != 1 || replay.Messages[1].Tools[0] != activity || replay.Messages[2].Content != "The fixture says hello." {
		t.Fatalf("replay = %#v, err=%v", replay, err)
	}
	closeRuntime(t, reopened)
}

func TestCodexLexicalPathEscapeIsRejectedBeforeToolPersistence(t *testing.T) {
	ctx := context.Background()
	parent := t.TempDir()
	workspace := filepath.Join(parent, "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	const sentinel = "OUTSIDE_LEXICAL_SENTINEL"
	if err := os.WriteFile(filepath.Join(parent, "outside.txt"), []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	transport := newPathToolTransport("../outside.txt")
	service := openCodexFixture(t, ctx, paths.Database, platform.WorkspaceSessionID(workspace), workspace, &http.Client{Transport: transport})
	started, err := service.Start(ctx, "try an invalid path", codexStartConfig(codexmodel.DefaultModel, codexmodel.ReasoningEffortMedium))
	if err != nil {
		t.Fatal(err)
	}
	terminal := drainProviderRun(t, started.Run)
	if terminal.Notice != runtimeui.NoticeProviderFailed || len(terminal.Messages) != 1 || len(terminal.Messages[0].Tools) != 0 || len(transport.snapshot()) != 1 {
		t.Fatalf("terminal=%#v requests=%d", terminal, len(transport.snapshot()))
	}
	if strings.Contains(fmt.Sprintf("%#v", terminal), sentinel) {
		t.Fatal("outside sentinel reached snapshot")
	}
	closeRuntime(t, service)
	store, err := agentsqlite.Open(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.GetToolCall(ctx, "call_path"); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("lexically invalid call was persisted: %v", err)
	}
}

func TestCodexSymlinkEscapeReturnsStructuredResultWithCompletedLifecycle(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	const sentinel = "OUTSIDE_SYMLINK_SENTINEL"
	if err := os.WriteFile(outside, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(workspace, "link.txt")); err != nil {
		t.Fatal(err)
	}
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	transport := newPathToolTransport("link.txt")
	sessionID := platform.WorkspaceSessionID(workspace)
	service := openCodexFixture(t, ctx, paths.Database, sessionID, workspace, &http.Client{Transport: transport})
	started, err := service.Start(ctx, "inspect the link", codexStartConfig(codexmodel.DefaultModel, codexmodel.ReasoningEffortMedium))
	if err != nil {
		t.Fatal(err)
	}
	terminal := drainProviderRun(t, started.Run)
	if terminal.Notice != "" || len(terminal.Messages) != 3 || len(terminal.Messages[1].Tools) != 1 || terminal.Messages[1].Tools[0].Status != runtimeui.ToolCompleted || terminal.Messages[2].Content != "The requested path is outside the workspace." {
		t.Fatalf("terminal=%#v", terminal)
	}
	requests := transport.snapshot()
	if len(requests) != 2 || strings.Contains(string(requests[1]), sentinel) {
		t.Fatalf("requests=%d sentinel leaked=%v", len(requests), len(requests) == 2 && strings.Contains(string(requests[1]), sentinel))
	}
	var second struct {
		Input []struct {
			Type   string `json:"type"`
			Output string `json:"output"`
		} `json:"input"`
	}
	if err := json.Unmarshal(requests[1], &second); err != nil {
		t.Fatal(err)
	}
	structuredFailure := false
	for _, item := range second.Input {
		if item.Type == "function_call_output" && strings.Contains(item.Output, `"path_escape"`) {
			structuredFailure = true
		}
	}
	if !structuredFailure {
		t.Fatal("symlink escape did not return a structured path_escape result")
	}
	closeRuntime(t, service)
	reopened := openCodexFixture(t, ctx, paths.Database, sessionID, workspace, &http.Client{Transport: transport})
	replay, err := reopened.Load(ctx)
	if err != nil || len(replay.Messages) != 3 || len(replay.Messages[1].Tools) != 1 || replay.Messages[1].Tools[0].Status != runtimeui.ToolCompleted {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	closeRuntime(t, reopened)
}

func TestCodexNonAllowlistedShellIsRejectedBeforeExecutionAndHiddenFromSnapshots(t *testing.T) {
	const (
		transient  = "SECRET_TRANSIENT_PROVIDER_TEXT"
		secretName = "shell"
		secretArgs = "SECRET_PROVIDER_TOOL_ARGS"
	)
	ctx := context.Background()
	workspace := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		stream := strings.Join([]string{
			`data: {"type":"response.output_text.delta","delta":"` + transient + `"}`,
			`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","name":"` + secretName + `","call_id":"call_secret","arguments":""}}`,
			`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"value\":\"` + secretArgs + `\"}"}`,
			`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","name":"` + secretName + `","call_id":"call_secret","arguments":"{\"value\":\"` + secretArgs + `\"}"}}`,
			`data: {"type":"response.completed","response":{"id":"response_tool"}}`,
			"",
		}, "\n\n")
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream))}, nil
	})
	service := openCodexFixture(t, ctx, paths.Database, platform.WorkspaceSessionID(workspace), workspace, &http.Client{Transport: transport})
	started, err := service.Start(ctx, "do not execute provider tools", codexStartConfig(codexmodel.DefaultModel, codexmodel.ReasoningEffortMedium))
	if err != nil {
		t.Fatal(err)
	}
	terminal := drainProviderRun(t, started.Run)
	if terminal.Notice != runtimeui.NoticeProviderFailed || len(terminal.Messages) != 1 || terminal.Messages[0].Status != runtimeui.StatusFailed {
		t.Fatalf("terminal = %#v", terminal)
	}
	closeRuntime(t, service)

	db, err := sql.Open("sqlite", paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var toolCalls int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tool_calls`).Scan(&toolCalls); err != nil {
		t.Fatal(err)
	}
	if toolCalls != 0 {
		t.Fatalf("provider-controlled response created %d durable tool calls", toolCalls)
	}
	var assistantPlaceholders int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE run_id = ? AND role = 'assistant'`, terminal.RunID).Scan(&assistantPlaceholders); err != nil {
		t.Fatal(err)
	}
	// Failed runs retain one content-free assistant placeholder as a recovery
	// anchor; terminal projection hides it and no streamed content may be stored.
	if assistantPlaceholders != 1 {
		t.Fatalf("rejected provider response left %d durable assistant placeholders", assistantPlaceholders)
	}
	var assistantParts int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM parts p JOIN messages m ON m.id = p.message_id WHERE m.run_id = ? AND m.role = 'assistant'`, terminal.RunID).Scan(&assistantParts); err != nil {
		t.Fatal(err)
	}
	if assistantParts != 0 {
		t.Fatalf("rejected provider response left %d durable assistant parts", assistantParts)
	}
	visible := fmt.Sprintf("%#v", terminal)
	for _, forbidden := range []string{transient, secretName, secretArgs, "call_secret"} {
		if strings.Contains(visible, forbidden) {
			t.Fatalf("provider tool data reached public snapshot")
		}
	}
}
