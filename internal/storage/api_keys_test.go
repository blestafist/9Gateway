package storage

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/auth"
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

func TestAPIKeyRepositoryListAPIsafeCursorPaginationAndSummary(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository := NewAPIKeyRepository(database)
	created := time.Unix(1_700_000_000, 0).UTC()
	for index, policy := range []string{`{"allowed_models":["a"],"log_request_body":true}`, `{}`, `{"denied_models":["b"],"log_response_body":true}`} {
		when := created.Add(time.Duration(index) * time.Second)
		if err := repository.Insert(ctx, APIKeyRecord{ID: string(rune('a' + index)), Name: "key", DisplayPrefix: "prefix-" + string(rune('a'+index)), Digest: bytesOf(byte(index + 1)), Enabled: true, CreatedAt: when, UpdatedAt: when, PolicyJSON: policy}); err != nil {
			t.Fatal(err)
		}
	}
	page, cursor, err := repository.ListAPIKeys(ctx, 2, "")
	if err != nil || len(page) != 2 || cursor == "" {
		t.Fatalf("first page = %#v, cursor %q, error %v", page, cursor, err)
	}
	if page[0].ID != "c" || page[1].ID != "b" || !page[0].PolicySummary.DenyModels || !page[0].PolicySummary.LogResponseBody || page[1].PolicySummary.AllowModels || page[1].PolicySummary.LogRequestBody {
		t.Fatalf("page order/summary = %#v", page)
	}
	page2, cursor2, err := repository.ListAPIKeys(ctx, 2, cursor)
	if err != nil || len(page2) != 1 || cursor2 != "" || page2[0].ID != "a" || page2[0].ExpiresAt != nil {
		t.Fatalf("second page = %#v, cursor %q, error %v", page2, cursor2, err)
	}
	if _, _, err := repository.ListAPIKeys(ctx, 2, cursor+"tampered"); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("tampered cursor error = %v", err)
	}
}

func TestAPIKeyRepositoryGetDetailParsesEffectivePolicyAndHidesSecrets(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository := NewAPIKeyRepository(database)
	when := time.Unix(1_700_000_000, 0).UTC()
	policy := `{"allowed_models":["gpt-*"],"denied_models":["gpt-secret"],"request_windows":[{"amount":7,"duration":"90s"}],"token_windows":[{"amount":1234,"duration":"2h"}],"token_mode":"usage_only","max_concurrent_requests":3,"budget_limits":[{"amount_micros":42,"period":"total"},{"amount_micros":43,"period":"day"},{"amount_micros":44,"period":"month"}],"log_request_body":true,"log_response_body":true}`
	if err := repository.Insert(ctx, APIKeyRecord{ID: "detail-key", Name: "detail", DisplayPrefix: "prefix", Digest: bytesOf(1), Enabled: false, ExpiresAt: nil, CreatedAt: when, UpdatedAt: when, PolicyJSON: policy}); err != nil {
		t.Fatal(err)
	}
	detail, err := repository.GetAPIKeyByID(ctx, "detail-key")
	if err != nil {
		t.Fatal(err)
	}
	if detail.ID != "detail-key" || detail.Enabled || detail.ExpiresAt != nil || detail.Policy.MaxConcurrency() != 3 || detail.Policy.TokenMode() != auth.TokenModeUsageOnly {
		t.Fatalf("detail metadata/policy = %#v", detail)
	}
	if got := detail.Policy.RequestWindows(); len(got) != 1 || got[0].Amount != 7 || got[0].Duration != 90*time.Second {
		t.Fatalf("request windows = %#v", got)
	}
	if got := detail.Policy.TokenWindows(); len(got) != 1 || got[0].Amount != 1234 || got[0].Duration != 2*time.Hour {
		t.Fatalf("token windows = %#v", got)
	}
	if total, ok := detail.Policy.TotalBudget(); func() bool { micros, known := total.Micros(); return !ok || !known || micros != 42 }() {
		t.Fatalf("total budget = %#v/%v", total, ok)
	}
	if !detail.Policy.LogRequestBody() || !detail.Policy.LogResponseBody() || len(detail.Policy.AllowedModels()) != 1 || len(detail.Policy.DeniedModels()) != 1 {
		t.Fatalf("policy details = %#v", detail.Policy)
	}
	if _, err := database.Exec(`UPDATE api_keys SET policy_json = ? WHERE id = ?`, `{"unknown":true}`, "detail-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetAPIKeyByID(ctx, "detail-key"); !errors.Is(err, auth.ErrInvalidPolicy) {
		t.Fatalf("corrupt policy error = %v, want auth.ErrInvalidPolicy", err)
	}
}

func TestAPIKeyRepositorySetEnabledPreservesFutureTimestampInvariant(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	repository := NewRepository(database)
	future := time.Now().UTC().Add(24 * time.Hour).Truncate(time.Second)
	record := APIKeyRecord{ID: "future", Name: "future", DisplayPrefix: "prefix-future", Digest: bytesOf(9), Enabled: true, CreatedAt: future, UpdatedAt: future}
	if err := repository.Insert(ctx, record); err != nil {
		t.Fatal(err)
	}
	if err := repository.SetEnabled(ctx, record.ID, false); err != nil {
		t.Fatal(err)
	}
	got, err := repository.Get(ctx, record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedAt.Before(got.CreatedAt) || got.UpdatedAt.Before(future) || got.Enabled {
		t.Fatalf("toggled record = %#v", got)
	}
	listed, err := repository.List(ctx)
	if err != nil || len(listed) != 1 || listed[0].UpdatedAt.Before(listed[0].CreatedAt) {
		t.Fatalf("listed records = %#v, error %v", listed, err)
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
