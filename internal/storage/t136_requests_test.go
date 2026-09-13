package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestT136RequestsSchemaAndRoundTrip(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	assertSchemaVersion(t, database.DB, CurrentSchemaVersion)
	wantColumns := []struct {
		name   string
		notnil int
	}{
		{"request_id", 1}, {"api_key_id", 0}, {"key_name", 0}, {"method", 0}, {"path", 0},
		{"route", 0}, {"model", 0}, {"requested_mode", 0}, {"upstream_mode", 0}, {"delivered_mode", 0},
		{"downstream_status", 0}, {"upstream_status", 0}, {"terminal_outcome", 0}, {"upstream_started", 1},
		{"error_code", 0}, {"client_bytes", 0}, {"upstream_bytes", 0}, {"delivered_bytes", 0},
		{"input_tokens", 0}, {"output_tokens", 0}, {"total_tokens", 0}, {"cached_input_tokens", 0},
		{"reasoning_output_tokens", 0}, {"cost_micros", 0}, {"started_at", 0}, {"upstream_started_at", 0},
		{"upstream_headers_at", 0}, {"first_byte_at", 0}, {"finished_at", 0}, {"total_micros", 0},
		{"time_to_upstream_headers_micros", 0}, {"time_to_first_byte_micros", 0}, {"stream_close_delay_micros", 0},
	}
	for _, column := range wantColumns {
		var got, notnull int
		if err := database.QueryRow(`SELECT cid, "notnull" FROM pragma_table_info('requests') WHERE name = ?`, column.name).Scan(&got, &notnull); err != nil {
			t.Fatalf("inspect %s: %v", column.name, err)
		}
		if notnull != column.notnil {
			t.Errorf("column %s notnull = %d, want %d", column.name, notnull, column.notnil)
		}
	}
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM pragma_table_info('requests')`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != len(wantColumns) {
		t.Fatalf("requests column count = %d, want %d", count, len(wantColumns))
	}
	for _, index := range []string{"idx_requests_recent", "idx_requests_key_time"} {
		if err := database.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'index' AND name = ?`, index).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("index %s missing", index)
		}
	}
	assertIndexColumns(t, database.DB, "idx_requests_recent", []string{"started_at", "request_id"})
	assertIndexColumns(t, database.DB, "idx_requests_key_time", []string{"api_key_id", "started_at", "request_id"})
	var tableSQL string
	if err := database.QueryRow(`SELECT sql FROM sqlite_schema WHERE type = 'table' AND name = 'requests'`).Scan(&tableSQL); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"blob", "raw_key", "authorization", "digest", "pepper", "policy_json", "pricing", "reservation", "error_text"} {
		if strings.Contains(strings.ToLower(tableSQL), forbidden) {
			t.Errorf("forbidden request-history schema term %q found in %q", forbidden, tableSQL)
		}
	}
	var onDelete string
	if err := database.QueryRow(`SELECT on_delete FROM pragma_foreign_key_list('requests') WHERE "from" = 'api_key_id'`).Scan(&onDelete); err != nil {
		t.Fatal(err)
	}
	if onDelete != "SET NULL" {
		t.Fatalf("requests api key delete action = %q, want SET NULL", onDelete)
	}

	if _, err := database.Exec(`INSERT INTO api_keys (id,name,prefix,key_hash,enabled,created_at,updated_at,policy_json) VALUES ('history-key','history','history-prefix',zeroblob(32),1,1,1,'{}')`); err != nil {
		t.Fatal(err)
	}
	const insert = `INSERT INTO requests (
		request_id,api_key_id,key_name,method,path,route,model,requested_mode,upstream_mode,delivered_mode,
		downstream_status,upstream_status,terminal_outcome,upstream_started,error_code,client_bytes,upstream_bytes,
		delivered_bytes,input_tokens,output_tokens,total_tokens,cached_input_tokens,reasoning_output_tokens,cost_micros,
		started_at,upstream_started_at,upstream_headers_at,first_byte_at,finished_at,total_micros,
		time_to_upstream_headers_micros,time_to_first_byte_micros,stream_close_delay_micros
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	knownZero := []any{"0123456789abcdef0123456789abcdef", "history-key", "history", "POST", "/v1/chat/completions", "chat_completions", "model", "json", "sse", "json", 0, 0, "complete", 1, "cancelled", 0, 0, 0, 0, 0, 0, 0, 0, 0, 100, 200, 300, 400, 500, 0, 0, 0, 0}
	if _, err := database.Exec(insert, knownZero...); err != nil {
		t.Fatalf("insert known-zero history: %v", err)
	}
	unknown := []any{"abcdef0123456789abcdef0123456789", nil, nil, "GET", "/health", nil, nil, nil, nil, nil, nil, nil, nil, 0, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil}
	if _, err := database.Exec(insert, unknown...); err != nil {
		t.Fatalf("insert unknown history: %v", err)
	}
	var status, cost, total, duration int64
	if err := database.QueryRow(`SELECT downstream_status,cost_micros,total_tokens,total_micros FROM requests WHERE request_id = '0123456789abcdef0123456789abcdef'`).Scan(&status, &cost, &total, &duration); err != nil {
		t.Fatal(err)
	}
	if status != 0 || cost != 0 || total != 0 || duration != 0 {
		t.Fatalf("known zero values = %d,%d,%d,%d", status, cost, total, duration)
	}
	var unknownStatus, unknownCost, unknownTotal, unknownDuration sql.NullInt64
	if err := database.QueryRow(`SELECT downstream_status,cost_micros,total_tokens,total_micros FROM requests WHERE request_id = 'abcdef0123456789abcdef0123456789'`).Scan(&unknownStatus, &unknownCost, &unknownTotal, &unknownDuration); err != nil {
		t.Fatal(err)
	}
	if unknownStatus.Valid || unknownCost.Valid || unknownTotal.Valid || unknownDuration.Valid {
		t.Fatalf("unknown values were not NULL: %#v %#v %#v %#v", unknownStatus, unknownCost, unknownTotal, unknownDuration)
	}
	if _, err := database.Exec(`DELETE FROM api_keys WHERE id = 'history-key'`); err != nil {
		t.Fatal(err)
	}
	var keyID sql.NullString
	if err := database.QueryRow(`SELECT api_key_id FROM requests WHERE request_id = '0123456789abcdef0123456789abcdef'`).Scan(&keyID); err != nil {
		t.Fatal(err)
	}
	if keyID.Valid {
		t.Fatalf("deleted key ID = %q, want NULL", keyID.String)
	}
}

func TestT136MigrationUpgradesFromVersionSix(t *testing.T) {
	database, err := sql.Open("sqlite", dataSource(":memory:", true))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Ping(); err != nil {
		t.Fatal(err)
	}
	migrations := mustEmbeddedMigrations(t)
	if err := runMigrations(context.Background(), database, migrations[:6]); err != nil {
		t.Fatalf("create version six schema: %v", err)
	}
	assertSchemaVersion(t, database, 6)
	if err := runMigrations(context.Background(), database, migrations[:7]); err != nil {
		t.Fatalf("apply migration 007: %v", err)
	}
	assertSchemaVersion(t, database, 7)
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'requests'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("requests table count = %d, want 1", count)
	}
	if err := runMigrations(context.Background(), database, migrations); err != nil {
		t.Fatalf("apply migration 008: %v", err)
	}
	assertSchemaVersion(t, database, CurrentSchemaVersion)
}

func TestT136MigrationRollsBackOnIndexConflict(t *testing.T) {
	database, err := sql.Open("sqlite", dataSource(":memory:", true))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Ping(); err != nil {
		t.Fatal(err)
	}
	migrations := mustEmbeddedMigrations(t)
	if err := runMigrations(context.Background(), database, migrations[:6]); err != nil {
		t.Fatalf("create version six schema: %v", err)
	}
	if _, err := database.Exec(`CREATE TABLE requests_index_conflict (id INTEGER); CREATE INDEX idx_requests_recent ON requests_index_conflict(id)`); err != nil {
		t.Fatal(err)
	}
	if err := runMigrations(context.Background(), database, migrations); err == nil {
		t.Fatal("migration 007 with conflicting index unexpectedly succeeded")
	}
	assertSchemaVersion(t, database, 6)
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'requests'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("failed migration 007 left requests table behind")
	}
}

func TestT136RequestsRejectInvalidValues(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO api_keys (id,name,prefix,key_hash,enabled,created_at,updated_at,policy_json) VALUES ('k','name','prefix',zeroblob(32),1,1,1,'{}')`); err != nil {
		t.Fatal(err)
	}
	const base = `INSERT INTO requests (request_id,api_key_id,method,path,model,upstream_started) VALUES ('0123456789abcdef0123456789abcdef','k','GET','/health',NULL,0)`
	if _, err := database.Exec(base); err != nil {
		t.Fatal(err)
	}
	invalid := []struct {
		name  string
		query string
	}{
		{"duplicate ID", base},
		{"orphan key", `INSERT INTO requests (request_id,api_key_id,method,path,model,upstream_started) VALUES ('abcdef0123456789abcdef0123456789','missing','GET','/health','',0)`},
		{"invalid route", `UPDATE requests SET route='secret' WHERE request_id='0123456789abcdef0123456789abcdef'`},
		{"invalid requested mode", `UPDATE requests SET requested_mode='opaque' WHERE request_id='0123456789abcdef0123456789abcdef'`},
		{"invalid upstream mode", `UPDATE requests SET upstream_mode='other' WHERE request_id='0123456789abcdef0123456789abcdef'`},
		{"invalid delivered mode", `UPDATE requests SET delivered_mode='other' WHERE request_id='0123456789abcdef0123456789abcdef'`},
		{"invalid terminal outcome", `UPDATE requests SET terminal_outcome='arbitrary' WHERE request_id='0123456789abcdef0123456789abcdef'`},
		{"invalid error code", `UPDATE requests SET error_code='arbitrary' WHERE request_id='0123456789abcdef0123456789abcdef'`},
		{"negative count", `UPDATE requests SET input_tokens=-1 WHERE request_id='0123456789abcdef0123456789abcdef'`},
		{"count overflow", `UPDATE requests SET output_tokens=9223372036854775808 WHERE request_id='0123456789abcdef0123456789abcdef'`},
		{"negative duration", `UPDATE requests SET total_micros=-1 WHERE request_id='0123456789abcdef0123456789abcdef'`},
		{"fractional status", `UPDATE requests SET downstream_status=200.5 WHERE request_id='0123456789abcdef0123456789abcdef'`},
		{"finish before start", `UPDATE requests SET started_at=2,finished_at=1 WHERE request_id='0123456789abcdef0123456789abcdef'`},
		{"SSE delivery without SSE upstream", `UPDATE requests SET delivered_mode='sse' WHERE request_id='0123456789abcdef0123456789abcdef'`},
		{"opaque upstream transformed", `UPDATE requests SET upstream_mode='opaque',delivered_mode='json' WHERE request_id='0123456789abcdef0123456789abcdef'`},
		{"requested and delivered conflict", `UPDATE requests SET requested_mode='sse',delivered_mode='json' WHERE request_id='0123456789abcdef0123456789abcdef'`},
		{"oversized method", fmt.Sprintf(`UPDATE requests SET method='%s' WHERE request_id='0123456789abcdef0123456789abcdef'`, strings.Repeat("m", 33))},
		{"oversized path", fmt.Sprintf(`UPDATE requests SET path='%s' WHERE request_id='0123456789abcdef0123456789abcdef'`, strings.Repeat("p", 2049))},
		{"oversized model", fmt.Sprintf(`UPDATE requests SET model='%s' WHERE request_id='0123456789abcdef0123456789abcdef'`, strings.Repeat("x", 513))},
		{"oversized key name", fmt.Sprintf(`UPDATE requests SET key_name='%s' WHERE request_id='0123456789abcdef0123456789abcdef'`, strings.Repeat("k", 257))},
		{"oversized key ID", fmt.Sprintf(`UPDATE requests SET api_key_id='%s' WHERE request_id='0123456789abcdef0123456789abcdef'`, strings.Repeat("k", 257))},
		{"pre-upstream started", `UPDATE requests SET terminal_outcome='pre_upstream',upstream_started=1 WHERE request_id='0123456789abcdef0123456789abcdef'`},
		{"post-upstream not started", `UPDATE requests SET terminal_outcome='complete',upstream_started=0 WHERE request_id='0123456789abcdef0123456789abcdef'`},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			if _, err := database.Exec(test.query); err == nil {
				t.Fatalf("invalid value unexpectedly accepted: %s", test.name)
			}
		})
	}
}
