package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mattsp1290/eino-agent/session"
)

func recordPath(dir, workspaceID string) string {
	return filepath.Join(dir, workspaceID+".json")
}

func mustWriteFile(t *testing.T, path string, data []byte, perm os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, perm); err != nil {
		t.Fatal(err)
	}
}

func mustWriteRecord(t *testing.T, path string, record WorkspacePreferences) {
	t.Helper()
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, path, data, 0o600)
}

func TestReadWorkspacePreferencesFreshHasNoSideEffects(t *testing.T) {
	dir := t.TempDir()
	store := NewPreferenceStore(dir)
	wsID := WorkspaceID(dir)
	ctx := context.Background()

	record, fresh, err := ReadWorkspacePreferences(ctx, store, wsID)
	if err != nil {
		t.Fatal(err)
	}
	if !fresh {
		t.Fatal("expected fresh record")
	}
	if record.Version != PreferenceFormatVersion || record.NextNumber != 1 || record.SelectedConversationID != "" {
		t.Fatalf("unexpected fresh record: %+v", record)
	}
	if record.WorkspaceID != wsID {
		t.Fatalf("workspace id = %q, want %q", record.WorkspaceID, wsID)
	}
	if _, err := os.Stat(recordPath(dir, wsID)); !os.IsNotExist(err) {
		t.Fatalf("read created a record file: err=%v", err)
	}
}

