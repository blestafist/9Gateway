package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOpenFileDatabaseConfiguresSQLiteAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	ctx := context.Background()

	database, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := database.PingContext(ctx); err != nil {
		t.Fatalf("PingContext() error = %v", err)
	}
	assertPragma(t, database.DB, "journal_mode", "wal")
	assertPragma(t, database.DB, "foreign_keys", "1")
	assertPragma(t, database.DB, "busy_timeout", "5000")
	if _, err := database.ExecContext(ctx, "CREATE TABLE parent (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("create parent: %v", err)
	}
	if _, err := database.ExecContext(ctx, "CREATE TABLE child (parent_id INTEGER REFERENCES parent(id))"); err != nil {
		t.Fatalf("create child: %v", err)
	}
	if _, err := database.ExecContext(ctx, "INSERT INTO child(parent_id) VALUES (99)"); err == nil {
		t.Fatal("foreign key violation unexpectedly succeeded")
	}
	if err := database.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if _, err := database.ExecContext(ctx, "SELECT 1"); err == nil {
		t.Fatal("query after Close() unexpectedly succeeded")
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer reopened.Close()
	assertPragma(t, reopened.DB, "journal_mode", "wal")
	assertSchemaVersion(t, reopened.DB, CurrentSchemaVersion)
	var count int
	if err := reopened.QueryRowContext(ctx, "SELECT count(*) FROM child").Scan(&count); err != nil {
		t.Fatalf("query after reopen: %v", err)
	}
	if count != 0 {
		t.Fatalf("reopened child row count = %d, want 0", count)
	}
}

