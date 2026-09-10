package runtimeui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/conversationtools"
	"github.com/mattsp1290/eino-tui/internal/demomodel"
	"github.com/mattsp1290/eino-tui/internal/platform"
)

func finishTurn(t *testing.T, service Service, prompt string) Snapshot {
	t.Helper()
	result, err := service.Start(context.Background(), prompt, fixtureStartConfig())
	if err != nil {
		t.Fatal(err)
	}
	if result.Kind != ActionStarted {
		t.Fatalf("start kind=%v", result.Kind)
	}
	terminal, _ := drainRun(t, result.Run)
	return terminal
}

func closeService(t *testing.T, service Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := service.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func userContents(messages []Message) []string {
	var contents []string
	for _, message := range messages {
		if message.Role == RoleUser {
			contents = append(contents, message.Content)
		}
	}
	return contents
}

func TestLoadCreatesFirstConversationAndRelaunchRestoresSelection(t *testing.T) {
	ctx := context.Background()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	workspace, err := platform.IdentifyWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := openFixture(ctx, paths, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := opened.Start(ctx, "too early", fixtureStartConfig()); !errors.Is(err, ErrNoConversation) {
		t.Fatalf("start before load error=%v", err)
	}
	first, err := opened.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.Phase != PhaseIdle || first.Conversation.Number != 1 || first.Conversation.Title != "Conversation 1" || first.Conversation.ID == "" || first.Conversation.Generation != 1 || len(first.Messages) != 0 {
		t.Fatalf("first load=%#v", first)
	}
	if !strings.HasPrefix(string(first.Conversation.ID), "conversation-") {
		t.Fatalf("conversation id=%q", first.Conversation.ID)
	}
	record, fresh, err := platform.ReadWorkspacePreferences(ctx, platform.NewPreferenceStore(paths.Workspaces), workspace.ID)
	if err != nil || fresh || record.SelectedConversationID != string(first.Conversation.ID) || record.NextNumber != 2 {
		t.Fatalf("preferences=%#v fresh=%v err=%v", record, fresh, err)
	}
	again, err := opened.Load(ctx)
	if err != nil || again.Conversation != first.Conversation {
		t.Fatalf("repeated load=%#v err=%v", again, err)
	}
	closeService(t, opened)

	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}
	linked, err := platform.IdentifyWorkspace(link)
	if err != nil || linked != workspace {
		t.Fatalf("symlink identity=%#v err=%v", linked, err)
	}
	reopened, err := openFixture(ctx, paths, linked)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := reopened.Load(ctx)
	if err != nil || restored.Conversation.ID != first.Conversation.ID || restored.Conversation.Number != 1 {
		t.Fatalf("restored=%#v err=%v", restored, err)
	}
	closeService(t, reopened)
}

func TestConversationsHaveIndependentHistoriesAndWorkspaceIsolation(t *testing.T) {
	ctx := context.Background()
	paths, workspaceA := fixtureWorkspace(t)
	service := openResolvedFixture(t, ctx, paths, workspaceA)
	loaded, _ := service.Load(ctx)
	one := loaded.Conversation
	terminal := finishTurn(t, service, "first conversation prompt\nsecond line")
	if terminal.Conversation.ID != one.ID || terminal.Conversation.Title != "Conversation 1 — first conversation prompt second line" {
		t.Fatalf("terminal conversation=%#v", terminal.Conversation)
	}
	if _, err := service.CreateConversation(ctx, one.Generation+7); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale create error=%v", err)
	}
	created, err := service.CreateConversation(ctx, one.Generation)
	if err != nil {
		t.Fatal(err)
	}
	two := created.Snapshot.Conversation
	if two.Number != 2 || two.Title != "Conversation 2" || two.ID == one.ID || two.Generation != one.Generation+1 || len(created.Snapshot.Messages) != 0 || created.Snapshot.Phase != PhaseIdle {
		t.Fatalf("created=%#v", created.Snapshot)
	}
	second := finishTurn(t, service, "second conversation prompt")
	if got := userContents(second.Messages); len(got) != 1 || got[0] != "second conversation prompt" {
		t.Fatalf("second history=%v", got)
	}
	page, err := service.ListConversations(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.NextCursor != "" || page.Items[0].ID != two.ID || !page.Items[0].Selected || page.Items[1].ID != one.ID || page.Items[1].Selected {
		t.Fatalf("directory=%#v", page)
	}
	if page.Items[0].Title != "Conversation 2 — second conversation prompt" || page.Items[1].Number != 1 || page.Items[0].CreatedAt.Before(page.Items[1].CreatedAt) {
		t.Fatalf("directory metadata=%#v", page)
	}
	selected, err := service.SelectConversation(ctx, one.ID, two.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Snapshot.Conversation.ID != one.ID || selected.Snapshot.Conversation.Generation != two.Generation+1 || selected.Snapshot.Phase != PhaseIdle {
		t.Fatalf("selected=%#v", selected.Snapshot)
	}
	if got := userContents(selected.Snapshot.Messages); len(got) != 1 || got[0] != "first conversation prompt\nsecond line" {
		t.Fatalf("selected history=%v", got)
	}
	third := finishTurn(t, service, "continue one")
	if got := userContents(third.Messages); len(got) != 2 || got[1] != "continue one" {
		t.Fatalf("continued history=%v", got)
	}
	if _, err := service.SelectConversation(ctx, "conversation-missing", third.Conversation.Generation); !errors.Is(err, ErrConversationUnavailable) {
		t.Fatalf("missing select error=%v", err)
	}
	closeService(t, service)

	reopened := openResolvedFixture(t, ctx, paths, workspaceA)
	restored, _ := reopened.Load(ctx)
	if restored.Conversation.ID != one.ID || len(userContents(restored.Messages)) != 2 {
		t.Fatalf("restored=%#v", restored)
	}
	closeService(t, reopened)

	workspaceB, err := platform.IdentifyWorkspace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sentinel := openResolvedFixture(t, ctx, paths, workspaceB)
	loadedB, _ := sentinel.Load(ctx)
	if loadedB.Conversation.Number != 1 || loadedB.Conversation.ID == one.ID || loadedB.Conversation.ID == two.ID {
		t.Fatalf("workspace B=%#v", loadedB.Conversation)
	}
	pageB, err := sentinel.ListConversations(ctx, "")
	if err != nil || len(pageB.Items) != 1 || pageB.Items[0].ID != loadedB.Conversation.ID {
		t.Fatalf("workspace B directory=%#v err=%v", pageB, err)
	}
	if _, err := sentinel.SelectConversation(ctx, one.ID, loadedB.Conversation.Generation); !errors.Is(err, ErrConversationUnavailable) {
		t.Fatalf("cross-workspace select error=%v", err)
	}
	if _, err := sentinel.RenameConversation(ctx, one.ID, loadedB.Conversation.Generation, "forged"); !errors.Is(err, ErrConversationUnavailable) {
		t.Fatalf("cross-workspace rename error=%v", err)
	}
	closeService(t, sentinel)

	// A forged remembered selector never loads another workspace's conversation.
	forged := openTestPreference(t, paths, workspaceB.ID)
	forged.SelectedConversationID = string(one.ID)
	writeTestPreference(t, paths, workspaceB.ID, forged)
	forgedService, err := openFixture(ctx, paths, workspaceB)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := forgedService.Load(ctx); !errors.Is(err, ErrConversationUnavailable) {
		t.Fatalf("forged selector error=%v", err)
	}
	closeService(t, forgedService)
	store := openTestStore(t, ctx, paths.Database)
	defer func() { _ = store.Close() }()
	if got, _ := store.GetSession(ctx, one.ID); got.WorkspaceID != workspaceA.ID || got.Title != "Conversation 1 — first conversation prompt second line" {
		t.Fatalf("workspace A record changed: %#v", got)
	}
}

func openTestPreference(t *testing.T, paths platform.Paths, workspaceID string) platform.WorkspacePreferences {
	t.Helper()
	record, _, err := platform.ReadWorkspacePreferences(context.Background(), platform.NewPreferenceStore(paths.Workspaces), workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func writeTestPreference(t *testing.T, paths platform.Paths, workspaceID string, record platform.WorkspacePreferences) {
	t.Helper()
	data := `{"version":` + strconv.Itoa(record.Version) + `,"workspace_id":"` + record.WorkspaceID + `","next_number":` + strconv.FormatUint(record.NextNumber, 10) + `,"selected_conversation_id":"` + record.SelectedConversationID + `"}`
	if err := os.WriteFile(filepath.Join(paths.Workspaces, workspaceID+".json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestManualRenameBeforeFirstPromptWinsAndValidates(t *testing.T) {
	ctx := context.Background()
	paths, workspace := fixtureWorkspace(t)
	service := openResolvedFixture(t, ctx, paths, workspace)
	loaded, _ := service.Load(ctx)
	conv := loaded.Conversation
	for _, invalid := range []string{"", "   ", "\x1b[31m‮", strings.Repeat("x", 257), string([]byte{0xff, 0xfe})} {
		if _, err := service.RenameConversation(ctx, conv.ID, conv.Generation, invalid); !errors.Is(err, ErrInvalidTitle) {
			t.Fatalf("invalid title %q error=%v", invalid, err)
		}
	}
	if _, err := service.RenameConversation(ctx, conv.ID, conv.Generation+1, "stale"); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale rename error=%v", err)
	}
	info, err := service.RenameConversation(ctx, conv.ID, conv.Generation, "  Custom\ttitle \x1b]0;leak\a ")
	if err != nil || info.Title != "Custom title" || info.ID != conv.ID || info.Generation != conv.Generation || info.Number != 1 {
		t.Fatalf("rename info=%#v err=%v", info, err)
	}
	terminal := finishTurn(t, service, "first prompt after rename")
	if terminal.Conversation.Title != "Custom title" {
		t.Fatalf("default initializer overwrote custom title: %#v", terminal.Conversation)
	}
	page, _ := service.ListConversations(ctx, "")
	if len(page.Items) != 1 || page.Items[0].Title != "Custom title" {
		t.Fatalf("directory title=%#v", page)
	}
	closeService(t, service)
	store := openTestStore(t, ctx, paths.Database)
	defer func() { _ = store.Close() }()
	record, err := store.GetSession(ctx, conv.ID)
	if err != nil || record.Title != "Custom title" || record.Metadata[ConversationNumberKey] != "1" || record.WorkspaceID != workspace.ID || record.Directory != workspace.Root {
		t.Fatalf("durable record=%#v err=%v", record, err)
	}
}

type titleFailStore struct {
	durableStore
	failures atomic.Int64
}

func (s *titleFailStore) WithinTx(ctx context.Context, fn func(context.Context, session.Store) error) error {
	if s.failures.Add(-1) >= 0 {
		return errors.New("secret /tmp/private tx failure")
	}
	return s.durableStore.WithinTx(ctx, fn)
}

func TestDefaultTitleFailureAfterAdmissionIsMetadataOnlyAndRetried(t *testing.T) {
	ctx := context.Background()
	paths, workspace := fixtureWorkspace(t)
	opened := openResolvedFixture(t, ctx, paths, workspace)
	chat := opened.(*service)
	failing := &titleFailStore{durableStore: chat.store}
	failing.failures.Store(1)
	chat.store = failing
	result, err := chat.Start(ctx, "admitted despite title failure", fixtureStartConfig())
	if err != nil {
		t.Fatal(err)
	}
	if result.Snapshot.Notice != NoticeTitleUnavailable || result.Snapshot.Conversation.Title != "Conversation 1" || len(result.Snapshot.Messages) != 1 {
		t.Fatalf("admitted snapshot=%#v", result.Snapshot)
	}
	terminal, _ := drainRun(t, result.Run)
	if terminal.Conversation.Title != "Conversation 1 — admitted despite title failure" || len(userContents(terminal.Messages)) != 1 {
		t.Fatalf("terminal=%#v", terminal)
	}
	if visible := terminal.Notice + terminal.Conversation.Title; strings.Contains(visible, "secret") {
		t.Fatal("store failure leaked")
	}
	closeService(t, chat)
}

func TestUnadmittedPromptNeverTitlesTheConversation(t *testing.T) {
	ctx := context.Background()
	paths, workspace := fixtureWorkspace(t)
	opened := openResolvedFixture(t, ctx, paths, workspace)
	chat := opened.(*service)
	entered := make(chan struct{})
	release := make(chan struct{})
	chat.tail = &subscribeBarrierTail{tailer: chat.tail, entered: entered, release: release}
	errCh := make(chan error, 1)
	go func() { _, err := chat.Start(ctx, "unsent draft text", fixtureStartConfig()); errCh <- err }()
	<-entered
	if err := chat.InterruptActive(ctx); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("start error=%v", err)
	}
	snapshot, err := chat.Load(ctx)
	if err != nil || snapshot.Conversation.Title != "Conversation 1" || len(snapshot.Messages) != 0 {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
	page, _ := chat.ListConversations(ctx, "")
	if len(page.Items) != 1 || page.Items[0].Title != "Conversation 1" {
		t.Fatalf("directory=%#v", page)
	}
	closeService(t, chat)
}

func TestMissingPreferencesSelectNewestAndReconcileCounter(t *testing.T) {
	ctx := context.Background()
	paths, workspace := fixtureWorkspace(t)
	service := openResolvedFixture(t, ctx, paths, workspace)
	loaded, _ := service.Load(ctx)
	created, err := service.CreateConversation(ctx, loaded.Conversation.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SelectConversation(ctx, loaded.Conversation.ID, created.Snapshot.Conversation.Generation); err != nil {
		t.Fatal(err)
	}
	closeService(t, service)
	if err := os.Remove(filepath.Join(paths.Workspaces, workspace.ID+".json")); err != nil {
		t.Fatal(err)
	}
	reopened := openResolvedFixture(t, ctx, paths, workspace)
	restored, _ := reopened.Load(ctx)
	if restored.Conversation.ID != created.Snapshot.Conversation.ID || restored.Conversation.Number != 2 || restored.Notice != "" {
		t.Fatalf("restored=%#v", restored)
	}
	next, err := reopened.CreateConversation(ctx, restored.Conversation.Generation)
	if err != nil || next.Snapshot.Conversation.Number != 3 {
		t.Fatalf("reconciled number=%#v err=%v", next.Snapshot.Conversation, err)
	}
	closeService(t, reopened)

	// A remembered ID that no longer exists falls back to the newest with a notice.
	forged := openTestPreference(t, paths, workspace.ID)
	forged.SelectedConversationID = "conversation-deleted"
	writeTestPreference(t, paths, workspace.ID, forged)
	replaced := openResolvedFixture(t, ctx, paths, workspace)
	fallback, _ := replaced.Load(ctx)
	if fallback.Conversation.ID != next.Snapshot.Conversation.ID {
		t.Fatalf("fallback=%#v", fallback.Conversation)
	}
	closeService(t, replaced)
	replacedAgain, err := openFixture(ctx, paths, workspace)
	if err != nil {
		t.Fatal(err)
	}
	writeTestPreference(t, paths, workspace.ID, forged)
	first, err := replacedAgain.Load(ctx)
	if err != nil || first.Notice != NoticeConversationReplaced {
		t.Fatalf("replacement notice=%q err=%v", first.Notice, err)
	}
	closeService(t, replacedAgain)
}

func TestCreationPreferenceFailuresKeepCommittedRecordDiscoverable(t *testing.T) {
	ctx := context.Background()
	paths, workspace := fixtureWorkspace(t)
	opened := openResolvedFixture(t, ctx, paths, workspace)
	chat := opened.(*service)
	loaded, _ := chat.Load(ctx)
	generation := loaded.Conversation.Generation

	// Directory sync failure after replacement: committed with a warning.
	chat.prefs = platform.NewPreferenceStoreWithHooks(paths.Workspaces, platform.PreferenceHooks{SyncDirectory: func(string) error { return errors.New("sync") }})
	warned, err := chat.CreateConversation(ctx, generation)
	if err != nil || !warned.DurabilityWarning || warned.Snapshot.Notice != NoticeSelectionUnconfirmed || warned.Snapshot.Conversation.Number != 2 {
		t.Fatalf("warned=%#v err=%v", warned, err)
	}
	generation = warned.Snapshot.Conversation.Generation

	// Readback failure after replacement: the number reservation is uncertain,
	// so no session is created and mutations block until a locked reread.
	var reads atomic.Int64
	chat.prefs = platform.NewPreferenceStoreWithHooks(paths.Workspaces, platform.PreferenceHooks{ReadBack: func(path string) ([]byte, error) {
		if reads.Add(1) == 2 {
			return nil, errors.New("unreadable")
		}
		return os.ReadFile(path)
	}})
	partial, err := chat.CreateConversation(ctx, generation)
	if !errors.Is(err, ErrReconciliationRequired) || partial.CommittedID != "" {
		t.Fatalf("reconcile error=%v result=%#v", err, partial)
	}
	if _, err := chat.Start(ctx, "blocked", fixtureStartConfig()); !errors.Is(err, ErrReconciliationRequired) {
		t.Fatalf("start during reconciliation error=%v", err)
	}
	if _, err := chat.SelectConversation(ctx, loaded.Conversation.ID, generation); !errors.Is(err, ErrReconciliationRequired) {
		t.Fatalf("select during reconciliation error=%v", err)
	}
	chat.prefs = platform.NewPreferenceStore(paths.Workspaces)
	reconciled, err := chat.Load(ctx)
	if err != nil || reconciled.Conversation.Number != 2 || reconciled.Conversation.Generation != generation+1 {
		t.Fatalf("reconciled=%#v err=%v", reconciled, err)
	}
	generation = reconciled.Conversation.Generation
	page, _ := chat.ListConversations(ctx, "")
	if len(page.Items) != 2 || page.Items[0].Number != 2 || !page.Items[0].Selected {
		t.Fatalf("directory after reconcile=%#v", page)
	}
	if _, err := chat.Start(ctx, "unblocked", fixtureStartConfig()); err != nil {
		t.Fatalf("start after reconciliation: %v", err)
	}
	chat.mu.Lock()
	active := chat.active
	chat.mu.Unlock()
	<-active.run.Finished()

	// Failure after session creation but before the selection is remembered:
	// the record stays discoverable and selection retry succeeds without a
	// duplicate creation. The reserved number 3 was consumed by the failed
	// attempt above, so the new record is number 4.
	chat.prefs = platform.NewPreferenceStoreWithHooks(paths.Workspaces, platform.PreferenceHooks{SyncDirectory: func(directory string) error {
		return os.Chmod(directory, 0o500)
	}})
	t.Cleanup(func() { _ = os.Chmod(paths.Workspaces, 0o700) })
	failed, err := chat.CreateConversation(ctx, generation)
	if !errors.Is(err, ErrPreferenceFailed) || failed.CommittedID == "" {
		t.Fatalf("pre-replacement failure error=%v result=%#v", err, failed)
	}
	if err := os.Chmod(paths.Workspaces, 0o700); err != nil {
		t.Fatal(err)
	}
	chat.prefs = platform.NewPreferenceStore(paths.Workspaces)
	current, _ := chat.Load(ctx)
	if current.Conversation.Number != 2 || current.Conversation.Generation != generation {
		t.Fatalf("selection changed after failed commit: %#v", current.Conversation)
	}
	page, _ = chat.ListConversations(ctx, "")
	if len(page.Items) != 3 || page.Items[0].ID != failed.CommittedID || page.Items[0].Number != 4 || page.Items[0].Selected {
		t.Fatalf("committed record not discoverable: %#v", page)
	}
	retried, err := chat.SelectConversation(ctx, failed.CommittedID, generation)
	if err != nil || retried.Snapshot.Conversation.Number != 4 || retried.Snapshot.Conversation.ID != failed.CommittedID {
		t.Fatalf("retry select=%#v err=%v", retried, err)
	}
	closeService(t, chat)
}

func TestSelectingDestinationWithUnfinishedRunEntersRecoveryWait(t *testing.T) {
	ctx := context.Background()
	paths, workspace := fixtureWorkspace(t)
	owner := openResolvedFixtureWithResolver(t, ctx, paths, workspace, demomodel.DynamicResolver(demomodel.TimerWait(10*time.Second)))
	loaded, _ := owner.Load(ctx)
	created, err := owner.CreateConversation(ctx, loaded.Conversation.Generation)
	if err != nil {
		t.Fatal(err)
	}
	active, err := owner.Start(ctx, "owned elsewhere", fixtureStartConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.CreateConversation(ctx, created.Snapshot.Conversation.Generation); !errors.Is(err, ErrBusy) {
		t.Fatalf("create during run error=%v", err)
	}
	if _, err := owner.SelectConversation(ctx, loaded.Conversation.ID, created.Snapshot.Conversation.Generation); !errors.Is(err, ErrBusy) {
		t.Fatalf("select during run error=%v", err)
	}
	if _, err := owner.RenameConversation(ctx, created.Snapshot.Conversation.ID, created.Snapshot.Conversation.Generation, "busy"); !errors.Is(err, ErrBusy) {
		t.Fatalf("rename during run error=%v", err)
	}
	if page, err := owner.ListConversations(ctx, ""); err != nil || len(page.Items) != 2 {
		t.Fatalf("directory during run=%#v err=%v", page, err)
	}

	contender, err := openFixture(ctx, paths, workspace)
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := contender.Load(ctx)
	if err != nil || waiting.Phase != PhaseRecoveryWaiting || waiting.Conversation.ID != created.Snapshot.Conversation.ID {
		t.Fatalf("contender load=%#v err=%v", waiting, err)
	}
	if _, err := contender.SelectConversation(ctx, loaded.Conversation.ID, waiting.Conversation.Generation); !errors.Is(err, ErrBusy) {
		t.Fatalf("select while waiting error=%v", err)
	}
	closeService(t, owner)
	<-active.Run.Finished()
	recovered, err := contender.Recover(ctx)
	if err != nil || recovered.Kind != ActionStarted {
		t.Fatalf("recover=%#v err=%v", recovered, err)
	}
	terminal, _ := drainRun(t, recovered.Run)
	if terminal.Conversation.ID != created.Snapshot.Conversation.ID || len(userContents(terminal.Messages)) != 1 {
		t.Fatalf("recovered terminal=%#v", terminal)
	}
	other, err := contender.SelectConversation(ctx, loaded.Conversation.ID, terminal.Conversation.Generation)
	if err != nil || other.Snapshot.Phase != PhaseIdle || len(other.Snapshot.Messages) != 0 {
		t.Fatalf("other conversation=%#v err=%v", other.Snapshot, err)
	}
	closeService(t, contender)
}

func TestCloseCancelsBlockedMutationAndReleasesResources(t *testing.T) {
	ctx := context.Background()
	paths, workspace := fixtureWorkspace(t)
	opened := openResolvedFixture(t, ctx, paths, workspace)
	loaded, _ := opened.Load(ctx)
	held := make(chan struct{})
	entered := make(chan struct{})
	go func() {
		_ = platform.NewPreferenceStore(paths.Workspaces).WithWorkspace(ctx, workspace.ID, func(*platform.LockedPreferences) error {
			close(entered)
			<-held
			return nil
		})
	}()
	<-entered
	createErr := make(chan error, 1)
	go func() { _, err := opened.CreateConversation(ctx, loaded.Conversation.Generation); createErr <- err }()
	time.Sleep(50 * time.Millisecond)
	closeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := opened.Close(closeCtx); err != nil {
		t.Fatalf("close during blocked mutation: %v", err)
	}
	if err := <-createErr; !errors.Is(err, ErrClosing) && !errors.Is(err, context.Canceled) && !errors.Is(err, ErrPreferenceFailed) {
		t.Fatalf("blocked create error=%v", err)
	}
	close(held)
	if _, err := opened.ListConversations(ctx, ""); !errors.Is(err, ErrClosing) {
		t.Fatalf("list after close error=%v", err)
	}
}

func TestDirectoryPagesMarkForeignRecordsAndRecoverFromBadCursors(t *testing.T) {
	ctx := context.Background()
	paths, workspace := fixtureWorkspace(t)
	seed := openTestStore(t, ctx, paths.Database)
	base := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	for i := 1; i <= DirectoryPageSize+5; i++ {
		record := session.Session{
			ID: session.ID("conversation-seed-" + strconv.Itoa(i)), WorkspaceID: workspace.ID, Directory: workspace.Root,
			Metadata: numberMetadata(uint64(i)), CreatedAt: base.Add(time.Duration(i) * time.Second), UpdatedAt: base,
		}
		if i%10 == 0 {
			record.Title = "Titled " + strconv.Itoa(i)
		}
		if _, err := seed.CreateSession(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	foreign := session.Session{ID: "foreign-record", WorkspaceID: workspace.ID, Directory: workspace.Root, Title: "not ours", CreatedAt: base.Add(time.Hour), UpdatedAt: base}
	if _, err := seed.CreateSession(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	service := openResolvedFixture(t, ctx, paths, workspace)
	loaded, _ := service.Load(ctx)
	if loaded.Conversation.ID != "conversation-seed-55" || loaded.Conversation.Number != 55 {
		t.Fatalf("newest valid selection=%#v", loaded.Conversation)
	}
	first, err := service.ListConversations(ctx, "")
	if err != nil || len(first.Items) != DirectoryPageSize || first.NextCursor == "" {
		t.Fatalf("first page=%d cursor=%q err=%v", len(first.Items), first.NextCursor, err)
	}
	if !first.Items[0].Unavailable || first.Items[0].Title != unavailableConversationTitle || first.Items[0].ID != "foreign-record" {
		t.Fatalf("foreign record row=%#v", first.Items[0])
	}
	if first.Items[1].ID != "conversation-seed-55" || !first.Items[1].Selected || first.Items[1].Title != "Conversation 55" || first.Items[6].Title != "Titled 50" {
		t.Fatalf("rows=%#v", first.Items[:7])
	}
	second, err := service.ListConversations(ctx, first.NextCursor)
	if err != nil || len(second.Items) != 6 || second.NextCursor != "" || second.Items[5].Number != 1 {
		t.Fatalf("second page=%#v err=%v", second, err)
	}
	if _, err := service.ListConversations(ctx, "not-a-cursor"); !errors.Is(err, ErrDirectoryCursor) {
		t.Fatalf("bad cursor error=%v", err)
	}
	if _, err := service.ListConversations(ctx, strings.Repeat("c", 9000)); !errors.Is(err, ErrDirectoryCursor) && !errors.Is(err, ErrDirectoryUnavailable) {
		t.Fatalf("oversized cursor error=%v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := service.ListConversations(canceled, ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled list error=%v", err)
	}
	if _, err := service.SelectConversation(ctx, "foreign-record", loaded.Conversation.Generation); !errors.Is(err, ErrConversationUnavailable) {
		t.Fatalf("foreign select error=%v", err)
	}
	created, err := service.CreateConversation(ctx, loaded.Conversation.Generation)
	if err != nil || created.Snapshot.Conversation.Number != 56 {
		t.Fatalf("counter reconciled from directory: %#v err=%v", created.Snapshot.Conversation, err)
	}
	closeService(t, service)
}

func TestAgentRenameToolUpdatesTitleActivityAndReplay(t *testing.T) {
	ctx := context.Background()
	paths, workspace := fixtureWorkspace(t)
	service := openResolvedFixtureWithResolver(t, ctx, paths, workspace, demomodel.DynamicRenameToolResolver(nil))
	terminal := finishTurn(t, service, "please rename this conversation")
	if terminal.Conversation.Title != demomodel.RenameToolTitle || terminal.Notice != "" {
		t.Fatalf("terminal=%#v", terminal)
	}
	var activity *ToolActivity
	for i := range terminal.Messages {
		for j := range terminal.Messages[i].Tools {
			activity = &terminal.Messages[i].Tools[j]
		}
	}
	if activity == nil || activity.Name != conversationtools.Name || activity.Subject != conversationtools.Subject || activity.Status != ToolCompleted {
		t.Fatalf("activity=%#v", activity)
	}
	if strings.Contains(activity.Subject, demomodel.RenameToolTitle) {
		t.Fatal("title argument rendered as subject")
	}
	page, _ := service.ListConversations(ctx, "")
	if len(page.Items) != 1 || page.Items[0].Title != demomodel.RenameToolTitle {
		t.Fatalf("directory=%#v", page)
	}
	closeService(t, service)
	reopened := openResolvedFixture(t, ctx, paths, workspace)
	replay, err := reopened.Load(ctx)
	if err != nil || replay.Conversation.Title != demomodel.RenameToolTitle || len(replay.Messages) != 3 {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	closeService(t, reopened)
	names := EnabledToolNames()
	if strings.Join(names, ",") != "file_read,file_list,glob,search,rename_conversation" {
		t.Fatalf("allowlist=%v", names)
	}
}
