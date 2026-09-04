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
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	_ "modernc.org/sqlite"
)

const (
	busyTimeoutMilliseconds = 5000
	fileMaxOpenConnections  = 4
	fileMaxIdleConnections  = 4

	// CurrentSchemaVersion is the newest schema understood by this binary.
	CurrentSchemaVersion = 1
)

// migrationFiles is embedded in the binary so startup does not depend on an
// external migration executable or a checkout of the source tree.
//
//go:embed migrations/*.sql
var migrationFiles embed.FS

type migration struct {
	version int
	name    string
	sql     string
}

// DB is the gateway-owned SQLite handle. Its embedded sql.DB keeps SQL access
// available to storage migrations and repositories while Close remains safe to
// call more than once during startup failure or shutdown cleanup.
type DB struct {
	*sql.DB

	closeOnce sync.Once
	closeErr  error
}

var memoryDatabaseID atomic.Uint64

// configureAndPingFunc is package-private so startup cleanup can be tested
// after a real connection has been opened without changing the production
// startup path.
var configureAndPingFunc = func(database *DB, ctx context.Context, inMemory bool) error {
	return database.configureAndPing(ctx, inMemory)
}

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
	if err := configureAndPingFunc(result, ctx, inMemory); err != nil {
		_ = result.Close()
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	if err := migrate(ctx, result.DB); err != nil {
		_ = result.Close()
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}
	return result, nil
}

func migrate(ctx context.Context, database *sql.DB) error {
	migrations, err := embeddedMigrations()
	if err != nil {
		return err
	}
	return runMigrations(ctx, database, migrations)
}

func embeddedMigrations() ([]migration, error) {
	entries, err := fs.Glob(migrationFiles, "migrations/*.sql")
	if err != nil {
		return nil, fmt.Errorf("list embedded migrations: %w", err)
	}
	sort.Strings(entries)
	migrations := make([]migration, 0, len(entries))
	for index, name := range entries {
		base := filepath.Base(name)
		separator := strings.IndexByte(base, '_')
		if separator <= 0 {
			return nil, fmt.Errorf("invalid embedded migration name %q", base)
		}
		version, err := strconv.Atoi(base[:separator])
		if err != nil || version != index+1 {
			return nil, fmt.Errorf("embedded migration %q has version %q, want %d", base, base[:separator], index+1)
		}
		contents, err := fs.ReadFile(migrationFiles, name)
		if err != nil {
			return nil, fmt.Errorf("read embedded migration %q: %w", base, err)
		}
		migrations = append(migrations, migration{version: version, name: base, sql: string(contents)})
	}
	if len(migrations) != CurrentSchemaVersion {
		return nil, fmt.Errorf("embedded migration count is %d, want schema version %d", len(migrations), CurrentSchemaVersion)
	}
	return migrations, nil
}

// runMigrations applies all pending migrations in one transaction. Keeping
// the version marker in that transaction means a failed migration cannot
// leave either a partially-created schema or a falsely advanced version.
func runMigrations(ctx context.Context, database *sql.DB, migrations []migration) error {
	for index, migration := range migrations {
		want := index + 1
		if migration.version != want {
			return fmt.Errorf("migration sequence has version %d at position %d, want %d", migration.version, index, want)
		}
	}
	// BEGIN IMMEDIATE obtains SQLite's write reservation before reading the
	// version. A deferred transaction would leave a race between two fresh
	// opens, both of which could read version zero and then attempt CREATE TABLE.
	connection, err := database.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open schema migration connection: %w", err)
	}
	defer connection.Close()
	if _, err := connection.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin schema migration: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = connection.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var current int
	if err := connection.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if current < 0 {
		return fmt.Errorf("invalid schema version %d", current)
	}
	if current > CurrentSchemaVersion {
		return fmt.Errorf("database schema version %d is newer than binary version %d", current, CurrentSchemaVersion)
	}
	if current > len(migrations) {
		return fmt.Errorf("database schema version %d has no embedded migration", current)
	}
	for _, migration := range migrations[current:] {
		if _, err := connection.ExecContext(ctx, migration.sql); err != nil {
			return fmt.Errorf("apply migration %s: %w", migration.name, err)
		}
		if _, err := connection.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", migration.version)); err != nil {
			return fmt.Errorf("record schema version %d: %w", migration.version, err)
		}
	}
	if _, err := connection.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit schema migrations: %w", err)
	}
	committed = true
	return nil
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