func TestOpenCreatesExpectedAPIKeySchema(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()

	assertSchemaVersion(t, database.DB, CurrentSchemaVersion)
	var tableSQL string
	if err := database.QueryRow(`SELECT sql FROM sqlite_schema WHERE type = 'table' AND name = 'api_keys'`).Scan(&tableSQL); err != nil {
		t.Fatalf("inspect api_keys table: %v", err)
	}
	for _, column := range []string{"id", "name", "prefix", "key_hash", "enabled", "expires_at", "created_at", "updated_at", "policy_json"} {
		var found int
		if err := database.QueryRow(`SELECT count(*) FROM pragma_table_info('api_keys') WHERE name = ?`, column).Scan(&found); err != nil {
			t.Fatalf("inspect %s column: %v", column, err)
		}
		if found != 1 {
			t.Errorf("column %q missing from api_keys", column)
		}
	}
	for _, forbidden := range []string{"raw_key", "gateway_key", "pepper", "auth_pepper", "authentication_pepper"} {
		if strings.Contains(strings.ToLower(tableSQL), forbidden) {
			t.Errorf("forbidden credential column %q found in table SQL %q", forbidden, tableSQL)
		}
	}
	for _, index := range []string{"idx_api_keys_prefix", "idx_api_keys_active"} {
		var found int
		if err := database.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'index' AND name = ?`, index).Scan(&found); err != nil {
			t.Fatalf("inspect %s index: %v", index, err)
		}
		if found != 1 {
			t.Errorf("index %q missing", index)
		}
	}
	if _, err := database.Exec(`INSERT INTO api_keys (id, name, prefix, key_hash, enabled, created_at, updated_at, policy_json) VALUES ('id-1', 'name', 'prefix', zeroblob(32), 1, 1, 1, '{}')`); err != nil {
		t.Fatalf("insert valid api key: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO api_keys (id, name, prefix, key_hash, enabled, created_at, updated_at, policy_json) VALUES ('id-2', 'name', 'prefix', zeroblob(32), 1, 1, 1, '{}')`); err == nil {
		t.Fatal("duplicate prefix unexpectedly succeeded")
	}
	if _, err := database.Exec(`INSERT INTO api_keys (id, name, prefix, key_hash, enabled, created_at, updated_at, policy_json) VALUES ('id-3', 'name', 'other',  zeroblob(31), 1, 1, 1, '{}')`); err == nil {
		t.Fatal("short key hash unexpectedly succeeded")
	}
	if _, err := database.Exec(`INSERT INTO api_keys (id, name, prefix, key_hash, enabled, created_at, updated_at, policy_json) VALUES ('id-4', 'name', 'other', zeroblob(32), 2, 1, 1, '{}')`); err == nil {
		t.Fatal("invalid enabled state unexpectedly succeeded")
	}
	if _, err := database.Exec(`INSERT INTO api_keys (id, name, prefix, key_hash, enabled, created_at, updated_at, policy_json) VALUES ('id-5', 'name', 'other', randomblob(32), 1, 1, 1, '{}')`); err != nil {
		t.Fatalf("insert second valid api key: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO api_keys (id, name, prefix, key_hash, enabled, created_at, updated_at, policy_json) VALUES ('id-6', 'name', 'third', zeroblob(32), 1, 1, 1, '{}')`); err == nil {
		t.Fatal("duplicate key hash unexpectedly succeeded")
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	ctx := context.Background()
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer reopened.Close()
	assertSchemaVersion(t, reopened.DB, CurrentSchemaVersion)
}

func TestConcurrentFileOpensMigrateAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.db")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	const openers = 8
	results := make(chan *DB, openers)
	errors := make(chan error, openers)
	for range openers {
		go func() {
			<-start
			database, err := Open(ctx, path)
			if err != nil {
				errors <- err
				return
			}
			results <- database
		}()
	}
	close(start)
	var databases []*DB
	for range openers {
		select {
		case database := <-results:
			databases = append(databases, database)
		case err := <-errors:
			t.Fatalf("concurrent Open() error = %v", err)
		case <-ctx.Done():
			t.Fatalf("concurrent Open() timed out: %v", ctx.Err())
		}
	}
	for _, database := range databases {
		assertSchemaVersion(t, database.DB, CurrentSchemaVersion)
		database.Close()
	}
	var tableCount int
	database, err := sql.Open("sqlite", dataSource(path, false))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.QueryRow("SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'api_keys'").Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 1 {
		t.Fatalf("api_keys table count = %d, want 1", tableCount)
	}
}

func TestFilePooledConnectionsRetainConnectionLocalSQLiteSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pooled-settings.db")
	database, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	if strings.Contains(dataSource(path, false), "journal_mode") {
		t.Fatalf("file DSN configures persistent journal mode: %q", dataSource(path, false))
	}
	ctx := context.Background()
	connections := make([]*sql.Conn, 0, fileMaxOpenConnections-1)
	for range fileMaxOpenConnections - 1 {
		connection, err := database.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, connection)
	}
	defer func() {
		for _, connection := range connections {
			connection.Close()
		}
	}()

	var foreignKeys, busyTimeout int
	if err := database.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 || busyTimeout != busyTimeoutMilliseconds {
		t.Fatalf("pooled connection settings = foreign_keys %d, busy_timeout %d", foreignKeys, busyTimeout)
	}
}

func TestFailedMigrationRollsBack(t *testing.T) {
	database, err := sql.Open("sqlite", dataSource(":memory:", true))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Ping(); err != nil {
		t.Fatal(err)
	}
	migrations := []migration{{version: 1, name: "001_test.sql", sql: "CREATE TABLE should_rollback (id INTEGER); INSERT INTO missing_table VALUES (1);"}}
	if err := runMigrations(context.Background(), database, migrations); err == nil {
		t.Fatal("failed migration unexpectedly succeeded")
	}
	var tables int
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'should_rollback'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatal("failed migration left its table behind")
	}
	assertSchemaVersion(t, database, 0)
}

func TestMigrationsRejectFutureSchemaVersion(t *testing.T) {
	database, err := sql.Open("sqlite", dataSource(":memory:", true))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(fmt.Sprintf("PRAGMA user_version = %d", CurrentSchemaVersion+1)); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(context.Background(), database, []migration{{version: 1, name: "001_test.sql", sql: "SELECT 1"}}); err == nil || !strings.Contains(err.Error(), "newer than binary") {
		t.Fatalf("future schema error = %v", err)
	}
}

