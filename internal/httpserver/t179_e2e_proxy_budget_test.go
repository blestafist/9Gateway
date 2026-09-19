package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
)

// readProcessRSSBytes reads the VmRSS (Resident Set Size) of the current process on Linux.
func readProcessRSSBytes() (int64, error) {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, fmt.Errorf("unexpected statm format")
	}
	pages, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, err
	}
	pageSize := int64(os.Getpagesize())
	return pages * pageSize, nil
}

func TestT179_ProxyHotPathInvariantsWithUI(t *testing.T) {
	// Verify that embedded UI does not alter proxy first-byte, per-chunk, or stream-close behavior.
	clock := &requestLimitTestClock{now: time.Unix(100, 0).UTC()}

	chunkDelivered := make(chan struct{})
	upstreamProceed := make(chan struct{})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("expected upstream flusher")
			return
		}

		// 1. Send first chunk
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"first\"}}]}\n\n")
		flusher.Flush()

		// Wait for downstream confirmation that chunk 1 was received
		<-upstreamProceed

		// 2. Send second chunk
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"second\"}}]}\n\n")
		flusher.Flush()

		// Wait before closing
		<-upstreamProceed

		// Upstream closes
	}))
	defer upstream.Close()

	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	keysRepo := storage.NewAPIKeyRepository(database)

	tokens := limiter.NewTokenLimiter(clock.Now)
	worker := NewUsageObservationWorker(UsageObservationWorkerOptions{Capacity: 16})
	defer func() { shutdownObservationWorker(t, worker) }()

	handler, err := NewHandlerWithAdminAndLimitersAndTokenConfigAndTokenLimiterAndUsageObservationWorker(
		transport.NewClient(), upstream.URL, "upstream-secret", "admin-secret", "auth-pepper-12345678901234567890",
		keysRepo, limiter.NewRequestLimiter(clock.Now), limiter.NewConcurrencyLimiter(),
		nil, tokens, TokenAdmissionConfig{FallbackUnknownInputTokens: 10, FallbackMaxOutputTokens: 10}, worker,
	)
	if err != nil {
		t.Fatal(err)
	}

	gateway := httptest.NewServer(handler)
	defer gateway.Close()

	// 1. Create key via admin API
	createReq, err := http.NewRequest(http.MethodPost, gateway.URL+"/admin/v1/keys", bytes.NewBufferString(`{"name":"t179-hotpath"}`))
	if err != nil {
		t.Fatal(err)
	}
	createReq.Header.Set("Authorization", "Bearer admin-secret")
	createReq.Header.Set("Content-Type", "application/json")
	createResp, err := http.DefaultClient.Do(createReq)
	if err != nil {
		t.Fatal(err)
	}
	defer createResp.Body.Close()
	if createResp.StatusCode != http.StatusCreated {
		t.Fatalf("key creation failed with status %d", createResp.StatusCode)
	}
	var createdKey struct {
		Key string `json:"key"`
	}
	if err := json.NewDecoder(createResp.Body).Decode(&createdKey); err != nil {
		t.Fatal(err)
	}

	// 2. Verify UI route serves without interfering with proxy routes
	uiResp, err := gateway.Client().Get(gateway.URL + "/ui/")
	if err != nil {
		t.Fatal(err)
	}
	_ = uiResp.Body.Close()
	if uiResp.StatusCode != http.StatusOK {
		t.Fatalf("UI /ui/ returned status %d, want 200", uiResp.StatusCode)
	}

	// 3. Initiate streaming proxy request
	reqBody := `{"model":"gpt-4o-mini","stream":true}`
	proxyReq, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewBufferString(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	proxyReq.Header.Set("Authorization", "Bearer "+createdKey.Key)
	proxyReq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	proxyResp, err := http.DefaultClient.Do(proxyReq)
	if err != nil {
		t.Fatal(err)
	}
	defer proxyResp.Body.Close()

	if proxyResp.StatusCode != http.StatusOK {
		t.Fatalf("proxy returned status %d, want 200", proxyResp.StatusCode)
	}

	// Read first chunk immediately (first-byte delivery must not buffer)
	buf := make([]byte, 256)
	n, err := proxyResp.Body.Read(buf)
	firstByteDuration := time.Since(start)
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if !strings.Contains(string(buf[:n]), "first") {
		t.Fatalf("expected first chunk, got %q", string(buf[:n]))
	}

	// First chunk arrived promptly without buffering
	if firstByteDuration > 2*time.Second {
		t.Fatalf("first byte took too long: %v", firstByteDuration)
	}

	// Release upstream for chunk 2
	upstreamProceed <- struct{}{}

	// Read second chunk
	n2, err := proxyResp.Body.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if !strings.Contains(string(buf[:n2]), "second") {
		t.Fatalf("expected second chunk, got %q", string(buf[:n2]))
	}

	// Release upstream for EOF
	upstreamProceed <- struct{}{}

	// Downstream must receive EOF immediately when upstream closes
	closeBuf := make([]byte, 16)
	_, closeErr := proxyResp.Body.Read(closeBuf)
	if closeErr != io.EOF {
		t.Fatalf("expected immediate EOF on upstream close, got error: %v", closeErr)
	}

	close(chunkDelivered)
}

