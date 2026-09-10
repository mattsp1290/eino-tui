package runtimeui

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-tui/internal/platform"
)

func fileDigest(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

func TestOpenDatabaseInitializesFreshFileAndReopensWithoutMigration(t *testing.T) {
	ctx := context.Background()
	paths, _ := fixtureWorkspace(t)
	store, err := openDatabase(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := store.pool.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal mode=%q err=%v", mode, err)
	}
	var foreignKeys int
	if err := store.pool.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
		t.Fatalf("foreign keys=%d err=%v", foreignKeys, err)
	}
	now := time.Now().UTC()
	if _, err := store.CreateSession(ctx, session.Session{ID: "fresh", WorkspaceID: "w", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openDatabase(ctx, paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if got, err := reopened.GetSession(ctx, "fresh"); err != nil || got.ID != "fresh" {
		t.Fatalf("reopened session=%#v err=%v", got, err)
	}
	if _, err := reopened.ListSessions(ctx, session.SessionDiscoveryQuery{WorkspaceID: "w"}); err != nil {
		t.Fatalf("discovery unavailable after reopen: %v", err)
	}
}

func TestOpenDatabaseRejectsForeignSchemaWithoutTouchingBytes(t *testing.T) {
	ctx := context.Background()
	paths, _ := fixtureWorkspace(t)
	legacy, err := sql.Open("sqlite", paths.Database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.ExecContext(ctx, `CREATE TABLE sessions (id TEXT PRIMARY KEY, record BLOB); INSERT INTO sessions VALUES ('legacy', X'00')`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	before := fileDigest(t, paths.Database)
	if _, err := os.Stat(paths.Database + "-wal"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("legacy fixture unexpectedly in WAL mode")
	}
	store, err := openDatabase(ctx, paths.Database)
	if store != nil || !errors.Is(err, ErrDatabaseUnsupported) {
		t.Fatalf("store=%v err=%v", store, err)
	}
	if fileDigest(t, paths.Database) != before {
		t.Fatal("rejected database bytes changed")
	}
	for _, sidecar := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Stat(paths.Database + sidecar); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("sidecar %s created for a rejected database", sidecar)
		}
	}
	// The old sessions.db of the previous storage generation is never opened.
	oldPath := filepath.Join(paths.Directory, "sessions.db")
	if err := os.WriteFile(oldPath, []byte("previous generation"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, paths, platform.Workspace{Root: t.TempDir(), ID: platform.WorkspaceID("/x")}, Config{Resolver: nil}); err == nil {
		t.Fatal("invalid config accepted")
	}
	if data, _ := os.ReadFile(oldPath); string(data) != "previous generation" {
		t.Fatal("old sessions.db was modified")
	}
}

func TestOpenDatabaseSerializesConcurrentFreshInitialization(t *testing.T) {
	ctx := context.Background()
	paths, _ := fixtureWorkspace(t)
	const workers = 4
	stores := make([]*pooledStore, workers)
	errs := make([]error, workers)
	var group sync.WaitGroup
	for i := range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			stores[i], errs[i] = openDatabase(ctx, paths.Database)
		}()
	}
	group.Wait()
	now := time.Now().UTC()
	for i := range workers {
		if errs[i] != nil {
			t.Fatalf("worker %d: %v", i, errs[i])
		}
		if _, err := stores[i].CreateSession(ctx, session.Session{ID: session.ID("s" + string(rune('a'+i))), WorkspaceID: "w", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("worker %d write: %v", i, err)
		}
	}
	page, err := stores[0].ListSessions(ctx, session.SessionDiscoveryQuery{WorkspaceID: "w"})
	if err != nil || len(page.Sessions) != workers {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	for i := range workers {
		if err := stores[i].Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenDatabaseRejectsSymlinksAndHonorsCancellation(t *testing.T) {
	paths, _ := fixtureWorkspace(t)
	link := filepath.Join(t.TempDir(), "link.db")
	if err := os.Symlink(paths.Database, link); err != nil {
		t.Fatal(err)
	}
	if _, err := openDatabase(context.Background(), link); !errors.Is(err, ErrDatabaseUnavailable) {
		t.Fatalf("symlink error=%v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := openDatabase(canceled, paths.Database); !errors.Is(err, ErrDatabaseUnavailable) {
		t.Fatalf("canceled error=%v", err)
	}
	if _, err := os.Stat(paths.Database + "-wal"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled open configured WAL")
	}
}
