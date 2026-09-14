package gwctl

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequestsListFiltersAndAggregatesJSON(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "next" {
			_, _ = w.Write([]byte(`{"requests":[{"request_id":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","api_key_name":null,"total_tokens":null,"cost_micros":null,"finished_at":null}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"requests":[{"request_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","api_key_name":"demo","method":"POST","route":"chat","model":"m","downstream_status":200,"total_tokens":1500,"cost_micros":1500000,"total_micros":1234000,"finished_at":"2024-01-02T03:04:05Z"}],"next_cursor":"next"}`))
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	status := Run(context.Background(), []string{"--gateway-url", server.URL, "--admin-credential", "secret", "requests", "list", "--key-id", "key_demo", "--after", "2024-01-01T00:00:00Z", "--before", "2024-01-03T00:00:00Z", "--json"}, &stdout, &stderr)
	if status != ExitSuccess {
		t.Fatalf("status = %d, stderr = %q", status, stderr.String())
	}
	var result struct {
		Requests []map[string]any `json:"requests"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || len(result.Requests) != 2 {
		t.Fatalf("result = %v, output = %q", err, stdout.String())
	}
	if result.Requests[1]["api_key_name"] != nil || !strings.Contains(stderr.String(), "Fetching request page 2") {
		t.Fatalf("null/progress = %#v/%q", result.Requests[1], stderr.String())
	}
	if len(queries) != 2 || !strings.Contains(queries[0], "key_id=key_demo") || !strings.Contains(queries[0], "after=2024-01-01T00%3A00%3A00Z") || !strings.Contains(queries[1], "cursor=next") || !strings.Contains(queries[1], "key_id=key_demo") {
		t.Fatalf("queries = %#v", queries)
	}
}

func TestRequestsBodyIsExactAndHasNoStdoutContamination(t *testing.T) {
	want := []byte{0x00, 0xff, 0x01, '\n'}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("X-Original-Size", "10")
		w.Header().Set("X-Truncated", "true")
		_, _ = w.Write(want)
	}))
	defer server.Close()
	id := "0123456789abcdef0123456789abcdef"
	var stdout, stderr bytes.Buffer
	status := Run(context.Background(), []string{"--gateway-url", server.URL, "--admin-credential", "secret", "requests", "get", id, "--body", "response"}, &stdout, &stderr)
	if status != ExitSuccess || !bytes.Equal(stdout.Bytes(), want) || !strings.Contains(stderr.String(), "truncated") {
		t.Fatalf("status/stdout/stderr = %d/%v/%q", status, stdout.Bytes(), stderr.String())
	}
	path := filepath.Join(t.TempDir(), "body.bin")
	stdout.Reset()
	status = Run(context.Background(), []string{"--gateway-url", server.URL, "--admin-credential", "secret", "requests", "get", id, "--body", "response", "--output", path}, &stdout, &stderr)
	if status != ExitSuccess || stdout.Len() != 0 {
		t.Fatalf("file status/stdout = %d/%q", status, stdout.String())
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("file = %v/%v", err, got)
	}
}

func TestRequestFormatting(t *testing.T) {
	value := int64(1500000)
	duration := int64(1234000)
	if got := formatMicrosCost(&value); got != "$1.50" {
		t.Errorf("cost = %q", got)
	}
	if got := formatDuration(&duration); got != "1.234s" {
		t.Errorf("duration = %q", got)
	}
	if got := formatInteger(1234567890); got != "1,234,567,890" {
		t.Errorf("integer = %q", got)
	}
}