func TestT179_GatewayRSSGrowthUnderUIAndCachedAnalytics(t *testing.T) {
	// Acceptance criterion: gateway RSS growth from embedded assets plus 32 cached analytics
	// responses stays below 32 MiB in the representative test.

	runtime.GC()
	initialRSS, err := readProcessRSSBytes()
	var initialMem runtime.MemStats
	runtime.ReadMemStats(&initialMem)

	database, errOpen := storage.Open(context.Background(), ":memory:")
	if errOpen != nil {
		t.Fatal(errOpen)
	}
	defer database.Close()
	keysRepo := storage.NewAPIKeyRepository(database)

	handler, errHandler := NewHandlerWithAdmin(
		transport.NewClient(), "http://127.0.0.1:1", "upstream",
		"admin-secret", "auth-pepper-12345678901234567890", keysRepo,
	)
	if errHandler != nil {
		t.Fatal(errHandler)
	}

	server := httptest.NewServer(handler)
	defer server.Close()

	client := server.Client()

	// 1. Request embedded UI assets
	uiRoutes := []string{
		"/ui/",
		"/ui/theme-init.js",
		"/ui/overview",
		"/ui/usage",
		"/ui/keys",
		"/ui/requests",
		"/ui/system",
	}
	for _, route := range uiRoutes {
		resp, err := client.Get(server.URL + route)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	// 2. Perform 32 distinct cached analytics responses (overview and usage ranges)
	baseTime := time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 32; i++ {
		after := baseTime.Add(-time.Duration(i+1) * time.Hour).Format(time.RFC3339)
		before := baseTime.Add(-time.Duration(i) * time.Hour).Format(time.RFC3339)

		url := fmt.Sprintf("%s/admin/v1/overview?after=%s&before=%s", server.URL, after, before)
		req, _ := http.NewRequest(http.MethodGet, url, nil)
		req.Header.Set("Authorization", "Bearer admin-secret")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}

	runtime.GC()
	finalRSS, errFinal := readProcessRSSBytes()
	var finalMem runtime.MemStats
	runtime.ReadMemStats(&finalMem)

	const maxAllowedGrowthBytes = int64(32 * 1024 * 1024) // 32 MiB

	// Check HeapAlloc growth
	heapGrowth := int64(finalMem.HeapAlloc) - int64(initialMem.HeapAlloc)
	t.Logf("HeapAlloc growth: %d bytes (%.2f MiB)", heapGrowth, float64(heapGrowth)/(1024*1024))
	if heapGrowth > maxAllowedGrowthBytes {
		t.Fatalf("Heap growth %d bytes exceeded 32 MiB budget", heapGrowth)
	}

	// If RSS is readable on this OS, verify RSS growth
	if err == nil && errFinal == nil && initialRSS > 0 {
		rssGrowth := finalRSS - initialRSS
		t.Logf("Process RSS growth: %d bytes (%.2f MiB)", rssGrowth, float64(rssGrowth)/(1024*1024))
		if rssGrowth > maxAllowedGrowthBytes {
			t.Fatalf("Process RSS growth %d bytes exceeded 32 MiB budget", rssGrowth)
		}
	}
}
