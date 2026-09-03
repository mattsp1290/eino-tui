package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mattsp1290/eino-agent/model"
	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
	"github.com/mattsp1290/eino-tui/internal/platform"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
)

const (
	reasoningOne = `{"type":"reasoning","id":"rs_private_1","summary":[],"encrypted_content":"REASON_SECRET_ONE"}`
	reasoningTwo = `{"type":"reasoning","id":"rs_private_2","summary":[],"encrypted_content":"REASON_SECRET_TWO"}`
)

type recordingCodexTransport struct {
	mu       sync.Mutex
	requests [][]byte
}

type failureThenSuccessTransport struct {
	mu    sync.Mutex
	calls int
}

type roundTripFunc func(*http.Request) (*http.Response, error)

type panickingCodexBody struct {
	closed chan struct{}
}

func (*panickingCodexBody) Read([]byte) (int, error) {
	panic("TOKEN /tmp/private provider body panic")
}

func (b *panickingCodexBody) Close() error {
	close(b.closed)
	return nil
}

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func (t *failureThenSuccessTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.calls++
	call := t.calls
	t.mu.Unlock()
	if call == 1 {
		body := `{"error":{"code":"usage_not_included","message":"TOKEN /tmp/private prompt text \\u001b]0;leak"}}`
		return &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
	}
	stream := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"recovered answer\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"response_ok\"}}\n\n"
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream)), Request: request}, nil
}

func (t *recordingCodexTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	t.requests = append(t.requests, append([]byte(nil), body...))
	turn := len(t.requests)
	t.mu.Unlock()
	var events []string
	if turn == 1 {
		events = []string{
			`{"type":"response.output_item.done","item":` + reasoningOne + `}`,
			`{"type":"response.output_item.done","item":` + reasoningTwo + `}`,
			`{"type":"response.output_text.delta","delta":"first answer"}`,
			`{"type":"response.completed","response":{"id":"response_1","usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}`,
		}
	} else {
		events = []string{
			`{"type":"response.output_text.delta","delta":"second answer"}`,
			`{"type":"response.completed","response":{"id":"response_2","usage":{"input_tokens":9,"output_tokens":2,"total_tokens":11}}}`,
		}
	}
	var stream strings.Builder
	for _, event := range events {
		stream.WriteString("data: ")
		stream.WriteString(event)
		stream.WriteString("\n\n")
	}
	return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream.String())), Request: request}, nil
}

func (t *recordingCodexTransport) request(index int) []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]byte(nil), t.requests[index]...)
}

func (t *recordingCodexTransport) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.requests)
}

