package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"

	"github.com/gofrs/flock"
	"github.com/mattsp1290/eino-agent/session"
)

const (
	// PreferenceFormatVersion is the only accepted record version.
	PreferenceFormatVersion = 1
	// MaxPreferenceBytes bounds a preference record on disk and in memory.
	MaxPreferenceBytes = 4096
	// PreferenceLockTimeout bounds every cross-process lock wait.
	PreferenceLockTimeout = 5 * time.Second
	lockRetryDelay        = 5 * time.Millisecond
	directorySyncRetries  = 3
	directorySyncRetryGap = 20 * time.Millisecond
)

var (
	// ErrLockUnavailable reports a lock that could not be acquired in time or
	// whose file is not a private regular file.
	ErrLockUnavailable = errors.New("state lock unavailable")
	// ErrPreferenceWorkspace reports a selector that is not a workspace ID.
	ErrPreferenceWorkspace = errors.New("workspace preference selector invalid")
	// ErrPreferenceCorrupt reports a malformed, oversized, foreign, or
	// unprotected record. It is never repaired or overwritten automatically.
	ErrPreferenceCorrupt = errors.New("workspace preference record invalid")
	// ErrPreferenceWrite reports a failure before atomic replacement. The
	// previous record is still authoritative.
	ErrPreferenceWrite = errors.New("workspace preference write failed")
	// ErrPreferenceReconcile reports that replacement happened but the
	// committed record could not be read back. Callers must block further
	// mutations until a locked reread succeeds.
	ErrPreferenceReconcile = errors.New("workspace preference requires reconciliation")
	// ErrPreferenceOverflow reports an exhausted per-workspace counter.
	ErrPreferenceOverflow = errors.New("conversation numbers exhausted")
)

// WorkspacePreferences is the complete host-owned record for one workspace.
// It never contains titles, prompts, history, credentials, or paths.
type WorkspacePreferences struct {
	Version                int    `json:"version"`
	WorkspaceID            string `json:"workspace_id"`
	NextNumber             uint64 `json:"next_number"`
	SelectedConversationID string `json:"selected_conversation_id"`
}

// PreferenceWrite is the observed outcome of a committed replacement.
// DurabilityWarning means the directory sync failed after the atomic rename:
// the record is visible to every reader but crash durability is unconfirmed.
type PreferenceWrite struct {
	Committed         WorkspacePreferences
	DurabilityWarning bool
}

// PreferenceHooks are test seams for filesystem failures after replacement.
type PreferenceHooks struct {
	SyncDirectory func(directory string) error
	ReadBack      func(path string) ([]byte, error)
}

// PreferenceStore owns the protected workspace preference directory.
type PreferenceStore struct {
	directory   string
	lockTimeout time.Duration
	hooks       PreferenceHooks
}

// NewPreferenceStore uses the verified workspace preference directory.
func NewPreferenceStore(directory string) *PreferenceStore {
	return NewPreferenceStoreWithHooks(directory, PreferenceHooks{})
}

// NewPreferenceStoreWithHooks is the test seam for injected sync/readback failures.
func NewPreferenceStoreWithHooks(directory string, hooks PreferenceHooks) *PreferenceStore {
	if hooks.SyncDirectory == nil {
		hooks.SyncDirectory = syncDirectory
	}
	if hooks.ReadBack == nil {
		hooks.ReadBack = os.ReadFile
	}
	return &PreferenceStore{directory: directory, lockTimeout: PreferenceLockTimeout, hooks: hooks}
}

// LockedPreferences is the handle held while the workspace lock is owned.
// Its methods never reacquire the lock.
type LockedPreferences struct {
	store       *PreferenceStore
	workspaceID string
	path        string
	current     WorkspacePreferences
	fresh       bool
	uncertain   bool
}

// AcquireLock takes an exclusive cross-process lock on a private lock file and
// returns its release function. The lock file is never removed.
func AcquireLock(ctx context.Context, path string, timeout time.Duration) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !ownedByCurrentUser(info) {
			return nil, ErrLockUnavailable
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, ErrLockUnavailable
	}
	lock := flock.New(path, flock.SetPermissions(0o600))
	lockCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	locked, err := lock.TryLockContext(lockCtx, lockRetryDelay)
	if err != nil || !locked {
		_ = lock.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, ErrLockUnavailable
	}
	return func() { _ = lock.Close() }, nil
}

