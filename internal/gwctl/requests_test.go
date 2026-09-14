package gwctl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

type interruptedReader struct {
	data []byte
	read bool
}

func (reader *interruptedReader) Read(dst []byte) (int, error) {
	if !reader.read {
		reader.read = true
		copy(dst, reader.data)
		return len(reader.data), nil
	}
	return 0, errors.New("interrupted")
}

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

func TestRequestsListTraversesMoreThanFiftyPages(t *testing.T) {
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := int(calls.Add(1))
		w.Header().Set("Content-Type", "application/json")
		if page < 53 {
			_, _ = fmt.Fprintf(w, `{"requests":[{"request_id":"%032x"}],"next_cursor":"cursor-%d"}`, page, page)
			return
		}
		_, _ = fmt.Fprintf(w, `{"requests":[{"request_id":"%032x"}]}`, page)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	status := Run(context.Background(), []string{"--gateway-url", server.URL, "--admin-credential", "secret", "requests", "list", "--json"}, &stdout, &stderr)
	if status != ExitSuccess {
		t.Fatalf("status = %d, stderr = %q", status, stderr.String())
	}
	var result aggregateRequestList
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Requests) != 53 || calls.Load() != 53 {
		t.Fatalf("rows/calls = %d/%d", len(result.Requests), calls.Load())
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

func TestRequestBodyOversizeLeavesDestinationUntouched(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "body.bin")
	want := []byte("preexisting")
	if err := os.WriteFile(path, want, 0640); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Original-Size", strconv.Itoa(maxResponseBytes+1))
		w.Header().Set("X-Truncated", "false")
		flusher := w.(http.Flusher)
		for i := 0; i < maxResponseBytes+1; i += 4096 {
			end := i + 4096
			if end > maxResponseBytes+1 {
				end = maxResponseBytes + 1
			}
			_, _ = w.Write(bytes.Repeat([]byte{'x'}, end-i))
			flusher.Flush()
		}
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	status := Run(context.Background(), []string{"--gateway-url", server.URL, "--admin-credential", "secret", "requests", "get", "0123456789abcdef0123456789abcdef", "--body", "response", "--output", path}, &stdout, &stderr)
	if status != ExitAPI {
		t.Fatalf("status = %d, stderr = %q", status, stderr.String())
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("destination = %q/%v", got, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != "body.bin" {
		t.Fatalf("temporary artifact(s) = %v/%v", entries, err)
	}
}

func TestRequestBodyInterruptedReadLeavesDestinationUntouched(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "body.bin")
	want := []byte("preexisting")
	if err := os.WriteFile(path, want, 0640); err != nil {
		t.Fatal(err)
	}
	if err := writeBodyFile(path, &interruptedReader{data: []byte("partial")}); err == nil {
		t.Fatal("interrupted body unexpectedly succeeded")
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("destination = %q/%v", got, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != "body.bin" {
		t.Fatalf("temporary artifact(s) = %v/%v", entries, err)
	}
}

func TestRequestBodyOversizeDoesNotPartiallyWriteStdout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Original-Size", strconv.Itoa(maxResponseBytes+1))
		w.Header().Set("X-Truncated", "false")
		flusher := w.(http.Flusher)
		for i := 0; i < maxResponseBytes+1; i += 4096 {
			end := i + 4096
			if end > maxResponseBytes+1 {
				end = maxResponseBytes + 1
			}
			_, _ = w.Write(bytes.Repeat([]byte{'x'}, end-i))
			flusher.Flush()
		}
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	status := Run(context.Background(), []string{"--gateway-url", server.URL, "--admin-credential", "secret", "requests", "get", "0123456789abcdef0123456789abcdef", "--body", "response"}, &stdout, &stderr)
	if status != ExitAPI || stdout.Len() != 0 {
		t.Fatalf("status/stdout = %d/%d", status, stdout.Len())
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
