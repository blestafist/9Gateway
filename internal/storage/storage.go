// Package storage owns the gateway's SQLite lifecycle.
//
// The modernc.org/sqlite driver is CGo-free, so the gateway remains a single
// statically deployable Go binary without a system SQLite library at runtime.
// Its SQLite implementation and driver dependencies are compiled into that
// binary for the supported Go target.
package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	_ "modernc.org/sqlite"
)

const (
	busyTimeoutMilliseconds = 5000
	fileMaxOpenConnections  = 4
	fileMaxIdleConnections  = 4
)

// DB is the gateway-owned SQLite handle. Its embedded sql.DB keeps SQL access
// available to storage migrations and repositories while Close remains safe to
// call more than once during startup failure or shutdown cleanup.
type DB struct {
	*sql.DB

	closeOnce sync.Once
	closeErr  error
}

var memoryDatabaseID atomic.Uint64

// Open validates, opens, configures, and pings the SQLite database at path.
// Parent directories are intentionally never created. The returned handle is
// ready for migrations and must be closed by its owner.
func Open(ctx context.Context, path string) (*DB, error) {
	if ctx == nil {
		return nil, errors.New("open sqlite: nil context")
	}
	if err := validatePath(path); err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	inMemory := path == ":memory:"
	database, err := sql.Open("sqlite", dataSource(path, inMemory))
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	if inMemory {
		// A private :memory: database exists once per connection. One pooled
		// connection makes its state deterministic for tests and startup work.
		database.SetMaxOpenConns(1)
		database.SetMaxIdleConns(1)
	} else {
		database.SetMaxOpenConns(fileMaxOpenConnections)
		database.SetMaxIdleConns(fileMaxIdleConnections)
	}

	result := &DB{DB: database}
	if err := result.configureAndPing(ctx, inMemory); err != nil {
		_ = result.Close()
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	return result, nil
}

func (database *DB) configureAndPing(ctx context.Context, inMemory bool) error {
	if err := database.PingContext(ctx); err != nil {
		return fmt.Errorf("ping: %w", err)
	}

	var foreignKeys, busyTimeout int
	if err := database.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("read foreign key setting: %w", err)
	}
	if foreignKeys != 1 {
		return fmt.Errorf("foreign keys are not enabled")
	}
	if err := database.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		return fmt.Errorf("read busy timeout setting: %w", err)
	}
	if busyTimeout != busyTimeoutMilliseconds {
		return fmt.Errorf("busy timeout is %dms, want %dms", busyTimeout, busyTimeoutMilliseconds)
	}

	var journalMode string
	if err := database.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return fmt.Errorf("read journal mode: %w", err)
	}
	wantJournalMode := "wal"
	if inMemory {
		wantJournalMode = "memory"
	}
	if !strings.EqualFold(journalMode, wantJournalMode) {
		return fmt.Errorf("journal mode is %q, want %q", journalMode, wantJournalMode)
	}
	return nil
}

// Close releases all pooled connections. It is idempotent and returns the
// result of the first close operation.
func (database *DB) Close() error {
	if database == nil || database.DB == nil {
		return nil
	}
	database.closeOnce.Do(func() {
		database.closeErr = database.DB.Close()
	})
	return database.closeErr
}

func dataSource(path string, inMemory bool) string {
	query := "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	if inMemory {
		// A shared named memory URI is unnecessary with MaxOpenConns(1), but
		// makes the single-state intent explicit if the pool is inspected. Give
		// every Open call its own name so separate gateway instances cannot
		// accidentally share test state.
		id := memoryDatabaseID.Add(1)
		return fmt.Sprintf("file:gateway-memory-%d?mode=memory&cache=shared&%s", id, query)
	}
	databaseURL := url.URL{Scheme: "file", Path: path, RawQuery: "_pragma=journal_mode(WAL)&" + query}
	return databaseURL.String()
}

func validatePath(path string) error {
	if path == ":memory:" {
		return nil
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("path is required")
	}
	if strings.IndexByte(path, 0) >= 0 {
		return errors.New("path contains NUL byte")
	}

	info, err := os.Stat(path)
	switch {
	case err == nil:
		if info.IsDir() {
			return errors.New("path is a directory")
		}
		if !info.Mode().IsRegular() {
			return errors.New("path is not a regular file")
		}
		if info.Mode().Perm()&0o222 == 0 {
			return errors.New("path is not writable")
		}
		return nil
	case !os.IsNotExist(err):
		return fmt.Errorf("inspect path: %w", err)
	}

	parent := filepath.Dir(path)
	parentInfo, err := os.Stat(parent)
	if err != nil {
		if os.IsNotExist(err) {
			return errors.New("parent directory does not exist")
		}
		return fmt.Errorf("inspect parent directory: %w", err)
	}
	if !parentInfo.IsDir() {
		return errors.New("parent path is not a directory")
	}
	if parentInfo.Mode().Perm()&0o222 == 0 || parentInfo.Mode().Perm()&0o111 == 0 {
		return errors.New("parent directory is not writable")
	}
	return nil
}
