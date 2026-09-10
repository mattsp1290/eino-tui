package runtimeui

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/mattsp1290/eino-agent/session"
	"github.com/mattsp1290/eino-agent/store/sqlite"
	"github.com/mattsp1290/eino-tui/internal/platform"
	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver the store validates
)

var (
	// ErrDatabaseUnsupported reports a nonempty database at the current path
	// whose schema is not the current generation. It is never repaired, reset,
	// or migrated; the operator must choose another state directory.
	ErrDatabaseUnsupported = errors.New("conversation database is unsupported")
	// ErrDatabaseUnavailable reports any other pool, lock, or initialization failure.
	ErrDatabaseUnavailable = errors.New("conversation database is unavailable")
)

const (
	databaseInitLockTimeout = 5 * time.Second
	databaseBusyTimeout     = "busy_timeout(5000)"
)

// durableStore is the host-owned store bundle: public session contracts, the
// discovery capability, and the pool that only the bundle may close.
type durableStore interface {
	session.Store
	session.SessionDiscoveryReader
	Close() error
}

type pooledStore struct {
	*sqlite.Store
	pool *sql.DB
}

func (p *pooledStore) Close() error { return p.pool.Close() }

// openDatabase initializes a zero-byte file with the current schema, validates
// an existing file without changing it, and only then enables WAL. Every
// failure path closes the pool and releases the initialization lock.
func openDatabase(ctx context.Context, path string) (*pooledStore, error) {
	release, err := platform.AcquireLock(ctx, path+".init.lock", databaseInitLockTimeout)
	if err != nil {
		return nil, fmt.Errorf("%w: initialization lock", ErrDatabaseUnavailable)
	}
	defer release()
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || !platform.OwnedByCurrentUser(info) {
		return nil, fmt.Errorf("%w: database file", ErrDatabaseUnavailable)
	}
	pool, err := sql.Open("sqlite", databaseDSN(path))
	if err != nil {
		return nil, fmt.Errorf("%w: pool", ErrDatabaseUnavailable)
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	fresh := info.Size() == 0
	if fresh {
		if err := sqlite.Migrate(ctx, pool); err != nil {
			_ = pool.Close()
			return nil, fmt.Errorf("%w: initialization", ErrDatabaseUnavailable)
		}
	}
	store, err := sqlite.New(ctx, pool)
	if err != nil {
		_ = pool.Close()
		// Upstream signals a foreign or partial schema with session.ErrConflict;
		// every other failure (busy, I/O, permission) is transient, not unsupported.
		if !fresh && errors.Is(err, session.ErrConflict) {
			return nil, ErrDatabaseUnsupported
		}
		return nil, fmt.Errorf("%w: validation", ErrDatabaseUnavailable)
	}
	var mode string
	if err := pool.QueryRowContext(ctx, "PRAGMA journal_mode=WAL").Scan(&mode); err != nil || !strings.EqualFold(mode, "wal") {
		_ = pool.Close()
		return nil, fmt.Errorf("%w: journal configuration", ErrDatabaseUnavailable)
	}
	return &pooledStore{Store: store, pool: pool}, nil
}

// databaseDSN carries only per-connection settings. Persistent journal mode is
// configured after validation, never through the DSN.
func databaseDSN(path string) string {
	location := url.URL{Scheme: "file", Path: path, OmitHost: true}
	query := url.Values{}
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", databaseBusyTimeout)
	location.RawQuery = query.Encode()
	return location.String()
}
