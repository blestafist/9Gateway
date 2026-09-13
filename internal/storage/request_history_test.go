package storage

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/observability"
)

func historyTestRecord(id string, finished int64) HistoryRecord {
	return HistoryRecord{
		RequestID: id, Method: "POST", Path: "/v1/chat/completions", Route: "chat_completions", Model: "model",
		RequestedMode: "json", UpstreamMode: "json", DeliveredMode: "json", TerminalOutcome: "complete", UpstreamStarted: true,
		DownstreamStatus: KnownInt64(200), InputTokens: KnownInt64(0), OutputTokens: KnownInt64(3), TotalTokens: KnownInt64(3),
		CostMicros: KnownInt64(0), StartedAt: KnownInt64(finished - 1), FinishedAt: KnownInt64(finished), TotalMicros: KnownInt64(0),
	}
}

func TestRequestHistoryRepositoryPersistsAtomicallyAndCopiesBodies(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository := NewRequestHistoryRepository(database)
	body := observability.BodySnapshot{Kind: observability.BodyKindResponse, Bytes: []byte{0, 0xff}, OriginalSize: 2, Captured: true}
	if err := repository.Persist(context.Background(), historyTestRecord("0123456789abcdef0123456789abcdef", 10), []observability.BodySnapshot{body}); err != nil {
		t.Fatal(err)
	}
	body.Bytes[0] = 9
	var stored []byte
	if err := database.QueryRow(`SELECT body FROM request_bodies WHERE request_id = ?`, "0123456789abcdef0123456789abcdef").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, []byte{0, 0xff}) {
		t.Fatalf("stored body = %v", stored)
	}
	if err := repository.Persist(context.Background(), historyTestRecord("0123456789abcdef0123456789abcdef", 10), nil); !errors.Is(err, ErrHistoryDuplicate) {
		t.Fatalf("duplicate error = %v", err)
	}
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM requests`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("request count after duplicate = %d", count)
	}
}

func TestRequestHistoryRepositoryRollbackAndRetention(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TRIGGER fail_history_body BEFORE INSERT ON request_bodies BEGIN SELECT RAISE(ABORT, 'secret trigger detail'); END`); err != nil {
		t.Fatal(err)
	}
	repository := NewRequestHistoryRepository(database)
	record := historyTestRecord("abcdef0123456789abcdef0123456789", 10)
	err = repository.Persist(context.Background(), record, []observability.BodySnapshot{{Kind: observability.BodyKindResponse, Captured: true}})
	if !errors.Is(err, ErrHistoryWrite) || errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rollback error = %v", err)
	}
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM requests`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rolled back request count = %d", count)
	}
	if _, err := database.Exec(`DROP TRIGGER fail_history_body`); err != nil {
		t.Fatal(err)
	}
	for index, finished := range []int64{10, 20, 30} {
		id := []string{"11111111111111111111111111111111", "22222222222222222222222222222222", "33333333333333333333333333333333"}[index]
		if err := repository.Persist(context.Background(), historyTestRecord(id, finished), []observability.BodySnapshot{{Kind: observability.BodyKindResponse, Bytes: []byte{'x'}, OriginalSize: 1, Captured: true}}); err != nil {
			t.Fatal(err)
		}
	}
	cutoff := time.UnixMicro(20).UTC()
	deleted, err := repository.DeleteBodiesBefore(context.Background(), cutoff, 100)
	if err != nil || deleted != 1 {
		t.Fatalf("body retention = %d, %v", deleted, err)
	}
	deleted, err = repository.DeleteMetadataBefore(context.Background(), cutoff, 100)
	if err != nil || deleted != 1 {
		t.Fatalf("metadata retention = %d, %v", deleted, err)
	}
	if err := database.QueryRow(`SELECT count(*) FROM request_bodies`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("remaining bodies = %d, want boundary and newer rows", count)
	}
}

func TestRequestHistoryRepositoryRejectsCancellationBeforeSQL(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = NewRequestHistoryRepository(database).Persist(ctx, historyTestRecord("0123456789abcdef0123456789abcdef", 10), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestRequestHistoryRetentionPlansUseCompletionIndex(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	plans := []string{
		`EXPLAIN QUERY PLAN DELETE FROM request_bodies
			WHERE rowid IN (
				SELECT request_bodies.rowid
				FROM requests
				JOIN request_bodies ON requests.request_id = request_bodies.request_id
				WHERE requests.finished_at IS NOT NULL AND requests.finished_at < ?
				ORDER BY requests.finished_at ASC, request_bodies.request_id ASC, request_bodies.body_kind ASC
				LIMIT ?
			)`,
		`EXPLAIN QUERY PLAN DELETE FROM requests
			WHERE request_id IN (
				SELECT request_id FROM requests
				WHERE finished_at IS NOT NULL AND finished_at < ?
				ORDER BY finished_at ASC, request_id ASC
				LIMIT ?
			)`,
	}
	for index, query := range plans {
		t.Run(fmt.Sprintf("retention-%d", index), func(t *testing.T) {
			rows, err := database.Query(query, 0, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var details []string
			for rows.Next() {
				var id, parent, notUsed int
				var detail string
				if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
					t.Fatal(err)
				}
				details = append(details, detail)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			joined := strings.Join(details, " | ")
			if !strings.Contains(joined, "idx_requests_finished") {
				t.Fatalf("retention plan does not use completion index: %s", joined)
			}
			if strings.Contains(joined, "SCAN requests") {
				t.Fatalf("retention plan scans requests: %s", joined)
			}
		})
	}
}