func TestOpenMemoryUsesOnePooledConnection(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()

	stats := database.Stats()
	if stats.MaxOpenConnections != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", stats.MaxOpenConnections)
	}
	if err := database.Ping(); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if err := database.QueryRow("PRAGMA journal_mode").Scan(new(string)); err != nil {
		t.Fatalf("journal mode query: %v", err)
	}
	if _, err := database.Exec("CREATE TABLE state (value TEXT)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := database.Exec("INSERT INTO state(value) VALUES ('kept')"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var value string
	if err := database.QueryRow("SELECT value FROM state").Scan(&value); err != nil {
		t.Fatalf("select: %v", err)
	}
	if value != "kept" {
		t.Fatalf("value = %q, want kept", value)
	}
}

func TestOpenRejectsInvalidPathBeforeCreatingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "gateway.db")
	_, err := Open(context.Background(), path)
	if err == nil || !strings.Contains(err.Error(), "parent directory does not exist") {
		t.Fatalf("Open() error = %v, want missing parent error", err)
	}
}

func TestOpenStartupFailureIsClean(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory", "gateway.db")
	database, err := Open(context.Background(), path)
	if err == nil {
		if database != nil {
			database.Close()
		}
		t.Fatal("Open() unexpectedly succeeded")
	}
	if database != nil {
		t.Fatal("Open() returned a database on startup failure")
	}
}

func TestOpenClosesDatabaseWhenPingFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	database, err := Open(ctx, ":memory:")
	if err == nil {
		if database != nil {
			database.Close()
		}
		t.Fatal("Open() unexpectedly succeeded with a canceled context")
	}
	if database != nil {
		t.Fatal("Open() returned a database after ping failure")
	}
	if !strings.Contains(err.Error(), "ping") {
		t.Fatalf("Open() error = %v, want ping context", err)
	}
}

func TestOpenClosesDatabaseAfterOpenedResourceFailsStartup(t *testing.T) {
	previousConfigureAndPing := configureAndPingFunc
	t.Cleanup(func() { configureAndPingFunc = previousConfigureAndPing })

	var opened *DB
	configureAndPingFunc = func(database *DB, ctx context.Context, inMemory bool) error {
		opened = database
		if err := database.PingContext(ctx); err != nil {
			t.Fatalf("PingContext() error = %v", err)
		}
		return errors.New("forced startup failure")
	}

	database, err := Open(context.Background(), ":memory:")
	if err == nil {
		if database != nil {
			database.Close()
		}
		t.Fatal("Open() unexpectedly succeeded")
	}
	if database != nil {
		t.Fatal("Open() returned a database after startup failure")
	}
	if opened == nil {
		t.Fatal("startup seam did not receive opened database")
	}
	if err := opened.Ping(); err == nil {
		t.Fatal("opened database remained usable after startup cleanup")
	}
}

func TestOpenRejectsExistingUnwritablePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.db")
	if err := os.WriteFile(path, nil, 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), path); err == nil || !strings.Contains(err.Error(), "path is not writable") {
		t.Fatalf("Open() error = %v, want existing unwritable path error", err)
	}
}

func assertPragma(t *testing.T, database *sql.DB, pragma, want string) {
	t.Helper()
	var got string
	if err := database.QueryRow("PRAGMA " + pragma).Scan(&got); err != nil {
		t.Fatalf("PRAGMA %s: %v", pragma, err)
	}
	if !strings.EqualFold(got, want) {
		t.Fatalf("PRAGMA %s = %q, want %q", pragma, got, want)
	}
}

func assertSchemaVersion(t *testing.T, database *sql.DB, want int) {
	t.Helper()
	var got int
	if err := database.QueryRow("PRAGMA user_version").Scan(&got); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if got != want {
		t.Fatalf("schema version = %d, want %d", got, want)
	}
}
