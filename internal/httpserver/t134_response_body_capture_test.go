package httpserver

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/observability"
	"github.com/pestit/9gateway/internal/transport"
)

func TestT134ResponseBodyCaptureRetainsDeliveredJSONAndOpaqueWireBytes(t *testing.T) {
	compressed := t134Gzip(t, []byte{0x00, 0xff, 0x80, 0x01})
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Upstream", "preserved")
		switch request.URL.Path {
		case "/v1/json":
			response.Header().Set("Content-Type", "application/json")
			_, _ = response.Write([]byte(`{"message":"héllo"}`))
		case "/v1/gzip":
			response.Header().Set("Content-Type", "application/json")
			response.Header().Set("Content-Encoding", "gzip")
			_, _ = response.Write(compressed)
		default:
			response.Header().Set("Content-Type", "application/octet-stream")
			_, _ = response.Write([]byte{0x00, 0xff, 0x80, 0x01})
		}
	}))
	t.Cleanup(upstream.Close)

	key, authenticator := t134Authenticator(t, `{"log_response_body":true}`)
	var traces []*RequestTraceState
	proxy := withGatewayAuthentication(authenticator, newProxyHandlerWithLimitersAndTokenConfig(
		transport.NewClient(), upstream.URL, "upstream", limiter.NewRequestLimiter(nil),
		limiter.NewConcurrencyLimiter(), TokenAdmissionConfig{MaxCapturedBodyBytes: 128},
	))
	handler := withRequestID(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		traces = append(traces, TraceFromContext(request.Context()))
		proxy.ServeHTTP(response, request)
	}))
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)

	tests := []struct {
		name string
		path string
		want []byte
	}{
		{name: "json", path: "/v1/json", want: []byte(`{"message":"héllo"}`)},
		{name: "gzip", path: "/v1/gzip", want: compressed},
		{name: "opaque binary", path: "/v1/binary", want: []byte{0x00, 0xff, 0x80, 0x01}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodGet, gateway.URL+test.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+key.RawKey)
			request.Header.Set("Accept-Encoding", "identity")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			if response.StatusCode != http.StatusOK || !bytes.Equal(body, test.want) || response.Header.Get("X-Upstream") != "preserved" {
				t.Fatalf("response = %d, headers = %v, body = %x; want 200, preserved, %x", response.StatusCode, response.Header, body, test.want)
			}
			trace := traces[len(traces)-1]
			snapshot, ok := trace.ResponseBodySnapshot()
			if !ok || snapshot.Kind != observability.BodyKindResponse || !snapshot.Captured || snapshot.Truncated || snapshot.OriginalSize != int64(len(test.want)) || !bytes.Equal(snapshot.Bytes, test.want) {
				t.Fatalf("snapshot = %s/%v; want delivered response", snapshot, ok)
			}
		})
	}
}

func TestT134ResponseBodyCaptureKnownEmptyAndBounded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/octet-stream")
		if request.URL.Path == "/v1/bounded" {
			_, _ = response.Write([]byte("0123456789"))
		}
	}))
	t.Cleanup(upstream.Close)
	key, authenticator := t134Authenticator(t, `{"log_response_body":true}`)
	var trace *RequestTraceState
	proxy := withGatewayAuthentication(authenticator, newProxyHandlerWithLimitersAndTokenConfig(
		transport.NewClient(), upstream.URL, "upstream", limiter.NewRequestLimiter(nil),
		limiter.NewConcurrencyLimiter(), TokenAdmissionConfig{MaxCapturedBodyBytes: 4},
	))
	handler := withRequestID(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		trace = TraceFromContext(request.Context())
		proxy.ServeHTTP(response, request)
	}))
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)

	for _, test := range []struct {
		path         string
		wantBody     []byte
		wantCaptured bool
		wantOriginal int64
		wantTrunc    bool
	}{
		{path: "/v1/empty", wantCaptured: true},
		{path: "/v1/bounded", wantBody: []byte("0123"), wantCaptured: true, wantOriginal: 10, wantTrunc: true},
	} {
		request, err := http.NewRequest(http.MethodGet, gateway.URL+test.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+key.RawKey)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil || !bytes.Equal(body, func() []byte {
			if test.path == "/v1/empty" {
				return nil
			}
			return []byte("0123456789")
		}()) {
			t.Fatalf("body = %x/%v", body, readErr)
		}
		snapshot, ok := trace.ResponseBodySnapshot()
		if !ok || snapshot.Captured != test.wantCaptured || snapshot.OriginalSize != test.wantOriginal || snapshot.Truncated != test.wantTrunc || !bytes.Equal(snapshot.Bytes, test.wantBody) {
			t.Fatalf("snapshot = %s/%v", snapshot, ok)
		}
	}
}

func TestT134ResponseBodyCaptureDisabledByPolicyOrBound(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{}`))
	}))
	t.Cleanup(upstream.Close)

	for _, test := range []struct {
		name   string
		policy string
		bound  int64
	}{
		{name: "policy disabled", policy: `{}`, bound: 128},
		{name: "global bound disabled", policy: `{"log_response_body":true}`, bound: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			key, authenticator := t134Authenticator(t, test.policy)
			var trace *RequestTraceState
			proxy := withGatewayAuthentication(authenticator, newProxyHandlerWithLimitersAndTokenConfig(
				transport.NewClient(), upstream.URL, "upstream", limiter.NewRequestLimiter(nil),
				limiter.NewConcurrencyLimiter(), TokenAdmissionConfig{MaxCapturedBodyBytes: test.bound},
			))
			handler := withRequestID(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				trace = TraceFromContext(request.Context())
				proxy.ServeHTTP(response, request)
			}))
			gateway := httptest.NewServer(handler)
			defer gateway.Close()
			request, err := http.NewRequest(http.MethodGet, gateway.URL+"/v1/response", nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+key.RawKey)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if _, ok := trace.ResponseBodySnapshot(); ok {
				t.Fatal("disabled response capture produced a snapshot")
			}
		})
	}
}

func TestT134CompletionWriterCapturesOnlyAcceptedPrefixOnShortWrite(t *testing.T) {
	underlying := &t124ResponseWriter{header: make(http.Header), results: []t124WriteResult{{n: 3, err: ioShortWrite}}}
	state, _ := traceTestState(t)
	recorder, err := observability.NewBodyRecorder(observability.BodyKindResponse, 16)
	if err != nil {
		t.Fatal(err)
	}
	writer := &completionResponseWriter{ResponseWriter: underlying, trace: state, responseBodyRecorder: recorder}
	if n, got := writer.Write([]byte("abcdef")); n != 3 || got != ioShortWrite {
		t.Fatalf("write = %d/%v", n, got)
	}
	snapshot := recorder.Finalize()
	if !bytes.Equal(snapshot.Bytes, []byte("abc")) || snapshot.OriginalSize != 3 || snapshot.Truncated || !snapshot.Captured {
		t.Fatalf("snapshot = %s", snapshot)
	}
}

func t134Gzip(t *testing.T, body []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func t134Authenticator(t *testing.T, policyJSON string) (auth.GeneratedGatewayKey, *auth.Authenticator) {
	t.Helper()
	key, err := auth.GenerateGatewayKey([]byte("t134-pepper"))
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := auth.NewAuthenticator([]byte("t134-pepper"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := authenticator.Load([]auth.Record{{ID: "t134-key", DisplayPrefix: key.DisplayPrefix, Digest: key.Digest, Enabled: true, PolicyJSON: []byte(policyJSON)}}); err != nil {
		t.Fatal(err)
	}
	return key, authenticator
}
