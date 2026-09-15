package storage

import (
	"context"
	"errors"
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
	// A row inserted after the first page with the same completion timestamp
	// must not displace an item from the initial traversal snapshot.
	if _, err := database.Exec(`INSERT INTO requests (request_id,terminal_outcome,upstream_started,finished_at) VALUES ('00000000000000000000000000000025','complete',1,20)`); err != nil {
		t.Fatal(err)
	}
	third, _, err := repository.ListRequests(context.Background(), ListRequestsFilter{}, 2, cursor)
	if err != nil || len(third) != 1 || third[0].FinishedAt.Value != 10 {
		t.Fatalf("same-timestamp insertion changed snapshot = %#v, error %v", third, err)
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

func TestT143RequestCursorBindsFiltersAndExpiresAfterBookmarkDeletion(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for index, keyID := range []string{"key-a", "key-b"} {
		hash := make([]byte, 32)
		hash[0] = byte(index + 1)
		if _, err := database.Exec(`INSERT INTO api_keys (id,name,prefix,key_hash,enabled,created_at,updated_at,policy_json) VALUES (?, ?, ?, ?, 1, 1, 1, '{}')`, keyID, keyID, "prefix-"+keyID, hash); err != nil {
			t.Fatal(err)
		}
	}
	repository := NewRequestHistoryRepository(database)
	for index, finished := range []int64{30, 20, 10} {
		id := "0000000000000000000000000000000" + string(rune('1'+index))
		if err := repository.Persist(context.Background(), HistoryRecord{RequestID: id, APIKeyID: "key-a", KeyName: "key-a", Method: "GET", Path: "/v1/models", Route: "models", TerminalOutcome: "complete", UpstreamStarted: true, FinishedAt: KnownInt64(finished), StartedAt: KnownInt64(finished - 1)}, nil); err != nil {
			t.Fatal(err)
		}
	}
	base := time.UnixMicro(15).UTC()
	first, cursor, err := repository.ListRequests(context.Background(), ListRequestsFilter{KeyID: "key-a", After: &base}, 1, "")
	if err != nil || len(first) != 1 || cursor == "" {
		t.Fatalf("first filtered page = %#v, cursor %q, error %v", first, cursor, err)
	}
	equivalent := time.Unix(0, 15*1000).In(time.FixedZone("offset", 2*60*60))
	second, _, err := repository.ListRequests(context.Background(), ListRequestsFilter{KeyID: "key-a", After: &equivalent}, 1, cursor)
	if err != nil || len(second) != 1 || second[0].FinishedAt.Value != 20 {
		t.Fatalf("equivalent normalized continuation = %#v, error %v", second, err)
	}
	for name, filter := range map[string]ListRequestsFilter{
		"key":    {KeyID: "key-b", After: &base},
		"after":  {KeyID: "key-a", After: func() *time.Time { value := time.UnixMicro(16); return &value }()},
		"before": {KeyID: "key-a", After: &base, Before: func() *time.Time { value := time.UnixMicro(25); return &value }()},
	} {
		if _, _, err := repository.ListRequests(context.Background(), filter, 1, cursor); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("%s filter mismatch error = %v, want ErrInvalidCursor", name, err)
		}
	}
	if _, err := database.Exec(`DELETE FROM requests WHERE request_id = ?`, first[0].RequestID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.ListRequests(context.Background(), ListRequestsFilter{KeyID: "key-a", After: &base}, 1, cursor); !errors.Is(err, ErrCursorExpired) {
		t.Fatalf("deleted bookmark error = %v, want ErrCursorExpired", err)
	}
}
