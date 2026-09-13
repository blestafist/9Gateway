package storage

import (
	"context"
	"testing"
)

func TestT144GetRequestByIDReturnsMetadataAndBodyKindsWithoutBodyBytes(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	id := "0123456789abcdef0123456789abcdef"
	if _, err := database.Exec(`INSERT INTO requests (request_id, api_key_id, key_name, method, path, route, model, terminal_outcome, upstream_started, input_tokens, cost_micros) VALUES (?, NULL, NULL, ?, ?, ?, ?, ?, ?, ?, ?)`, id, "POST", "/v1/chat/completions", "chat_completions", "model", "complete", 1, 0, 0); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"response", "client_request", "upstream_request"} {
		if _, err := database.Exec(`INSERT INTO request_bodies (request_id, body_kind, body, original_size, truncated) VALUES (?, ?, ?, 6, 0)`, id, kind, []byte("secret")); err != nil {
			t.Fatal(err)
		}
	}
	record, err := NewRequestHistoryRepository(database).GetRequestByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if record.RequestID != id || record.APIKeyID != "" || record.KeyName != "" || !record.InputTokens.Known || record.InputTokens.Value != 0 || len(record.HasBodies) != 3 || record.HasBodies[0] != "client_request" || record.HasBodies[1] != "upstream_request" || record.HasBodies[2] != "response" {
		t.Fatalf("detail = %#v", record)
	}
	if _, err := database.Exec(`DELETE FROM request_bodies WHERE request_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	record, err = NewRequestHistoryRepository(database).GetRequestByID(context.Background(), id)
	if err != nil || record == nil || record.HasBodies == nil || len(record.HasBodies) != 0 {
		t.Fatalf("after body deletion = %#v, error %v", record, err)
	}
}

func TestT144GetRequestByIDValidatesAndReportsMissing(t *testing.T) {
	repository := NewRequestHistoryRepository(nil)
	if _, err := repository.GetRequestByID(context.Background(), "bad"); err != ErrHistoryInvalidRecord {
		t.Fatalf("malformed error = %v", err)
	}
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := NewRequestHistoryRepository(database).GetRequestByID(context.Background(), "0123456789abcdef0123456789abcdef"); err != ErrNotFound {
		t.Fatalf("missing error = %v", err)
	}
}
