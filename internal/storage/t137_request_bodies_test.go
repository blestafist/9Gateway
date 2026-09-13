package storage

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pestit/9gateway/internal/config"
)

const t137RequestID = "0123456789abcdef0123456789abcdef"

func TestT137RequestBodiesSchemaRoundTripAndCascade(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	assertSchemaVersion(t, database.DB, 8)
	if RequestBodySchemaSafetyMaxBytes != config.MaxMaxCapturedBodyBytes {
		t.Fatalf("body safety maximum = %d, T130 maximum = %d", RequestBodySchemaSafetyMaxBytes, config.MaxMaxCapturedBodyBytes)
	}
	wantColumns := []struct {
		name   string
		notnil int
	}{
		{"request_id", 1}, {"body_kind", 1}, {"body", 1}, {"original_size", 1}, {"truncated", 1},
	}
	for _, column := range wantColumns {
		var notnull int
		if err := database.QueryRow(`SELECT "notnull" FROM pragma_table_info('request_bodies') WHERE name = ?`, column.name).Scan(&notnull); err != nil {
			t.Fatalf("inspect %s: %v", column.name, err)
		}
		if notnull != column.notnil {
			t.Errorf("column %s notnull = %d, want %d", column.name, notnull, column.notnil)
		}
	}
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM pragma_table_info('request_bodies')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(wantColumns) {
		t.Fatalf("request_bodies column count = %d, want %d", count, len(wantColumns))
	}
	var tableSQL string
	if err := database.QueryRow(`SELECT sql FROM sqlite_schema WHERE type = 'table' AND name = 'requests'`).Scan(&tableSQL); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(tableSQL), "body") {
		t.Fatalf("requests schema contains body content column: %q", tableSQL)
	}
	var onDelete string
	if err := database.QueryRow(`SELECT on_delete FROM pragma_foreign_key_list('request_bodies') WHERE "from" = 'request_id'`).Scan(&onDelete); err != nil {
		t.Fatal(err)
	}
	if onDelete != "CASCADE" {
		t.Fatalf("request_bodies delete action = %q, want CASCADE", onDelete)
	}

	if _, err := database.Exec(`INSERT INTO requests (request_id, upstream_started) VALUES (?, 0)`, t137RequestID); err != nil {
		t.Fatal(err)
	}
	const insert = `INSERT INTO request_bodies (request_id, body_kind, body, original_size, truncated) VALUES (?, ?, ?, ?, ?)`
	cases := []struct {
		kind      string
		body      []byte
		original  int64
		truncated int
	}{
		{"client_request", []byte{}, 0, 0},
		{"upstream_request", []byte{0, 0xff, 0xc3, 0x28}, 4, 0},
		{"response", []byte("prefix"), 10, 1},
	}
	for _, test := range cases {
		if _, err := database.Exec(insert, t137RequestID, test.kind, test.body, test.original, test.truncated); err != nil {
			t.Fatalf("insert %s: %v", test.kind, err)
		}
	}
	var got []byte
	var original int64
	var truncated int
	if err := database.QueryRow(`SELECT body, original_size, truncated FROM request_bodies WHERE request_id = ? AND body_kind = 'upstream_request'`, t137RequestID).Scan(&got, &original, &truncated); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{0, 0xff, 0xc3, 0x28}) || original != 4 || truncated != 0 {
		t.Fatalf("binary body = %v, %d, %d", got, original, truncated)
	}
	if _, err := database.Exec(`DELETE FROM request_bodies WHERE request_id = ? AND body_kind = 'response'`, t137RequestID); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT count(*) FROM requests WHERE request_id = ?`, t137RequestID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("independent body deletion removed request metadata")
	}
	if err := database.QueryRow(`SELECT count(*) FROM request_bodies WHERE request_id = ?`, t137RequestID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("remaining body rows = %d, want 2", count)
	}
	if _, err := database.Exec(`DELETE FROM requests WHERE request_id = ?`, t137RequestID); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT count(*) FROM request_bodies WHERE request_id = ?`, t137RequestID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("cascade left %d body rows", count)
	}
}

