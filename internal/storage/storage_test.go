package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	var count int
	if err := reopened.QueryRowContext(ctx, "SELECT count(*) FROM child").Scan(&count); err != nil {
		t.Fatalf("query after reopen: %v", err)
	}
	if count != 0 {
		t.Fatalf("reopened child row count = %d, want 0", count)
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