// WithWorkspace runs fn while holding that workspace's preference lock. The
// record is read and validated before fn runs; a missing record is fresh.
func (p *PreferenceStore) WithWorkspace(ctx context.Context, workspaceID string, fn func(*LockedPreferences) error) error {
	if !ValidWorkspaceID(workspaceID) {
		return ErrPreferenceWorkspace
	}
	release, err := AcquireLock(ctx, filepath.Join(p.directory, workspaceID+".lock"), p.lockTimeout)
	if err != nil {
		return err
	}
	defer release()
	locked := &LockedPreferences{store: p, workspaceID: workspaceID, path: filepath.Join(p.directory, workspaceID+".json")}
	if err := locked.load(); err != nil {
		return err
	}
	return fn(locked)
}

// ReserveConversationNumber reserves the next number under the workspace lock.
func ReserveConversationNumber(ctx context.Context, store *PreferenceStore, workspaceID string) (uint64, PreferenceWrite, error) {
	var number uint64
	var write PreferenceWrite
	err := store.WithWorkspace(ctx, workspaceID, func(locked *LockedPreferences) error {
		var err error
		number, write, err = locked.ReserveNumber()
		return err
	})
	return number, write, err
}

// RememberConversation persists the selected conversation under the workspace lock.
func RememberConversation(ctx context.Context, store *PreferenceStore, workspaceID string, id session.ID) (PreferenceWrite, error) {
	var write PreferenceWrite
	err := store.WithWorkspace(ctx, workspaceID, func(locked *LockedPreferences) error {
		var err error
		write, err = locked.Remember(id)
		return err
	})
	return write, err
}

// ReadWorkspacePreferences returns the validated record and whether it is fresh.
func ReadWorkspacePreferences(ctx context.Context, store *PreferenceStore, workspaceID string) (WorkspacePreferences, bool, error) {
	var record WorkspacePreferences
	fresh := false
	err := store.WithWorkspace(ctx, workspaceID, func(locked *LockedPreferences) error {
		record, fresh = locked.Current(), locked.Fresh()
		return nil
	})
	return record, fresh, err
}

// Current returns the validated record, or the fresh initial state.
func (l *LockedPreferences) Current() WorkspacePreferences { return l.current }

// Fresh reports that no record existed when the lock was taken.
func (l *LockedPreferences) Fresh() bool { return l.fresh }

// ReserveNumber consumes the next monotonic number. A reservation observed
// after replacement stays consumed even if the caller later aborts.
func (l *LockedPreferences) ReserveNumber() (uint64, PreferenceWrite, error) {
	if l.uncertain {
		return 0, PreferenceWrite{}, ErrPreferenceReconcile
	}
	number := l.current.NextNumber
	if number == 0 || number == math.MaxUint64 {
		return 0, PreferenceWrite{}, ErrPreferenceOverflow
	}
	next := l.current
	next.NextNumber = number + 1
	write, err := l.replace(next)
	if err != nil {
		return 0, PreferenceWrite{}, err
	}
	return number, write, nil
}

// EnsureNextNumber raises the counter to at least floor. It never lowers it.
func (l *LockedPreferences) EnsureNextNumber(floor uint64) (PreferenceWrite, error) {
	if l.uncertain {
		return PreferenceWrite{}, ErrPreferenceReconcile
	}
	if floor == 0 || floor == math.MaxUint64 {
		return PreferenceWrite{}, ErrPreferenceOverflow
	}
	if !l.fresh && l.current.NextNumber >= floor {
		return PreferenceWrite{Committed: l.current}, nil
	}
	next := l.current
	if next.NextNumber < floor {
		next.NextNumber = floor
	}
	return l.replace(next)
}

// Remember persists the selected conversation. Atomic replacement is the
// logical commit point; the returned outcome reports crash-durability doubt.
func (l *LockedPreferences) Remember(id session.ID) (PreferenceWrite, error) {
	if l.uncertain {
		return PreferenceWrite{}, ErrPreferenceReconcile
	}
	if !validConversationSelector(string(id)) {
		return PreferenceWrite{}, ErrPreferenceWrite
	}
	if !l.fresh && l.current.SelectedConversationID == string(id) {
		return PreferenceWrite{Committed: l.current}, nil
	}
	next := l.current
	next.SelectedConversationID = string(id)
	return l.replace(next)
}