func TestCodexReasoningStateSurvivesSQLiteReopenAndStaysPrivate(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	transport := &recordingCodexTransport{}
	httpClient := &http.Client{Transport: transport}
	sessionID := platform.WorkspaceSessionID(workspace)

	service := openCodexFixture(t, ctx, paths.Database, sessionID, workspace, httpClient)
	first, err := service.Start(ctx, "first prompt")
	if err != nil {
		t.Fatal(err)
	}
	firstTerminal := drainProviderRun(t, first.Run)
	assertPrivateBytesAbsent(t, firstTerminal)
	if len(firstTerminal.Messages) != 2 || firstTerminal.Messages[1].Content != "first answer" {
		t.Fatalf("turn one snapshot = %#v", firstTerminal)
	}
	closeRuntime(t, service)

	reopened := openCodexFixture(t, ctx, paths.Database, sessionID, workspace, httpClient)
	second, err := reopened.Start(ctx, "second prompt")
	if err != nil {
		t.Fatal(err)
	}
	secondTerminal := drainProviderRun(t, second.Run)
	assertPrivateBytesAbsent(t, secondTerminal)
	if len(secondTerminal.Messages) != 4 || secondTerminal.Messages[3].Content != "second answer" {
		t.Fatalf("turn two snapshot = %#v", secondTerminal)
	}

	request := transport.request(1)
	var payload struct {
		Input []json.RawMessage `json:"input"`
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(request, &payload); err != nil {
		t.Fatalf("decode second request: %v", err)
	}
	if len(payload.Tools) != 0 {
		t.Fatalf("Codex request registered tools: %s", request)
	}
	var restored []string
	for _, item := range payload.Input {
		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(item, &kind); err != nil {
			t.Fatal(err)
		}
		if kind.Type == "reasoning" {
			restored = append(restored, string(item))
		}
	}
	if len(restored) != 2 || restored[0] != reasoningOne || restored[1] != reasoningTwo {
		t.Fatalf("restored reasoning order/bytes = %#v; request=%s", restored, request)
	}
	closeRuntime(t, reopened)
}

func TestCodexCompatibleModelSwitchRestoresReasoningState(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	transport := &recordingCodexTransport{}
	client := &http.Client{Transport: transport}
	sessionID := platform.WorkspaceSessionID(workspace)

	first := openCodexFixtureModel(t, ctx, paths.Database, sessionID, workspace, client, codexmodel.DefaultModel)
	started, err := first.Start(ctx, "seed cross-model state")
	if err != nil {
		t.Fatal(err)
	}
	_ = drainProviderRun(t, started.Run)
	closeRuntime(t, first)

	const nextModel = "gpt-5.6"
	reopened := openCodexFixtureModel(t, ctx, paths.Database, sessionID, workspace, client, nextModel)
	continued, err := reopened.Start(ctx, "continue with compatible model")
	if err != nil {
		t.Fatal(err)
	}
	terminal := drainProviderRun(t, continued.Run)
	if len(terminal.Messages) != 4 || transport.count() != 2 {
		t.Fatalf("terminal=%#v dispatches=%d", terminal, transport.count())
	}

	request := transport.request(1)
	var payload struct {
		Model string            `json:"model"`
		Input []json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(request, &payload); err != nil {
		t.Fatal(err)
	}
	var restored []string
	for _, item := range payload.Input {
		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(item, &kind); err != nil {
			t.Fatal(err)
		}
		if kind.Type == "reasoning" {
			restored = append(restored, string(item))
		}
	}
	if payload.Model != nextModel || len(restored) != 2 || restored[0] != reasoningOne || restored[1] != reasoningTwo {
		t.Fatalf("model=%q restored=%#v", payload.Model, restored)
	}
	closeRuntime(t, reopened)
}

func TestCodexProviderFailureKeepsFailedUserAndAllowsNextTurn(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	sessionID := platform.WorkspaceSessionID(workspace)
	client := &http.Client{Transport: &failureThenSuccessTransport{}}
	service := openCodexFixture(t, ctx, paths.Database, sessionID, workspace, client)

	failed, err := service.Start(ctx, "failed prompt")
	if err != nil {
		t.Fatal(err)
	}
	failedTerminal := drainProviderRun(t, failed.Run)
	if failedTerminal.Notice != runtimeui.NoticePlanUnavailable || len(failedTerminal.Messages) != 1 || failedTerminal.Messages[0].Status != runtimeui.StatusFailed {
		t.Fatalf("failed terminal = %#v", failedTerminal)
	}
	visible := fmt.Sprintf("%#v", failedTerminal)
	for _, secret := range []string{"TOKEN", "/tmp/private", "prompt text", "leak"} {
		if strings.Contains(visible, secret) {
			t.Fatalf("provider error leaked: %s", visible)
		}
	}

	recovered, err := service.Start(ctx, "retry prompt")
	if err != nil {
		t.Fatal(err)
	}
	recoveredTerminal := drainProviderRun(t, recovered.Run)
	if len(recoveredTerminal.Messages) != 3 || recoveredTerminal.Messages[2].Content != "recovered answer" {
		t.Fatalf("retry terminal = %#v", recoveredTerminal)
	}
	closeRuntime(t, service)
	assertDurableSecretsAbsent(t, paths.Database, "TOKEN", "/tmp/private", "prompt text", "leak")

	reopened := openCodexFixture(t, ctx, paths.Database, sessionID, workspace, client)
	replay, err := reopened.Load(ctx)
	if err != nil || len(replay.Messages) != 3 || replay.Messages[0].Status != runtimeui.StatusFailed || replay.Messages[2].Content != "recovered answer" {
		t.Fatalf("replay = %#v, %v", replay, err)
	}
	closeRuntime(t, reopened)
}

func TestCodexProviderErrorsUseBoundedTypedNotices(t *testing.T) {
	sseResponse := func(body string) *http.Response {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
	}
	panicBody := &panickingCodexBody{closed: make(chan struct{})}
	tests := []struct {
		name      string
		transport http.RoundTripper
		want      string
		after     func(*testing.T)
	}{
		{
			name: "quota",
			transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return sseResponse("data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"insufficient_quota\",\"message\":\"TOKEN /tmp/private\"}}}\n\n"), nil
			}),
			want: runtimeui.NoticeQuotaExceeded,
		},
		{
			name: "malformed SSE",
			transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return sseResponse("data: {TOKEN /tmp/private\n\n"), nil
			}),
			want: runtimeui.NoticeProviderFailed,
		},
		{
			name: "non-2xx",
			transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("TOKEN /tmp/private\x1b]0;leak\a"))}, nil
			}),
			want: runtimeui.NoticeProviderFailed,
		},
		{
			name: "transport",
			transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("TOKEN /tmp/private\x1b]0;leak\a")
			}),
			want: runtimeui.NoticeProviderFailed,
		},
		{
			name: "response body panic",
			transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: panicBody}, nil
			}),
			want: runtimeui.NoticeProviderFailed,
			after: func(t *testing.T) {
				t.Helper()
				select {
				case <-panicBody.closed:
				default:
					t.Fatal("panicking provider response body was not closed")
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			workspace := t.TempDir()
			paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
			if err != nil {
				t.Fatal(err)
			}
			service := openCodexFixture(t, ctx, paths.Database, platform.WorkspaceSessionID(workspace), workspace, &http.Client{Transport: test.transport})
			started, err := service.Start(ctx, "safe provider error prompt")
			if err != nil {
				t.Fatal(err)
			}
			terminal := drainProviderRun(t, started.Run)
			if terminal.Notice != test.want || len(terminal.Messages) != 1 || terminal.Messages[0].Status != runtimeui.StatusFailed {
				t.Fatalf("terminal = %#v", terminal)
			}
			visible := fmt.Sprintf("%#v", terminal)
			for _, secret := range []string{"TOKEN", "/tmp/private", "leak"} {
				if strings.Contains(visible, secret) {
					t.Fatalf("provider error leaked: %s", visible)
				}
			}
			closeRuntime(t, service)
			assertDurableSecretsAbsent(t, paths.Database, "TOKEN", "/tmp/private", "leak")
			if test.after != nil {
				test.after(t)
			}
		})
	}
}

