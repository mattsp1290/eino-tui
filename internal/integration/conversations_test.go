package integration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
	"github.com/mattsp1290/eino-tui/internal/conversationtools"
	"github.com/mattsp1290/eino-tui/internal/platform"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
)

const (
	journeyPromptOne   = "first conversation prompt\nsecond line 界"
	journeyPromptTwo   = "second conversation prompt"
	journeyRenameAsk   = "please rename this conversation"
	journeyContinueOne = "continue the first conversation"
	journeyAfterLaunch = "after relaunch"
	agentTitle         = "Codex chose this title"
)

func textReply(id, text string) []string {
	return []string{
		`{"type":"response.output_text.delta","delta":"` + text + `"}`,
		`{"type":"response.completed","response":{"id":"` + id + `"}}`,
	}
}

func renameCall(id string) []string {
	arguments, _ := json.Marshal(map[string]string{"title": agentTitle})
	quoted, _ := json.Marshal(string(arguments))
	return []string{
		`{"type":"response.output_text.delta","delta":"Renaming now."}`,
		`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","name":"rename_conversation","call_id":"call_rename_1","arguments":""}}`,
		`{"type":"response.function_call_arguments.done","output_index":0,"arguments":` + string(quoted) + `}`,
		`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","name":"rename_conversation","call_id":"call_rename_1","arguments":` + string(quoted) + `}}`,
		`{"type":"response.completed","response":{"id":"` + id + `"}}`,
	}
}

func journeyTransport() *scriptedCodexTransport {
	return &scriptedCodexTransport{responses: [][]string{
		textReply("response_a1_1", "reply one"),
		textReply("response_a2_1", "reply two"),
		renameCall("response_a2_rename"),
		textReply("response_a2_renamed", "Done renaming."),
		textReply("response_a1_2", "reply three"),
		textReply("response_a1_3", "reply four"),
	}}
}

