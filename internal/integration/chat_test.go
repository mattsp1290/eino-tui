package integration

import (
	"context"
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/mattsp1290/eino-agent/model"
	agentsqlite "github.com/mattsp1290/eino-agent/store/sqlite"
	"github.com/mattsp1290/eino-tui/internal/codexmodel"
	"github.com/mattsp1290/eino-tui/internal/demomodel"
	"github.com/mattsp1290/eino-tui/internal/platform"
	"github.com/mattsp1290/eino-tui/internal/runtimeui"
)

func openDemoFixture(ctx context.Context, paths platform.Paths, workspace platform.Workspace, resolver model.Resolver) (runtimeui.Service, error) {
	service, err := runtimeui.Open(ctx, paths, workspace, runtimeui.Config{
		Resolver:  resolver,
		AgentName: "fixture", SystemPrompt: "Return only the configured deterministic fixture response.",
	})
	if err != nil {
		return nil, err
	}
	if _, err := service.Load(ctx); err != nil {
		return nil, err
	}
	return service, nil
}

func demoStartConfig() runtimeui.StartConfig {
	return runtimeui.StartConfig{
		Selection:       model.Selection{ProviderID: codexmodel.ProviderID, ModelID: codexmodel.DefaultModel},
		ReasoningEffort: "medium",
	}
}

// openInspectionStore opens a direct, read-only inspection handle onto an
// already-initialized conversation database. It never migrates the schema;
// the runtime under test owns initialization. Callers close the returned
// *sql.DB, not the store.
func openInspectionStore(t *testing.T, ctx context.Context, database string) (*agentsqlite.Store, *sql.DB) {
	t.Helper()
	location := url.URL{Scheme: "file", Path: database, OmitHost: true}
	query := url.Values{}
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "busy_timeout(5000)")
	location.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", location.String())
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	store, err := agentsqlite.New(ctx, db)
	if err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	return store, db
}

func TestProductionWiringDurableJourney(t *testing.T) {
	ctx := context.Background()
	workspaceDir := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := platform.IdentifyWorkspace(workspaceDir)
	if err != nil {
		t.Fatal(err)
	}
	service, err := openDemoFixture(ctx, paths, workspace, demomodel.DynamicResolver(nil))
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Start(ctx, "Unicode λ\nmultiline", demoStartConfig())
	if err != nil {
		t.Fatal(err)
	}
	var terminal runtimeui.Snapshot
	updates := 0
	deadline, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	for {
		snapshot, ok := result.Run.Next(deadline)
		if !ok {
			break
		}
		if snapshot.Terminal {
			terminal = snapshot
		} else {
			updates++
		}
	}
	if updates < 2 || len(terminal.Messages) != 2 {
		t.Fatalf("updates=%d terminal=%#v", updates, terminal)
	}
	closeCtx, closeCancel := context.WithTimeout(ctx, 2*time.Second)
	defer closeCancel()
	if err := service.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	reopened, err := openDemoFixture(ctx, paths, workspace, demomodel.DynamicResolver(nil))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := reopened.Load(ctx)
	if err != nil || len(replay.Messages) != 2 {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	if err := reopened.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}

func TestBackpressuredConsumerReceivesAuthoritativeTerminalReplay(t *testing.T) {
	ctx := context.Background()
	workspaceDir := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := platform.IdentifyWorkspace(workspaceDir)
	if err != nil {
		t.Fatal(err)
	}
	chunks := make([]string, 100)
	for index := range chunks {
		chunks[index] = "x"
	}
	release := make(chan struct{})
	waiter := func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-release:
			return nil
		}
	}
	service, err := openDemoFixture(ctx, paths, workspace, demomodel.DynamicScriptedResolver(waiter, chunks))
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Start(ctx, "backpressure", demoStartConfig())
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-result.Run.Finished():
	case <-time.After(3 * time.Second):
		t.Fatal("run did not settle without consumer")
	}
	var terminal runtimeui.Snapshot
	for {
		snapshot, ok := result.Run.Next(ctx)
		if !ok {
			break
		}
		if snapshot.Terminal {
			terminal = snapshot
		}
	}
	replay, err := service.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !terminal.Terminal || !terminal.Resync || len(terminal.Messages) != len(replay.Messages) || terminal.Messages[1].Content != replay.Messages[1].Content {
		t.Fatalf("terminal=%#v replay=%#v", terminal, replay)
	}
	deadline, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := service.Close(deadline); err != nil {
		t.Fatal(err)
	}
}

func TestLiveLeaseContentionWaitsWithoutStealingThenRecoversTerminalState(t *testing.T) {
	ctx := context.Background()
	workspaceDir := t.TempDir()
	paths, err := platform.PrepareState(ctx, filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := platform.IdentifyWorkspace(workspaceDir)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := openDemoFixture(ctx, paths, workspace, demomodel.DynamicResolver(demomodel.TimerWait(10*time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	active, err := owner.Start(ctx, "owned elsewhere", demoStartConfig())
	if err != nil {
		t.Fatal(err)
	}
	contender, err := openDemoFixture(ctx, paths, workspace, demomodel.DynamicResolver(nil))
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := contender.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Phase != runtimeui.PhaseRecoveryWaiting || loaded.RecoveryAt.IsZero() {
		t.Fatalf("load=%#v", loaded)
	}
	waiting, err := contender.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if waiting.Kind != runtimeui.ActionRecoveryWaiting || waiting.Run != nil {
		t.Fatalf("recover contention=%#v", waiting)
	}
	closeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := owner.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	<-active.Run.Finished()
	recovered, err := contender.Recover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Kind != runtimeui.ActionStarted || recovered.Run == nil {
		t.Fatalf("terminal recover=%#v", recovered)
	}
	select {
	case <-recovered.Run.Finished():
	case <-time.After(2 * time.Second):
		t.Fatal("terminal recovery did not finish")
	}
	if err := contender.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}