func TestCodexToolCallResponseIsRejectedBeforeDurableToolExecution(t *testing.T) {
	const (
		transient  = "SECRET_TRANSIENT_PROVIDER_TEXT"
		secretName = "SECRET_PROVIDER_TOOL_NAME"
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
	started, err := service.Start(ctx, "do not execute provider tools")
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
	assertDurableSecretsAbsent(t, paths.Database, transient, secretName, secretArgs, "call_secret")
}

func TestProviderStatePersistenceFailureLeavesNoAssistantParts(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	transport := &recordingCodexTransport{}
	service := openCodexFixture(t, ctx, paths.Database, platform.WorkspaceSessionID(workspace), workspace, &http.Client{Transport: transport})
	db, err := sql.Open("sqlite", paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, `CREATE TRIGGER fail_provider_state BEFORE INSERT ON parts
		WHEN instr(CAST(NEW.record AS TEXT), '"Kind":"provider_state"') > 0
		BEGIN SELECT RAISE(ABORT, 'forced provider-state persistence failure'); END`)
	if err != nil {
		t.Fatal(err)
	}
	started, err := service.Start(ctx, "atomic persistence prompt")
	if err != nil {
		t.Fatal(err)
	}
	terminal := drainProviderRun(t, started.Run)
	if terminal.Notice != runtimeui.NoticeProviderFailed || len(terminal.Messages) != 1 || terminal.Messages[0].Status != runtimeui.StatusFailed {
		t.Fatalf("terminal = %#v", terminal)
	}
	var assistantParts int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM parts p JOIN messages m ON m.id = p.message_id WHERE m.run_id = ? AND m.role = 'assistant'`, terminal.RunID).Scan(&assistantParts); err != nil {
		t.Fatal(err)
	}
	if assistantParts != 0 {
		t.Fatalf("provider-state transaction left %d assistant parts", assistantParts)
	}
	closeRuntime(t, service)
}

func TestCorruptDurableProviderStatePreventsDispatch(t *testing.T) {
	ctx := context.Background()
	workspace := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	transport := &recordingCodexTransport{}
	client := &http.Client{Transport: transport}
	sessionID := platform.WorkspaceSessionID(workspace)
	service := openCodexFixture(t, ctx, paths.Database, sessionID, workspace, client)
	started, err := service.Start(ctx, "seed provider state")
	if err != nil {
		t.Fatal(err)
	}
	terminal := drainProviderRun(t, started.Run)
	if len(terminal.Messages) != 2 || transport.count() != 1 {
		t.Fatalf("seed terminal=%#v dispatches=%d", terminal, transport.count())
	}
	closeRuntime(t, service)

	db, err := sql.Open("sqlite", paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	var partID string
	var record []byte
	if err := db.QueryRowContext(ctx, `SELECT id, record FROM parts WHERE instr(CAST(record AS TEXT), '"Kind":"provider_state"') > 0 LIMIT 1`).Scan(&partID, &record); err != nil {
		db.Close()
		t.Fatal(err)
	}
	var part session.Part
	if err := json.Unmarshal(record, &part); err != nil {
		db.Close()
		t.Fatal(err)
	}
	part.Payload = json.RawMessage(`{"broken":true}`)
	corruptRecord, err := json.Marshal(part)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE parts SET record = ? WHERE id = ?`, corruptRecord, partID); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openCodexFixture(t, ctx, paths.Database, sessionID, workspace, client)
	if _, err := reopened.Start(ctx, "must not dispatch"); err == nil {
		t.Fatal("corrupt provider state was accepted")
	}
	if transport.count() != 1 {
		t.Fatalf("provider dispatched with corrupt state: %d requests", transport.count())
	}
	closeRuntime(t, reopened)
}