func openJourney(t *testing.T, ctx context.Context, paths platform.Paths, workspace platform.Workspace, transport http.RoundTripper) runtimeui.Service {
	t.Helper()
	resolver, err := codexmodel.NewResolver(&http.Client{Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	service, err := runtimeui.Open(ctx, paths, workspace, runtimeui.Config{Resolver: resolver, AgentName: "codex", SystemPrompt: "Be helpful. Rename the conversation only when asked."})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func runJourneyTurn(t *testing.T, service runtimeui.Service, prompt string) runtimeui.Snapshot {
	t.Helper()
	started, err := service.Start(context.Background(), prompt, codexStartConfig(codexmodel.DefaultModel, codexmodel.ReasoningEffortMedium))
	if err != nil {
		t.Fatal(err)
	}
	if started.Kind != runtimeui.ActionStarted {
		t.Fatalf("start kind=%v", started.Kind)
	}
	return drainProviderRun(t, started.Run)
}

func userTexts(messages []runtimeui.Message) []string {
	var texts []string
	for _, message := range messages {
		if message.Role == runtimeui.RoleUser {
			texts = append(texts, message.Content)
		}
	}
	return texts
}

func TestCredentialFreeMultipleConversationJourney(t *testing.T) {
	ctx := context.Background()
	stateDir := filepath.Join(t.TempDir(), "state")
	paths, err := platform.PrepareState(ctx, stateDir)
	if err != nil {
		t.Fatal(err)
	}
	oldDatabase := filepath.Join(paths.Directory, "sessions.db")
	if err := os.WriteFile(oldDatabase, []byte("previous storage generation"), 0o600); err != nil {
		t.Fatal(err)
	}
	rootA := t.TempDir()
	workspaceA, err := platform.IdentifyWorkspace(rootA)
	if err != nil {
		t.Fatal(err)
	}
	transport := journeyTransport()

	// 1. Launch A with no state: Conversation 1, no provider call.
	service := openJourney(t, ctx, paths, workspaceA, transport)
	loaded, err := service.Load(ctx)
	if err != nil || loaded.Conversation.Number != 1 || loaded.Conversation.Title != "Conversation 1" || loaded.Phase != runtimeui.PhaseIdle || transport.count() != 0 {
		t.Fatalf("initial load=%#v err=%v requests=%d", loaded, err, transport.count())
	}
	one := loaded.Conversation
	first := runJourneyTurn(t, service, journeyPromptOne)
	if texts := userTexts(first.Messages); len(texts) != 1 || texts[0] != journeyPromptOne {
		t.Fatalf("admitted user messages=%v", texts)
	}
	if first.Conversation.Title != "Conversation 1 — first conversation prompt second line 界" || first.Messages[len(first.Messages)-1].Content != "reply one" {
		t.Fatalf("first terminal=%#v", first)
	}

	// 2. Create Conversation 2 while idle and prove request isolation.
	created, err := service.CreateConversation(ctx, one.Generation)
	if err != nil || created.Snapshot.Conversation.Number != 2 || len(created.Snapshot.Messages) != 0 {
		t.Fatalf("create=%#v err=%v", created.Snapshot, err)
	}
	two := created.Snapshot.Conversation
	second := runJourneyTurn(t, service, journeyPromptTwo)
	if second.Conversation.ID != two.ID || second.Conversation.Title != "Conversation 2 — second conversation prompt" {
		t.Fatalf("second terminal=%#v", second.Conversation)
	}
	requests := transport.snapshot()
	if len(requests) != 2 {
		t.Fatalf("provider requests=%d", len(requests))
	}
	if body := string(requests[1]); strings.Contains(body, "first conversation prompt") || strings.Contains(body, "reply one") || !strings.Contains(body, journeyPromptTwo) {
		t.Fatal("second conversation request leaked first conversation history")
	}
	var advertised struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(requests[1], &advertised); err != nil {
		t.Fatal(err)
	}
	if names := providerToolNames(t, advertised.Tools); strings.Join(names, ",") != "file_read,file_list,glob,search,rename_conversation" {
		t.Fatalf("advertised tools=%v", names)
	}
	if strings.Contains(string(requests[1]), `"shell"`) || strings.Contains(string(requests[1]), "file_write") {
		t.Fatal("non-allowlisted tool advertised")
	}

	// 3. Manual rename, then an agent rename through the real tool loop.
	renamed, err := service.RenameConversation(ctx, two.ID, second.Conversation.Generation, "Manual title")
	if err != nil || renamed.Title != "Manual title" {
		t.Fatalf("manual rename=%#v err=%v", renamed, err)
	}
	agent := runJourneyTurn(t, service, journeyRenameAsk)
	if agent.Notice != "" || agent.Conversation.Title != agentTitle {
		t.Fatalf("agent rename terminal=%#v", agent.Conversation)
	}
	var activity *runtimeui.ToolActivity
	for i := range agent.Messages {
		for j := range agent.Messages[i].Tools {
			activity = &agent.Messages[i].Tools[j]
		}
	}
	if activity == nil || activity.ID != "call_rename_1" || activity.Name != conversationtools.Name || activity.Subject != conversationtools.Subject || activity.Status != runtimeui.ToolCompleted {
		t.Fatalf("rename activity=%#v", activity)
	}
	if visible := fmt.Sprintf("%#v", agent); strings.Contains(visible, `"renamed"`) || strings.Contains(activity.Subject, agentTitle) {
		t.Fatal("tool result or title argument entered the public snapshot")
	}
	if agent.Messages[len(agent.Messages)-1].Content != "Done renaming." {
		t.Fatalf("final answer after rename=%#v", agent.Messages[len(agent.Messages)-1])
	}
	requests = transport.snapshot()
	if len(requests) != 4 {
		t.Fatalf("provider requests after rename=%d (no extra call may be made for naming)", len(requests))
	}
	var continuation struct {
		Input []struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Output string `json:"output"`
		} `json:"input"`
	}
	if err := json.Unmarshal(requests[3], &continuation); err != nil {
		t.Fatal(err)
	}
	structured := false
	for _, item := range continuation.Input {
		if item.Type == "function_call_output" && item.CallID == "call_rename_1" && strings.Contains(item.Output, `"renamed":true`) && strings.Contains(item.Output, `"changed":true`) {
			structured = true
		}
	}
	if !structured {
		t.Fatalf("rename tool result did not return to the provider: %s", requests[3])
	}
	directory, err := service.ListConversations(ctx, "")
	if err != nil || len(directory.Items) != 2 || directory.Items[0].ID != two.ID || directory.Items[0].Title != agentTitle || !directory.Items[0].Selected || directory.Items[1].Title != first.Conversation.Title {
		t.Fatalf("directory=%#v err=%v", directory, err)
	}

	// 4. Switch back to Conversation 1 and continue only its history.
	if _, err := service.SelectConversation(ctx, "conversation-nope", agent.Conversation.Generation); !errors.Is(err, runtimeui.ErrConversationUnavailable) {
		t.Fatalf("failed selection error=%v", err)
	}
	if still, _ := service.Load(ctx); still.Conversation.ID != two.ID {
		t.Fatalf("failed selection changed source: %#v", still.Conversation)
	}
	selected, err := service.SelectConversation(ctx, one.ID, agent.Conversation.Generation)
	if err != nil || selected.Snapshot.Conversation.ID != one.ID {
		t.Fatalf("select one=%#v err=%v", selected.Snapshot, err)
	}
	if texts := userTexts(selected.Snapshot.Messages); len(texts) != 1 || texts[0] != journeyPromptOne {
		t.Fatalf("selected history=%v", texts)
	}
	third := runJourneyTurn(t, service, journeyContinueOne)
	if texts := userTexts(third.Messages); len(texts) != 2 || texts[1] != journeyContinueOne || third.Messages[len(third.Messages)-1].Content != "reply three" {
		t.Fatalf("continued history=%#v", third.Messages)
	}
	body := string(transport.request(4))
	for _, forbidden := range []string{journeyPromptTwo, journeyRenameAsk, "reply two", "Done renaming", "call_rename_1", agentTitle} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("conversation one request carried conversation two content %q", forbidden)
		}
	}
	for _, required := range []string{"first conversation prompt", "reply one", journeyContinueOne} {
		if !strings.Contains(body, required) {
			t.Fatalf("conversation one request missing its own history %q", required)
		}
	}
	closeRuntime(t, service)

	// 5. Relaunch through a symlink spelling, then a separate workspace.
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(rootA, link); err != nil {
		t.Fatal(err)
	}
	linked, err := platform.IdentifyWorkspace(link)
	if err != nil || linked != workspaceA {
		t.Fatalf("symlink identity=%#v err=%v", linked, err)
	}
	relaunched := openJourney(t, ctx, paths, linked, transport)
	restored, err := relaunched.Load(ctx)
	if err != nil || restored.Conversation.ID != one.ID || restored.Conversation.Title != first.Conversation.Title || len(userTexts(restored.Messages)) != 2 {
		t.Fatalf("restored=%#v err=%v", restored, err)
	}
	fourth := runJourneyTurn(t, relaunched, journeyAfterLaunch)
	if len(userTexts(fourth.Messages)) != 3 || fourth.Messages[len(fourth.Messages)-1].Content != "reply four" {
		t.Fatalf("after relaunch=%#v", fourth.Messages)
	}
	closeRuntime(t, relaunched)

	workspaceB, err := platform.IdentifyWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sentinel := openJourney(t, ctx, paths, workspaceB, transport)
	loadedB, err := sentinel.Load(ctx)
	if err != nil || loadedB.Conversation.Number != 1 || loadedB.Conversation.ID == one.ID || loadedB.Conversation.ID == two.ID {
		t.Fatalf("workspace B=%#v err=%v", loadedB.Conversation, err)
	}
	pageB, err := sentinel.ListConversations(ctx, "")
	if err != nil || len(pageB.Items) != 1 {
		t.Fatalf("workspace B directory=%#v err=%v", pageB, err)
	}
	if _, err := sentinel.SelectConversation(ctx, one.ID, loadedB.Conversation.Generation); !errors.Is(err, runtimeui.ErrConversationUnavailable) {
		t.Fatalf("cross-workspace select error=%v", err)
	}
	if _, err := sentinel.RenameConversation(ctx, two.ID, loadedB.Conversation.Generation, "forged"); !errors.Is(err, runtimeui.ErrConversationUnavailable) {
		t.Fatalf("cross-workspace rename error=%v", err)
	}
	closeRuntime(t, sentinel)
	forgedRecord := fmt.Sprintf(`{"version":1,"workspace_id":%q,"next_number":2,"selected_conversation_id":%q}`, workspaceB.ID, one.ID)
	if err := os.WriteFile(filepath.Join(paths.Workspaces, workspaceB.ID+".json"), []byte(forgedRecord), 0o600); err != nil {
		t.Fatal(err)
	}
	forged := openJourney(t, ctx, paths, workspaceB, transport)
	if _, err := forged.Load(ctx); !errors.Is(err, runtimeui.ErrConversationUnavailable) {
		t.Fatalf("forged selector error=%v", err)
	}
	closeRuntime(t, forged)
	if transport.count() != 6 {
		t.Fatalf("provider requests=%d; directory, selection and rename must not call the model", transport.count())
	}
	if data, err := os.ReadFile(oldDatabase); err != nil || string(data) != "previous storage generation" {
		t.Fatalf("old sessions.db changed: %q err=%v", data, err)
	}
	store, db := openInspectionStore(t, ctx, paths.Database)
	defer func() { _ = db.Close() }()
	for _, expected := range []struct {
		id    session.ID
		title string
	}{{one.ID, first.Conversation.Title}, {two.ID, agentTitle}} {
		record, err := store.GetSession(ctx, expected.id)
		if err != nil || record.Title != expected.title || record.WorkspaceID != workspaceA.ID || record.Directory != workspaceA.Root {
			t.Fatalf("durable record %s=%#v err=%v", expected.id, record, err)
		}
	}
}