func (l *LockedPreferences) load() error {
	info, err := os.Lstat(l.path)
	if errors.Is(err, os.ErrNotExist) {
		l.fresh = true
		l.current = WorkspacePreferences{Version: PreferenceFormatVersion, WorkspaceID: l.workspaceID, NextNumber: 1}
		return nil
	}
	if err != nil {
		return ErrPreferenceCorrupt
	}
	record, err := readPreferenceFile(l.path, info, l.workspaceID, l.store.hooks.ReadBack)
	if err != nil {
		return err
	}
	l.current = record
	return nil
}

func (l *LockedPreferences) replace(next WorkspacePreferences) (PreferenceWrite, error) {
	data, err := json.Marshal(next)
	if err != nil || len(data) > MaxPreferenceBytes {
		return PreferenceWrite{}, ErrPreferenceWrite
	}
	temp, err := os.CreateTemp(l.store.directory, "."+l.workspaceID+".tmp-")
	if err != nil {
		return PreferenceWrite{}, ErrPreferenceWrite
	}
	tempPath := temp.Name()
	if err := writeAndSync(temp, data); err != nil {
		_ = os.Remove(tempPath)
		return PreferenceWrite{}, ErrPreferenceWrite
	}
	if err := os.Rename(tempPath, l.path); err != nil {
		_ = os.Remove(tempPath)
		return PreferenceWrite{}, ErrPreferenceWrite
	}
	// Replacement happened: from here the new record is what every reader sees.
	syncErr := l.store.hooks.SyncDirectory(l.store.directory)
	for attempt := 0; syncErr != nil && attempt < directorySyncRetries; attempt++ {
		time.Sleep(directorySyncRetryGap)
		syncErr = l.store.hooks.SyncDirectory(l.store.directory)
	}
	info, err := os.Lstat(l.path)
	if err != nil {
		l.uncertain = true
		return PreferenceWrite{}, ErrPreferenceReconcile
	}
	observed, err := readPreferenceFile(l.path, info, l.workspaceID, l.store.hooks.ReadBack)
	if err != nil || observed != next {
		l.uncertain = true
		return PreferenceWrite{}, ErrPreferenceReconcile
	}
	l.current = observed
	l.fresh = false
	return PreferenceWrite{Committed: observed, DurabilityWarning: syncErr != nil}, nil
}

func writeAndSync(file *os.File, data []byte) error {
	defer func() { _ = file.Close() }()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return file.Close()
}

func syncDirectory(directory string) error {
	dir, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	return dir.Sync()
}

func readPreferenceFile(path string, info os.FileInfo, workspaceID string, read func(string) ([]byte, error)) (WorkspacePreferences, error) {
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !ownedByCurrentUser(info) || info.Size() > MaxPreferenceBytes {
		return WorkspacePreferences{}, ErrPreferenceCorrupt
	}
	data, err := read(path)
	if err != nil || len(data) > MaxPreferenceBytes {
		return WorkspacePreferences{}, ErrPreferenceCorrupt
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var record WorkspacePreferences
	if err := decoder.Decode(&record); err != nil {
		return WorkspacePreferences{}, ErrPreferenceCorrupt
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return WorkspacePreferences{}, ErrPreferenceCorrupt
	}
	if record.Version != PreferenceFormatVersion || record.WorkspaceID != workspaceID || record.NextNumber == 0 ||
		(record.SelectedConversationID != "" && !validConversationSelector(record.SelectedConversationID)) {
		return WorkspacePreferences{}, ErrPreferenceCorrupt
	}
	return record, nil
}

func validConversationSelector(value string) bool {
	return value != "" && len(value) <= session.DiscoveryMaxIdentityBytes && utf8.ValidString(value)
}

// String keeps preference records out of diagnostics: only the shape is shown.
func (p WorkspacePreferences) String() string {
	return fmt.Sprintf("preferences{version=%d next=%d selected=%t}", p.Version, p.NextNumber, p.SelectedConversationID != "")
}