func openCodexFixture(t *testing.T, ctx context.Context, database string, sessionID session.ID, workspace string, client *http.Client) runtimeui.Service {
	t.Helper()
	return openCodexFixtureModel(t, ctx, database, sessionID, workspace, client, codexmodel.DefaultModel)
}

func openCodexFixtureModel(t *testing.T, ctx context.Context, database string, sessionID session.ID, workspace string, client *http.Client, modelID string) runtimeui.Service {
	t.Helper()
	resolver, err := codexmodel.NewResolver(ctx, client, modelID)
	if err != nil {
		t.Fatal(err)
	}
	service, err := runtimeui.Open(ctx, database, sessionID, workspace, runtimeui.Config{
		Resolver:  resolver,
		Selection: model.Selection{ProviderID: codexmodel.ProviderID, ModelID: model.ID(modelID)},
		AgentName: "codex", SystemPrompt: "Be helpful and use no tools.",
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func assertDurableSecretsAbsent(t *testing.T, database string, secrets ...string) {
	t.Helper()
	db, err := sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, secret := range secrets {
		var matches int
		if err := db.QueryRow(`SELECT
			(SELECT COUNT(*) FROM sessions WHERE instr(CAST(record AS TEXT), ?) > 0) +
			(SELECT COUNT(*) FROM runs WHERE instr(CAST(record AS TEXT), ?) > 0) +
			(SELECT COUNT(*) FROM messages WHERE instr(CAST(record AS TEXT), ?) > 0) +
			(SELECT COUNT(*) FROM parts WHERE instr(CAST(record AS TEXT), ?) > 0) +
			(SELECT COUNT(*) FROM events WHERE instr(CAST(record AS TEXT), ?) > 0) +
			(SELECT COUNT(*) FROM tool_calls WHERE instr(CAST(record AS TEXT), ?) > 0) +
			(SELECT COUNT(*) FROM context_epochs WHERE instr(CAST(record AS TEXT), ?) > 0) +
			(SELECT COUNT(*) FROM model_requests WHERE instr(CAST(record AS TEXT), ?) > 0)`,
			secret, secret, secret, secret, secret, secret, secret, secret).Scan(&matches); err != nil {
			t.Fatal(err)
		}
		if matches != 0 {
			t.Fatalf("durable records retained forbidden marker %q", secret)
		}
	}
}

func drainProviderRun(t *testing.T, run runtimeui.Run) runtimeui.Snapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var terminal runtimeui.Snapshot
	for {
		snapshot, ok := run.Next(ctx)
		if !ok {
			break
		}
		if snapshot.Terminal {
			terminal = snapshot
		}
	}
	if !terminal.Terminal {
		t.Fatalf("terminal snapshot missing: %v", ctx.Err())
	}
	return terminal
}

func assertPrivateBytesAbsent(t *testing.T, snapshot runtimeui.Snapshot) {
	t.Helper()
	visible := fmt.Sprintf("%#v", snapshot)
	for _, secret := range []string{"REASON_SECRET_ONE", "REASON_SECRET_TWO", "rs_private_1", "rs_private_2"} {
		if strings.Contains(visible, secret) {
			t.Fatalf("provider-private state leaked into snapshot: %s", visible)
		}
	}
}

func closeRuntime(t *testing.T, service runtimeui.Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
