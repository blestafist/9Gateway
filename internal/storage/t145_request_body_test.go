package storage

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestT145GetRequestBodyReturnsExactBytesAndMetadata(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	id := "0123456789abcdef0123456789abcdef"
	if _, err := database.Exec(`INSERT INTO requests (request_id, upstream_started) VALUES (?, 0)`, id); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		kind      string
		body      []byte
		original  int64
		truncated bool
	}{
		{kind: "client_request", body: []byte(`{"model":"x"}`), original: 13},
		{kind: "upstream_request", body: []byte("data: {\"x\":\"\xff\"}\n\n"), original: int64(len([]byte("data: {\"x\":\"\xff\"}\n\n")))},
		{kind: "response", body: []byte{0x1f, 0x8b, 0x00, 0x00, 0xff, 0x0a}, original: 10, truncated: true},
	}
	for _, test := range cases {
		if _, err := database.Exec(`INSERT INTO request_bodies (request_id, body_kind, body, original_size, truncated) VALUES (?, ?, ?, ?, ?)`, id, test.kind, test.body, test.original, test.truncated); err != nil {
			t.Fatalf("insert %s: %v", test.kind, err)
		}
	}
	repository := NewRequestHistoryRepository(database)
	for _, test := range cases {
		got, err := repository.GetRequestBody(context.Background(), id, test.kind)
		if err != nil {
			t.Fatalf("get %s: %v", test.kind, err)
		}
		if !bytes.Equal(got.Bytes, test.body) || got.OriginalSize != test.original || got.Truncated != test.truncated {
			t.Fatalf("get %s = bytes %v, original %d, truncated %t", test.kind, got.Bytes, got.OriginalSize, got.Truncated)
		}
	}
	if _, err := repository.GetRequestBody(context.Background(), id, "missing"); !errors.Is(err, ErrHistoryInvalidBody) {
		t.Fatalf("invalid kind error = %v", err)
	}
	if _, err := repository.GetRequestBody(context.Background(), id, "response"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetRequestBody(context.Background(), "ffffffffffffffffffffffffffffffff", "response"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing request error = %v", err)
	}
}

func TestT145GetRequestBodyRejectsInconsistentStoredMetadata(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	id := "0123456789abcdef0123456789abcdef"
	if _, err := database.Exec(`INSERT INTO requests (request_id, upstream_started) VALUES (?, 0)`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`PRAGMA ignore_check_constraints = ON`); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		body      []byte
		original  any
		truncated any
	}{
		{"negative original", []byte("x"), int64(-1), int64(0)},
		{"short non-truncated", []byte("x"), int64(2), int64(0)},
		{"equal truncated", []byte("x"), int64(1), int64(1)},
		{"bad flag", []byte("x"), int64(1), int64(2)},
	}
	for index, test := range cases {
		kind := "client_request"
		switch index {
		case 1:
			kind = "upstream_request"
		case 2:
			kind = "response"
		}
		if _, err := database.Exec(`INSERT INTO request_bodies (request_id, body_kind, body, original_size, truncated) VALUES (?, ?, ?, ?, ?)`, id, kind, test.body, test.original, test.truncated); err != nil {
			t.Fatalf("insert %s: %v", test.name, err)
		}
		if _, err := NewRequestHistoryRepository(database).GetRequestBody(context.Background(), id, kind); !errors.Is(err, ErrHistoryInvalidBody) {
			t.Fatalf("%s error = %v", test.name, err)
		}
		if _, err := database.Exec(`DELETE FROM request_bodies WHERE request_id = ? AND body_kind = ?`, id, kind); err != nil {
			t.Fatal(err)
		}
	}
	oversized := bytes.Repeat([]byte{'x'}, int(RequestBodySchemaSafetyMaxBytes)+1)
	if _, err := database.Exec(`INSERT INTO request_bodies (request_id, body_kind, body, original_size, truncated) VALUES (?, 'client_request', ?, ?, 0)`, id, oversized, int64(len(oversized))); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRequestHistoryRepository(database).GetRequestBody(context.Background(), id, "client_request"); !errors.Is(err, ErrHistoryInvalidBody) {
		t.Fatalf("oversized body error = %v", err)
	}
}

func TestT145GetRequestBodyReturnsZeroByteCapture(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	id := "0123456789abcdef0123456789abcdef"
	if _, err := database.Exec(`INSERT INTO requests (request_id, upstream_started) VALUES (?, 0)`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO request_bodies (request_id, body_kind, body, original_size, truncated) VALUES (?, 'response', ?, 0, 0)`, id, []byte{}); err != nil {
		t.Fatal(err)
	}
	got, err := NewRequestHistoryRepository(database).GetRequestBody(context.Background(), id, "response")
	if err != nil {
		t.Fatal(err)
	}
	if got.OriginalSize != 0 || got.Truncated || len(got.Bytes) != 0 {
		t.Fatalf("zero-byte capture = %#v", got)
	}
}
