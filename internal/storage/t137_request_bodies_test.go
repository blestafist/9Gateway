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

	assertSchemaVersion(t, database.DB, CurrentSchemaVersion)
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
	assertSchemaVersion(t, reopened.DB, CurrentSchemaVersion)
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

func TestT137Migration009PreservesVersionEightRows(t *testing.T) {
	database, err := sql.Open("sqlite", dataSource(":memory:", true))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Ping(); err != nil {
		t.Fatal(err)
	}
	var fkEnabled int
	if err := database.QueryRow(`PRAGMA foreign_keys`).Scan(&fkEnabled); err != nil {
		t.Fatal(err)
	}
	if fkEnabled != 1 {
		t.Fatalf("foreign_keys = %d, want 1", fkEnabled)
	}
	migrations := mustEmbeddedMigrations(t)
	if err := runMigrations(context.Background(), database, migrations[:8]); err != nil {
		t.Fatalf("create version eight schema: %v", err)
	}
	if _, err := database.Exec(`INSERT INTO requests (request_id, upstream_started, started_at, upstream_started_at, finished_at) VALUES (?, 1, 10, 11, 12)`, t137RequestID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO request_bodies (request_id, body_kind, body, original_size, truncated) VALUES (?, 'response', ?, 3, 0)`, t137RequestID, []byte("abc")); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(context.Background(), database, migrations); err != nil {
		t.Fatalf("apply migration 009: %v", err)
	}
	assertSchemaVersion(t, database, CurrentSchemaVersion)
	var fkErrors int
	if err := database.QueryRow(`SELECT count(*) FROM pragma_foreign_key_check()`).Scan(&fkErrors); err != nil {
		t.Fatal(err)
	}
	if fkErrors != 0 {
		t.Fatalf("foreign_key_check returned %d errors after upgrade", fkErrors)
	}
	var fkTable, onDelete string
	if err := database.QueryRow(`SELECT "table", on_delete FROM pragma_foreign_key_list('request_bodies') WHERE "from" = 'request_id'`).Scan(&fkTable, &onDelete); err != nil {
		t.Fatal(err)
	}
	if fkTable != "requests" || onDelete != "CASCADE" {
		t.Fatalf("request_bodies FK = table %q, on_delete %q; want requests, CASCADE", fkTable, onDelete)
	}
	var apiKeyFKOnDelete string
	if err := database.QueryRow(`SELECT on_delete FROM pragma_foreign_key_list('requests') WHERE "from" = 'api_key_id'`).Scan(&apiKeyFKOnDelete); err != nil {
		t.Fatal(err)
	}
	if apiKeyFKOnDelete != "SET NULL" {
		t.Fatalf("requests.api_key_id FK on_delete = %q, want SET NULL", apiKeyFKOnDelete)
	}
	var started, upstreamStarted, finished int64
	if err := database.QueryRow(`SELECT started_at, upstream_started_at, finished_at FROM requests WHERE request_id = ?`, t137RequestID).Scan(&started, &upstreamStarted, &finished); err != nil {
		t.Fatal(err)
	}
	if started != 10 || upstreamStarted != 11 || finished != 12 {
		t.Fatalf("preserved lifecycle = %d, %d, %d", started, upstreamStarted, finished)
	}
	var body []byte
	if err := database.QueryRow(`SELECT body FROM request_bodies WHERE request_id = ? AND body_kind = 'response'`, t137RequestID).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if string(body) != "abc" {
		t.Fatalf("preserved body = %q", body)
	}
	if _, err := database.Exec(`DELETE FROM requests WHERE request_id = ?`, t137RequestID); err != nil {
		t.Fatal(err)
	}
	var bodyCount int
	if err := database.QueryRow(`SELECT count(*) FROM request_bodies WHERE request_id = ?`, t137RequestID).Scan(&bodyCount); err != nil {
		t.Fatal(err)
	}
	if bodyCount != 0 {
		t.Fatalf("cascade after upgrade left %d body rows, want 0", bodyCount)
	}
}

func TestT137Migration009RollsBackWithoutChangingVersionEightSchema(t *testing.T) {
	database, err := sql.Open("sqlite", dataSource(":memory:", true))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Ping(); err != nil {
		t.Fatal(err)
	}
	migrations := mustEmbeddedMigrations(t)
	if err := runMigrations(context.Background(), database, migrations[:8]); err != nil {
		t.Fatalf("create version eight schema: %v", err)
	}
	const rollbackRequestID = "abcdef0123456789abcdef0123456789"
	if _, err := database.Exec(`INSERT INTO requests (request_id, upstream_started, api_key_id, key_name, method, path, route, model, downstream_status, upstream_status, client_bytes, delivered_bytes, input_tokens, output_tokens, total_tokens, cost_micros, started_at, finished_at) VALUES (?, 0, NULL, 'test-key', 'POST', '/v1/chat/completions', 'chat_completions', 'test-model', 200, 200, 0, 0, 10, 20, 30, 0, 1000, 2000)`, rollbackRequestID); err != nil {
		t.Fatal(err)
	}
	binaryBody := []byte{0x00, 0xff, 0xc3, 0x28, 0x00}
	if _, err := database.Exec(`INSERT INTO request_bodies (request_id, body_kind, body, original_size, truncated) VALUES (?, 'client_request', ?, 5, 0)`, rollbackRequestID, binaryBody); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE index_conflict (id INTEGER); CREATE INDEX idx_requests_finished ON index_conflict(id)`); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(context.Background(), database, migrations); err == nil {
		t.Fatal("migration 009 unexpectedly succeeded with index conflict")
	}
	assertSchemaVersion(t, database, 8)
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'requests'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("requests table count after rollback = %d, want 1", count)
	}
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'request_bodies'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("request_bodies table count after rollback = %d, want 1", count)
	}
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'index' AND name = 'idx_requests_recent'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("idx_requests_recent after rollback = %d, want 1", count)
	}
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'index' AND name = 'idx_requests_finished'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("idx_requests_finished after rollback = %d, want conflicting sentinel index", count)
	}
	var upstreamStarted int
	var apiKeyID sql.NullString
	var keyName, method, path, route, model string
	var downstreamStatus, upstreamStatus, clientBytes, deliveredBytes, inputTokens, outputTokens, totalTokens, costMicros, startedAt, finishedAt int64
	if err := database.QueryRow(`SELECT upstream_started, api_key_id, key_name, method, path, route, model, downstream_status, upstream_status, client_bytes, delivered_bytes, input_tokens, output_tokens, total_tokens, cost_micros, started_at, finished_at FROM requests WHERE request_id = ?`, rollbackRequestID).Scan(&upstreamStarted, &apiKeyID, &keyName, &method, &path, &route, &model, &downstreamStatus, &upstreamStatus, &clientBytes, &deliveredBytes, &inputTokens, &outputTokens, &totalTokens, &costMicros, &startedAt, &finishedAt); err != nil {
		t.Fatal(err)
	}
	if upstreamStarted != 0 || apiKeyID.Valid || keyName != "test-key" || method != "POST" || path != "/v1/chat/completions" || route != "chat_completions" || model != "test-model" || downstreamStatus != 200 || upstreamStatus != 200 || clientBytes != 0 || deliveredBytes != 0 || inputTokens != 10 || outputTokens != 20 || totalTokens != 30 || costMicros != 0 || startedAt != 1000 || finishedAt != 2000 {
		t.Fatalf("rolled-back request values changed: upstream_started=%d, api_key_id=%v, key_name=%s, method=%s, path=%s, route=%s, model=%s, downstream_status=%d, upstream_status=%d, client_bytes=%d, delivered_bytes=%d, input_tokens=%d, output_tokens=%d, total_tokens=%d, cost_micros=%d, started_at=%d, finished_at=%d", upstreamStarted, apiKeyID, keyName, method, path, route, model, downstreamStatus, upstreamStatus, clientBytes, deliveredBytes, inputTokens, outputTokens, totalTokens, costMicros, startedAt, finishedAt)
	}
	var bodyKind string
	var body []byte
	var originalSize int64
	var truncated int
	if err := database.QueryRow(`SELECT body_kind, body, original_size, truncated FROM request_bodies WHERE request_id = ?`, rollbackRequestID).Scan(&bodyKind, &body, &originalSize, &truncated); err != nil {
		t.Fatal(err)
	}
	if bodyKind != "client_request" || !bytes.Equal(body, binaryBody) || originalSize != 5 || truncated != 0 {
		t.Fatalf("rolled-back body changed: kind=%s, body=%v, original_size=%d, truncated=%d", bodyKind, body, originalSize, truncated)
	}
	var fkErrors int
	if err := database.QueryRow(`SELECT count(*) FROM pragma_foreign_key_check()`).Scan(&fkErrors); err != nil {
		t.Fatal(err)
	}
	if fkErrors != 0 {
		t.Fatalf("foreign_key_check returned %d errors after rollback", fkErrors)
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

func TestT137RequestBodiesSQLLiteralMatchesSafetyMaximum(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var tableSQL string
	if err := database.QueryRow(`SELECT sql FROM sqlite_schema WHERE type = 'table' AND name = 'request_bodies'`).Scan(&tableSQL); err != nil {
		t.Fatal(err)
	}
	expectedLiteral := fmt.Sprintf("length(body) <= %d", RequestBodySchemaSafetyMaxBytes)
	if !strings.Contains(tableSQL, expectedLiteral) {
		t.Fatalf("request_bodies schema SQL does not contain %q:\n%s", expectedLiteral, tableSQL)
	}
}
