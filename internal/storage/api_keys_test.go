package storage

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAPIKeyRepositoryRoundTripAndBoundaryCopies(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository := NewAPIKeyRepository(database)
	created := time.Unix(1_700_000_000, 0).UTC()
	digest := bytesOf(1)
	record := APIKeyRecord{
		ID:            "key-1",
		Name:          "Primary",
		DisplayPrefix: "sk-gw-a1",
		Digest:        digest,
		Enabled:       true,
		CreatedAt:     created,
		UpdatedAt:     created,
		PolicyJSON:    `{"rpm":10}`,
	}
	if err := repository.Insert(ctx, record); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	digest[0] = 99
	got, err := repository.LookupByDisplayPrefix(ctx, record.DisplayPrefix)
	if err != nil {
		t.Fatalf("LookupByDisplayPrefix() error = %v", err)
	}
	if got.Digest[0] != 1 || got.KeyHash[0] != 1 {
		t.Fatal("repository retained caller's digest mutation")
	}
	got.Digest[0] = 88
	got.KeyHash[1] = 77
	again, err := repository.GetByID(ctx, record.ID)
	if err != nil {
		t.Fatalf("GetByID() error = %v", err)
	}
	if again.Digest[0] != 1 || again.KeyHash[1] != 2 {
		t.Fatal("returned digest aliases repository state")
	}
	if again.ExpiresAt != nil || again.PolicyJSON != record.PolicyJSON || !again.CreatedAt.Equal(created) {
		t.Fatalf("round trip = %#v", again)
	}
}

func TestAPIKeyRepositoryListUpdateConflictsAndNotFound(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository := NewRepository(database)
	for _, record := range []APIKeyRecord{
		{ID: "key-b", Name: "B", DisplayPrefix: "prefix-b", Digest: bytesOf(2), Enabled: true, CreatedAt: time.Unix(2, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC()},
		{ID: "key-a", Name: "A", DisplayPrefix: "prefix-a", Digest: bytesOf(3), Enabled: true, CreatedAt: time.Unix(3, 0).UTC(), UpdatedAt: time.Unix(3, 0).UTC()},
	} {
		if err := repository.Insert(ctx, record); err != nil {
			t.Fatal(err)
		}
	}
	duplicate := APIKeyRecord{ID: "key-c", Name: "C", DisplayPrefix: "prefix-a", Digest: bytesOf(4), CreatedAt: time.Unix(4, 0).UTC(), UpdatedAt: time.Unix(4, 0).UTC()}
	if err := repository.Insert(ctx, duplicate); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate error = %v, want ErrConflict", err)
	}
	list, err := repository.List(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("List() = %#v, %v", list, err)
	}
	if list[0].ID != "key-a" || list[1].ID != "key-b" {
		t.Fatalf("list order = %q, %q", list[0].ID, list[1].ID)
	}
	if err := repository.UpdateEnabled(ctx, "key-a", false); err != nil {
		t.Fatalf("UpdateEnabled() error = %v", err)
	}
	updated, err := repository.Get(ctx, "key-a")
	if err != nil || updated.Enabled {
		t.Fatalf("disabled record = %#v, %v", updated, err)
	}
	if err := repository.SetEnabled(ctx, "missing", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing update error = %v, want ErrNotFound", err)
	}
	if _, err := repository.GetByID(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing lookup error = %v, want ErrNotFound", err)
	}
	if _, err := repository.LookupByPrefix(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing prefix error = %v, want ErrNotFound", err)
	}
}

func TestAPIKeyRepositoryReopensAndValidatesRecords(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "keys.db")
	database, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	repository := NewAPIKeyRepository(database)
	expires := time.Unix(1_800_000_000, 0).UTC()
	record := APIKeyRecord{ID: "key-1", Name: "name", DisplayPrefix: "prefix", Digest: bytesOf(1), ExpiresAt: &expires, CreatedAt: time.Unix(10, 0).UTC(), UpdatedAt: time.Unix(10, 0).UTC()}
	if err := repository.Insert(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	got, err := NewAPIKeyRepository(database).GetByID(ctx, record.ID)
	if err != nil || got.ExpiresAt == nil || !got.ExpiresAt.Equal(expires) {
		t.Fatalf("reopened record = %#v, %v", got, err)
	}

	invalid := record
	for _, mutate := range []func(*APIKeyRecord){
		func(r *APIKeyRecord) { r.Digest = []byte{1} },
		func(r *APIKeyRecord) { r.Name = " " },
		func(r *APIKeyRecord) { r.CreatedAt = time.Time{} },
		func(r *APIKeyRecord) { r.UpdatedAt = r.CreatedAt.Add(-time.Second) },
		func(r *APIKeyRecord) { r.ExpiresAt = timePtr(time.Unix(1, 1)) },
	} {
		candidate := invalid
		candidate.Digest = append([]byte(nil), invalid.Digest...)
		mutate(&candidate)
		if err := candidate.Validate(); !errors.Is(err, ErrInvalidRecord) {
			t.Errorf("invalid record error = %v, want ErrInvalidRecord", err)
		}
	}

	secret := "policy-secret-digest-credential"
	err = NewAPIKeyRepository(database).Insert(ctx, APIKeyRecord{ID: "", Name: secret, DisplayPrefix: secret, Digest: bytesOf(1), CreatedAt: expires, UpdatedAt: expires, PolicyJSON: secret})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe validation error = %v", err)
	}
}

func bytesOf(first byte) []byte {
	digest := make([]byte, HMACDigestSize)
	digest[0] = first
	for index := 1; index < len(digest); index++ {
		digest[index] = byte(index + 1)
	}
	return digest
}

func timePtr(value time.Time) *time.Time {
	return &value
}
