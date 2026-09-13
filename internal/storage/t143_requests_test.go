package storage

import (
	"context"
	"testing"
	"time"
)

func TestT143ListRequestsUsesCompletionBookmarkAndPreservesNulls(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO api_keys (id,name,prefix,key_hash,enabled,created_at,updated_at,policy_json) VALUES ('key-a','alpha','prefix',zeroblob(32),1,1,1,'{}')`); err != nil {
		t.Fatal(err)
	}
	repository := NewRequestHistoryRepository(database)
	for index, finished := range []int64{10, 20, 30} {
		record := HistoryRecord{
			RequestID:        "0000000000000000000000000000000" + string(rune('1'+index)),
			APIKeyID:         "key-a",
			KeyName:          "alpha",
			Method:           "POST",
			Path:             "/v1/chat/completions",
			Route:            "chat_completions",
			Model:            "model",
			RequestedMode:    "json",
			UpstreamMode:     "json",
			DeliveredMode:    "json",
			TerminalOutcome:  "complete",
			UpstreamStarted:  true,
			DownstreamStatus: KnownInt64(200),
			FinishedAt:       KnownInt64(finished),
			StartedAt:        KnownInt64(finished - 1),
			TotalTokens:      KnownInt64(0),
		}
		if err := repository.Persist(context.Background(), record, nil); err != nil {
			t.Fatal(err)
		}
	}
	first, cursor, err := repository.ListRequests(context.Background(), ListRequestsFilter{}, 2, "")
	if err != nil || len(first) != 2 || cursor == "" {
		t.Fatalf("first page = %#v, cursor %q, error %v", first, cursor, err)
	}
	if first[0].FinishedAt.Value != 30 || first[1].FinishedAt.Value != 20 {
		t.Fatalf("first order = %#v", first)
	}
	if _, err := database.Exec(`INSERT INTO requests (request_id,terminal_outcome,upstream_started,finished_at) VALUES ('ffffffffffffffffffffffffffffffff','complete',1,40)`); err != nil {
		t.Fatal(err)
	}
	second, next, err := repository.ListRequests(context.Background(), ListRequestsFilter{}, 2, cursor)
	if err != nil || len(second) != 1 || next != "" || second[0].FinishedAt.Value != 10 {
		t.Fatalf("stable second page = %#v, cursor %q, error %v", second, next, err)
	}
	if _, _, err := repository.ListRequests(context.Background(), ListRequestsFilter{}, 2, cursor+"x"); err != ErrInvalidCursor {
		t.Fatalf("tampered cursor error = %v", err)
	}

	unknownID := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	if _, err := database.Exec(`INSERT INTO requests (request_id,upstream_started) VALUES (?,0)`, unknownID); err != nil {
		t.Fatal(err)
	}
	cutoff := time.UnixMicro(0)
	zero, _, err := repository.ListRequests(context.Background(), ListRequestsFilter{After: &cutoff}, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range zero {
		if record.RequestID == unknownID && record.TotalTokens.Known {
			t.Fatalf("unknown total tokens = %#v", record.TotalTokens)
		}
	}
	known, _, err := repository.ListRequests(context.Background(), ListRequestsFilter{}, 10, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range known {
		if record.RequestID == "00000000000000000000000000000001" && (!record.TotalTokens.Known || record.TotalTokens.Value != 0) {
			t.Fatalf("known zero total tokens = %#v", record.TotalTokens)
		}
	}
}