func TestRenameToolRejectsInjectedSelectorsWithoutMutation(t *testing.T) {
	ctx := context.Background()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := platform.IdentifyWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const secretTitle = "SECRET_INJECTED_TITLE"
	arguments := `{"title":"` + secretTitle + `","session_id":"other","workspace_id":"other","fence":"claim"}`
	quoted, _ := json.Marshal(arguments)
	transport := &scriptedCodexTransport{responses: [][]string{
		{
			`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","name":"rename_conversation","call_id":"call_injected","arguments":""}}`,
			`{"type":"response.function_call_arguments.done","output_index":0,"arguments":` + string(quoted) + `}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","name":"rename_conversation","call_id":"call_injected","arguments":` + string(quoted) + `}}`,
			`{"type":"response.completed","response":{"id":"response_injected"}}`,
		},
		textReply("response_after_injection", "understood"),
	}}
	service := openJourney(t, ctx, paths, workspace, transport)
	loaded, err := service.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	terminal := runJourneyTurn(t, service, "try to rename another conversation")
	if terminal.Conversation.Title != "Conversation 1 — try to rename another conversation" {
		t.Fatalf("injected selectors changed the title: %#v", terminal.Conversation)
	}
	if visible := fmt.Sprintf("%#v", terminal); strings.Contains(visible, secretTitle) {
		t.Fatal("rejected title leaked into the snapshot")
	}
	closeRuntime(t, service)
	store, db := openInspectionStore(t, ctx, paths.Database)
	defer func() { _ = db.Close() }()
	record, err := store.GetSession(ctx, loaded.Conversation.ID)
	if err != nil || record.Title == secretTitle {
		t.Fatalf("durable title=%q err=%v", record.Title, err)
	}
	if _, err := store.GetToolCall(ctx, "call_injected"); err == nil {
		call, _ := store.GetToolCall(ctx, "call_injected")
		if call.Status == session.ToolCallCompleted {
			t.Fatalf("injected rename completed: %#v", call)
		}
	}
	assertDurableSecretsAbsent(t, paths.Database, "SECRET_INJECTED")
}