func TestReserveConversationNumberRestartSafeSequenceAndDiskShape(t *testing.T) {
	dir := t.TempDir()
	wsID := WorkspaceID(dir)
	ctx := context.Background()
	path := recordPath(dir, wsID)

	for want := uint64(1); want <= 3; want++ {
		// A fresh PreferenceStore on every call simulates a process restart.
		store := NewPreferenceStore(dir)
		got, write, err := ReserveConversationNumber(ctx, store, wsID)
		if err != nil {
			t.Fatalf("reserve #%d: %v", want, err)
		}
		if got != want {
			t.Fatalf("reserve #%d = %d, want %d", want, got, want)
		}
		if write.Committed.NextNumber != want+1 {
			t.Fatalf("committed next_number = %d, want %d", write.Committed.NextNumber, want+1)
		}
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("record file mode = %o, want 0600", info.Mode().Perm())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	wantKeys := []string{"version", "workspace_id", "next_number", "selected_conversation_id"}
	if len(raw) != len(wantKeys) {
		t.Fatalf("record keys = %v, want exactly %v", raw, wantKeys)
	}
	for _, key := range wantKeys {
		if _, ok := raw[key]; !ok {
			t.Fatalf("record missing key %q: %v", key, raw)
		}
	}
}

func TestRememberConversationPersistsAndDedupesWrites(t *testing.T) {
	dir := t.TempDir()
	wsID := WorkspaceID(dir)
	ctx := context.Background()
	path := recordPath(dir, wsID)

	write, err := RememberConversation(ctx, NewPreferenceStore(dir), wsID, session.ID("conversation-abc"))
	if err != nil {
		t.Fatal(err)
	}
	if write.Committed.SelectedConversationID != "conversation-abc" {
		t.Fatalf("committed = %+v", write.Committed)
	}

	record, fresh, err := ReadWorkspacePreferences(ctx, NewPreferenceStore(dir), wsID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh {
		t.Fatal("expected non-fresh record after remembering a conversation")
	}
	if record.SelectedConversationID != "conversation-abc" {
		t.Fatalf("record = %+v", record)
	}

	infoBefore, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	dataBefore, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Remembering the same ID again must not write.
	write2, err := RememberConversation(ctx, NewPreferenceStore(dir), wsID, session.ID("conversation-abc"))
	if err != nil {
		t.Fatal(err)
	}
	if write2.DurabilityWarning {
		t.Fatal("unexpected durability warning on a no-op remember")
	}

	infoAfter, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	dataAfter, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(infoBefore, infoAfter) {
		t.Fatal("file identity changed on a no-op remember")
	}
	if !bytes.Equal(dataBefore, dataAfter) {
		t.Fatal("file content changed on a no-op remember")
	}
}

func TestWorkspaceScopingIsExactAndRejectsInvalidSelectors(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	wsA := WorkspaceID("/a")
	wsB := WorkspaceID("/b")
	storeA := NewPreferenceStore(dir)
	storeB := NewPreferenceStore(dir)

	if n, _, err := ReserveConversationNumber(ctx, storeA, wsA); err != nil || n != 1 {
		t.Fatalf("A#1 = %d, %v", n, err)
	}
	if n, _, err := ReserveConversationNumber(ctx, storeA, wsA); err != nil || n != 2 {
		t.Fatalf("A#2 = %d, %v", n, err)
	}
	if n, _, err := ReserveConversationNumber(ctx, storeB, wsB); err != nil || n != 1 {
		t.Fatalf("B#1 = %d, %v", n, err)
	}

	if _, err := RememberConversation(ctx, storeA, wsA, session.ID("selected-a")); err != nil {
		t.Fatal(err)
	}
	if _, err := RememberConversation(ctx, storeB, wsB, session.ID("selected-b")); err != nil {
		t.Fatal(err)
	}

	recA, _, err := ReadWorkspacePreferences(ctx, storeA, wsA)
	if err != nil {
		t.Fatal(err)
	}
	recB, _, err := ReadWorkspacePreferences(ctx, storeB, wsB)
	if err != nil {
		t.Fatal(err)
	}
	if recA.NextNumber != 3 || recA.SelectedConversationID != "selected-a" {
		t.Fatalf("workspace A record = %+v", recA)
	}
	if recB.NextNumber != 2 || recB.SelectedConversationID != "selected-b" {
		t.Fatalf("workspace B record = %+v", recB)
	}

	if _, _, err := ReadWorkspacePreferences(ctx, storeA, "not-a-workspace"); !errors.Is(err, ErrPreferenceWorkspace) {
		t.Fatalf("err = %v, want ErrPreferenceWorkspace", err)
	}
}

func TestCorruptRecordsAreRejectedWithoutOverwrite(t *testing.T) {
	validBase := func(workspaceID string) WorkspacePreferences {
		return WorkspacePreferences{Version: PreferenceFormatVersion, WorkspaceID: workspaceID, NextNumber: 1}
	}

	cases := []struct {
		name  string
		setup func(t *testing.T, dir, workspaceID, path string)
	}{
		{
			name: "malformed json",
			setup: func(t *testing.T, dir, workspaceID, path string) {
				mustWriteFile(t, path, []byte("{not json"), 0o600)
			},
		},
		{
			name: "unknown field",
			setup: func(t *testing.T, dir, workspaceID, path string) {
				raw := fmt.Sprintf(`{"version":%d,"workspace_id":%q,"next_number":1,"selected_conversation_id":"","extra":"nope"}`,
					PreferenceFormatVersion, workspaceID)
				mustWriteFile(t, path, []byte(raw), 0o600)
			},
		},
		{
			name: "wrong version",
			setup: func(t *testing.T, dir, workspaceID, path string) {
				record := validBase(workspaceID)
				record.Version = PreferenceFormatVersion + 1
				mustWriteRecord(t, path, record)
			},
		},
		{
			name: "wrong workspace id",
			setup: func(t *testing.T, dir, workspaceID, path string) {
				record := validBase(WorkspaceID("/some/other/workspace"))
				mustWriteRecord(t, path, record)
			},
		},
		{
			name: "next number zero",
			setup: func(t *testing.T, dir, workspaceID, path string) {
				record := validBase(workspaceID)
				record.NextNumber = 0
				mustWriteRecord(t, path, record)
			},
		},
		{
			name: "oversized",
			setup: func(t *testing.T, dir, workspaceID, path string) {
				huge := bytes.Repeat([]byte("a"), MaxPreferenceBytes+1)
				mustWriteFile(t, path, huge, 0o600)
			},
		},
		{
			name: "symlinked record",
			setup: func(t *testing.T, dir, workspaceID, path string) {
				target := filepath.Join(dir, "symlink-target.json")
				mustWriteRecord(t, target, validBase(workspaceID))
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "non regular file",
			setup: func(t *testing.T, dir, workspaceID, path string) {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			wsID := WorkspaceID(dir)
			path := recordPath(dir, wsID)
			tc.setup(t, dir, wsID, path)

			before, err := os.Lstat(path)
			if err != nil {
				t.Fatal(err)
			}
			var beforeData []byte
			var beforeTarget string
			switch {
			case before.Mode()&os.ModeSymlink != 0:
				beforeTarget, err = os.Readlink(path)
				if err != nil {
					t.Fatal(err)
				}
			case before.Mode().IsRegular():
				beforeData, err = os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
			}

			ctx := context.Background()
			if _, _, err := ReadWorkspacePreferences(ctx, NewPreferenceStore(dir), wsID); !errors.Is(err, ErrPreferenceCorrupt) {
				t.Fatalf("read err = %v, want ErrPreferenceCorrupt", err)
			}
			assertUnchanged(t, path, before, beforeData, beforeTarget)

			// A corrupt record must also block reservation without writing.
			if _, _, err := ReserveConversationNumber(ctx, NewPreferenceStore(dir), wsID); !errors.Is(err, ErrPreferenceCorrupt) {
				t.Fatalf("reserve err = %v, want ErrPreferenceCorrupt", err)
			}
			assertUnchanged(t, path, before, beforeData, beforeTarget)
		})
	}
}

func assertUnchanged(t *testing.T, path string, before os.FileInfo, beforeData []byte, beforeTarget string) {
	t.Helper()
	after, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode() != after.Mode() {
		t.Fatalf("mode changed: %v -> %v", before.Mode(), after.Mode())
	}
	switch {
	case before.Mode()&os.ModeSymlink != 0:
		afterTarget, err := os.Readlink(path)
		if err != nil {
			t.Fatal(err)
		}
		if afterTarget != beforeTarget {
			t.Fatalf("symlink target changed: %q -> %q", beforeTarget, afterTarget)
		}
	case before.Mode().IsRegular():
		afterData, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(beforeData, afterData) {
			t.Fatal("regular file content changed")
		}
	}
}

func TestStaleTempFileIsIgnored(t *testing.T) {
	dir := t.TempDir()
	wsID := WorkspaceID(dir)
	ctx := context.Background()

	if _, _, err := ReserveConversationNumber(ctx, NewPreferenceStore(dir), wsID); err != nil {
		t.Fatal(err)
	}

	stale := filepath.Join(dir, "."+wsID+".tmp-999999")
	mustWriteFile(t, stale, []byte("garbage-not-json"), 0o600)

	record, fresh, err := ReadWorkspacePreferences(ctx, NewPreferenceStore(dir), wsID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh {
		t.Fatal("unexpected fresh record with a real record present")
	}
	if record.NextNumber != 2 {
		t.Fatalf("record = %+v, want next_number 2", record)
	}
}

func TestReserveConversationNumberOverflow(t *testing.T) {
	dir := t.TempDir()
	wsID := WorkspaceID(dir)
	path := recordPath(dir, wsID)
	mustWriteRecord(t, path, WorkspacePreferences{Version: PreferenceFormatVersion, WorkspaceID: wsID, NextNumber: math.MaxUint64})

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := ReserveConversationNumber(context.Background(), NewPreferenceStore(dir), wsID); !errors.Is(err, ErrPreferenceOverflow) {
		t.Fatalf("err = %v, want ErrPreferenceOverflow", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("record was written despite counter overflow")
	}
}

func TestDirectorySyncFailureSetsDurabilityWarning(t *testing.T) {
	dir := t.TempDir()
	wsID := WorkspaceID(dir)
	ctx := context.Background()

	hooks := PreferenceHooks{SyncDirectory: func(string) error { return errors.New("sync failed") }}
	store := NewPreferenceStoreWithHooks(dir, hooks)

	write, err := RememberConversation(ctx, store, wsID, session.ID("durability-check"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !write.DurabilityWarning {
		t.Fatal("expected DurabilityWarning to be true")
	}
	if write.Committed.SelectedConversationID != "durability-check" {
		t.Fatalf("committed = %+v", write.Committed)
	}

	record, fresh, err := ReadWorkspacePreferences(ctx, NewPreferenceStore(dir), wsID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh || record.SelectedConversationID != "durability-check" {
		t.Fatalf("record not committed to disk: %+v fresh=%v", record, fresh)
	}
}

func TestReadbackFailureAfterReplaceReturnsReconcileError(t *testing.T) {
	dir := t.TempDir()
	wsID := WorkspaceID(dir)
	ctx := context.Background()
	path := recordPath(dir, wsID)

	// Seed a valid record using a normal store first.
	if _, err := RememberConversation(ctx, NewPreferenceStore(dir), wsID, session.ID("seed")); err != nil {
		t.Fatal(err)
	}

	calls := 0
	hooks := PreferenceHooks{
		ReadBack: func(p string) ([]byte, error) {
			calls++
			if calls == 1 {
				// Initial load inside WithWorkspace succeeds normally.
				return os.ReadFile(p)
			}
			// Post-rename readback inside replace() fails.
			return nil, errors.New("readback failed")
		},
	}
	store := NewPreferenceStoreWithHooks(dir, hooks)

	err := store.WithWorkspace(ctx, wsID, func(locked *LockedPreferences) error {
		_, err := locked.Remember(session.ID("replaced"))
		return err
	})
	if !errors.Is(err, ErrPreferenceReconcile) {
		t.Fatalf("err = %v, want ErrPreferenceReconcile", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var onDisk WorkspacePreferences
	if err := json.Unmarshal(data, &onDisk); err != nil {
		t.Fatal(err)
	}
	if onDisk.SelectedConversationID != "replaced" {
		t.Fatalf("on-disk record = %+v, want the replaced record to be committed despite the reconcile error", onDisk)
	}
}

func TestReserveConversationNumberContextCancellationWhileLockHeld(t *testing.T) {
	dir := t.TempDir()
	wsID := WorkspaceID(dir)
	store := NewPreferenceStore(dir)

	lockedCh := make(chan struct{})
	releaseCh := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- store.WithWorkspace(context.Background(), wsID, func(*LockedPreferences) error {
			close(lockedCh)
			<-releaseCh
			return nil
		})
	}()
	<-lockedCh

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	start := time.Now()
	_, _, err := ReserveConversationNumber(ctx, NewPreferenceStore(dir), wsID)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want a context cancellation error", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("cancellation was not prompt: %v", elapsed)
	}

	close(releaseCh)
	if err := <-done; err != nil {
		t.Fatalf("lock holder returned error: %v", err)
	}
}

func TestConcurrentReservationsYieldDistinctNumbers(t *testing.T) {
	dir := t.TempDir()
	wsID := WorkspaceID(dir)
	const workers = 20

	results := make(chan uint64, workers)
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for i := 0; i < workers; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			n, _, err := ReserveConversationNumber(context.Background(), NewPreferenceStore(dir), wsID)
			if err != nil {
				errs <- err
				return
			}
			results <- n
		}()
	}
	group.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("reservation failed: %v", err)
	}
	seen := make(map[uint64]bool, workers)
	for n := range results {
		if seen[n] {
			t.Fatalf("duplicate reservation number %d", n)
		}
		seen[n] = true
	}
	if len(seen) != workers {
		t.Fatalf("got %d distinct numbers, want %d", len(seen), workers)
	}
	for i := uint64(1); i <= workers; i++ {
		if !seen[i] {
			t.Fatalf("missing reservation number %d", i)
		}
	}
}

func TestEnsureNextNumberRaisesButNeverLowers(t *testing.T) {
	dir := t.TempDir()
	wsID := WorkspaceID(dir)
	ctx := context.Background()
	path := recordPath(dir, wsID)
	store := NewPreferenceStore(dir)

	var committed WorkspacePreferences
	if err := store.WithWorkspace(ctx, wsID, func(locked *LockedPreferences) error {
		write, err := locked.EnsureNextNumber(5)
		committed = write.Committed
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if committed.NextNumber != 5 {
		t.Fatalf("committed = %+v, want next_number 5", committed)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("fresh record was not written: %v", err)
	}

	if err := store.WithWorkspace(ctx, wsID, func(locked *LockedPreferences) error {
		write, err := locked.EnsureNextNumber(10)
		committed = write.Committed
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if committed.NextNumber != 10 {
		t.Fatalf("committed = %+v, want next_number raised to 10", committed)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := store.WithWorkspace(ctx, wsID, func(locked *LockedPreferences) error {
		write, err := locked.EnsureNextNumber(3)
		committed = write.Committed
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if committed.NextNumber != 10 {
		t.Fatalf("lowering the floor changed the counter: %+v", committed)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("record was written despite the floor being lower than the counter")
	}
}

func TestAcquireLockRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	mustWriteFile(t, target, nil, 0o600)
	lockPath := filepath.Join(dir, "workspace.lock")
	if err := os.Symlink(target, lockPath); err != nil {
		t.Fatal(err)
	}

	release, err := AcquireLock(context.Background(), lockPath, time.Second)
	if release != nil {
		defer release()
	}
	if !errors.Is(err, ErrLockUnavailable) {
		t.Fatalf("err = %v, want ErrLockUnavailable", err)
	}
}