func TestT137RequestBodiesRejectMalformedValuesWithoutLeakingBody(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO requests (request_id, upstream_started) VALUES (?, 0)`, t137RequestID); err != nil {
		t.Fatal(err)
	}
	const insert = `INSERT INTO request_bodies (request_id, body_kind, body, original_size, truncated) VALUES (?, ?, ?, ?, ?)`
	if _, err := database.Exec(insert, t137RequestID, "client_request", []byte{}, 0, 0); err != nil {
		t.Fatal(err)
	}
	secret := []byte("credential-body-")
	invalid := []struct {
		name      string
		body      any
		original  any
		truncated any
	}{
		{"duplicate kind", []byte{}, int64(0), 0},
		{"orphan request", []byte("orphan"), int64(6), 0},
		{"invalid kind", []byte{}, int64(0), 0},
		{"captured larger than original", []byte("ab"), int64(1), 1},
		{"short body not truncated", []byte("a"), int64(2), 0},
		{"equal body marked truncated", []byte("ab"), int64(2), 1},
		{"truncated body marked noninteger", []byte("a"), int64(2), 2},
		{"negative original", []byte{}, int64(-1), 0},
		{"fractional original", []byte{}, 1.5, 0},
		{"text body", "text", int64(4), 0},
		{"null body", nil, int64(0), 0},
		{"oversized body", bytes.Repeat([]byte{'x'}, int(RequestBodySchemaSafetyMaxBytes)+1), int64(RequestBodySchemaSafetyMaxBytes + 1), 0},
	}
	for index, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			requestID := fmt.Sprintf("%032x", index+1)
			kind := "client_request"
			body := test.body
			if test.name == "duplicate kind" {
				requestID = t137RequestID
				if _, err := database.Exec(insert, requestID, kind, body, test.original, test.truncated); err == nil {
					t.Fatal("duplicate body kind unexpectedly succeeded")
				}
				return
			}
			if test.name == "orphan request" {
				requestID = "abcdef0123456789abcdef0123456789"
			} else if test.name == "invalid kind" {
				kind = "other"
			} else if test.name == "text body" {
				body = "credential-body-"
				test.original = int64(len(body.(string)))
			} else if test.name == "null body" {
				body = nil
			}
			if test.name != "orphan request" {
				if _, err := database.Exec(`INSERT INTO requests (request_id, upstream_started) VALUES (?, 0)`, requestID); err != nil {
					t.Fatal(err)
				}
			}
			_, err := database.Exec(insert, requestID, kind, body, test.original, test.truncated)
			if err == nil {
				t.Fatal("malformed body unexpectedly succeeded")
			}
			if strings.Contains(err.Error(), string(secret)) || strings.Contains(err.Error(), "credential") {
				t.Fatalf("constraint error leaked body or credential: %v", err)
			}
		})
	}
}

func TestT137RequestBodiesUpgradeAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "request-bodies.db")
	ctx := context.Background()
	rawDatabase, err := sql.Open("sqlite", dataSource(path, false))
	if err != nil {
		t.Fatal(err)
	}
	if err := rawDatabase.PingContext(ctx); err != nil {
		rawDatabase.Close()
		t.Fatal(err)
	}
	migrations := mustEmbeddedMigrations(t)
	if err := runMigrations(ctx, rawDatabase, migrations[:7]); err != nil {
		rawDatabase.Close()
		t.Fatalf("create version seven database: %v", err)
	}
	if _, err := rawDatabase.Exec(`INSERT INTO requests (request_id, upstream_started) VALUES (?, 0)`, t137RequestID); err != nil {
		rawDatabase.Close()
		t.Fatal(err)
	}
	if err := rawDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO request_bodies (request_id, body_kind, body, original_size, truncated) VALUES (?, 'response', ?, 3, 0)`, t137RequestID, []byte{0, 0xff, 0xc3}); err != nil {
		database.Close()
		t.Fatal(err)
	}
	for _, kind := range []string{"client_request", "upstream_request"} {
		if _, err := database.Exec(`INSERT INTO request_bodies (request_id, body_kind, body, original_size, truncated) VALUES (?, ?, ?, 0, 0)`, t137RequestID, kind, []byte{}); err != nil {
			database.Close()
			t.Fatalf("insert %s after upgrade: %v", kind, err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertSchemaVersion(t, reopened.DB, 8)
	for _, kind := range []string{"client_request", "upstream_request", "response"} {
		var body []byte
		if err := reopened.QueryRow(`SELECT body FROM request_bodies WHERE request_id = ? AND body_kind = ?`, t137RequestID, kind).Scan(&body); err != nil {
			t.Fatalf("reopened %s body: %v", kind, err)
		}
		if kind == "response" && !bytes.Equal(body, []byte{0, 0xff, 0xc3}) {
			t.Fatalf("reopened response body = %v", body)
		}
		if kind != "response" && len(body) != 0 {
			t.Fatalf("reopened %s body = %v, want empty", kind, body)
		}
	}
}

func TestT137MigrationRollsBackFromVersionSeven(t *testing.T) {
	database, err := sql.Open("sqlite", dataSource(":memory:", true))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Ping(); err != nil {
		t.Fatal(err)
	}
	migrations := mustEmbeddedMigrations(t)
	if err := runMigrations(context.Background(), database, migrations[:7]); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE request_bodies (sentinel INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(context.Background(), database, migrations); err == nil {
		t.Fatal("migration 008 unexpectedly succeeded with table conflict")
	}
	assertSchemaVersion(t, database, 7)
	var columnCount int
	if err := database.QueryRow(`SELECT count(*) FROM pragma_table_info('request_bodies')`).Scan(&columnCount); err != nil {
		t.Fatal(err)
	}
	if columnCount != 1 {
		t.Fatalf("failed migration replaced sentinel table with %d columns", columnCount)
	}
}

func TestT137RequestBodiesRejectAllTruncationCombinations(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO requests (request_id, upstream_started) VALUES (?, 0)`, t137RequestID); err != nil {
		t.Fatal(err)
	}
	const insert = `INSERT INTO request_bodies (request_id, body_kind, body, original_size, truncated) VALUES (?, ?, ?, ?, ?)`
	for index, test := range []struct {
		body      []byte
		original  int64
		truncated int
	}{
		{[]byte("a"), 0, 0},
		{[]byte("a"), 0, 1},
		{[]byte{}, 1, 0},
		{[]byte("a"), 1, 1},
		{[]byte("ab"), 1, 0},
		{[]byte("ab"), 2, 1},
	} {
		t.Run(fmt.Sprintf("combination-%d", index), func(t *testing.T) {
			if _, err := database.Exec(insert, t137RequestID, "client_request", test.body, test.original, test.truncated); err == nil {
				t.Fatal("invalid size/truncation combination unexpectedly succeeded")
			}
		})
	}
}

func TestT137RequestBodiesAcceptEmptyTruncatedCapture(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO requests (request_id, upstream_started) VALUES (?, 0)`, t137RequestID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO request_bodies (request_id, body_kind, body, original_size, truncated) VALUES (?, 'response', ?, 1, 1)`, t137RequestID, []byte{}); err != nil {
		t.Fatalf("empty truncated capture rejected: %v", err)
	}
}

func TestT137RequestBodiesAcceptSchemaMaximum(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO requests (request_id, upstream_started) VALUES (?, 0)`, t137RequestID); err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte{'x'}, int(RequestBodySchemaSafetyMaxBytes))
	if _, err := database.Exec(`INSERT INTO request_bodies (request_id, body_kind, body, original_size, truncated) VALUES (?, 'client_request', ?, ?, 0)`, t137RequestID, body, RequestBodySchemaSafetyMaxBytes); err != nil {
		t.Fatalf("schema maximum rejected: %v", err)
	}
}
