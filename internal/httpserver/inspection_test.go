package httpserver

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pestit/9gateway/internal/protocol/openai"
	"github.com/pestit/9gateway/internal/transport"
)

func TestIsJSONMediaType(t *testing.T) {
	for _, test := range []struct {
		name   string
		values []string
		want   bool
	}{
		{name: "application json", values: []string{"application/json"}, want: true},
		{name: "parameters", values: []string{"application/json; charset=utf-8"}, want: true},
		{name: "json suffix", values: []string{"text/problem+json"}, want: true},
		{name: "mixed case", values: []string{"Application/Problem+JSON; Charset=UTF-8"}, want: true},
		{name: "missing", want: false},
		{name: "repeated", values: []string{"application/json", "application/problem+json"}, want: false},
		{name: "malformed", values: []string{"application/json; charset"}, want: false},
		{name: "non json", values: []string{"application/octet-stream"}, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			header := make(http.Header)
			for _, value := range test.values {
				header.Add("Content-Type", value)
			}
			if got := isJSONMediaType(header); got != test.want {
				t.Fatalf("isJSONMediaType(%v) = %t, want %t", test.values, got, test.want)
			}
		})
	}
}

func TestProxyPreservesInspectedChatAndUnknownRequestBodies(t *testing.T) {
	overLimitBody := append([]byte(`{"padding":"`), bytes.Repeat([]byte{'x'}, int(requestInspectionLimit))...)
	overLimitBody = append(overLimitBody, []byte(`"}`)...)
	tests := []struct {
		name          string
		path          string
		body          []byte
		contentType   string
		unknownLength bool
	}{
		{
			name:        "valid chat body",
			path:        "/v1/chat/completions",
			body:        []byte(`{"model":"gpt-test","stream":false,"messages":[{"role":"user","content":"keep"}]}`),
			contentType: "application/json; charset=utf-8",
		},
		{
			name:        "malformed chat body",
			path:        "/v1/chat/completions",
			body:        []byte(`{"model":"gpt-test"`),
			contentType: "application/json",
		},
		{
			name:        "over-limit chat body",
			path:        "/v1/chat/completions",
			body:        overLimitBody,
			contentType: "application/json",
		},
		{
			name:          "unknown streaming endpoint body",
			path:          "/v1/unknown",
			body:          []byte("raw unknown endpoint bytes"),
			contentType:   "application/json",
			unknownLength: true,
		},
	}

	type receivedRequest struct {
		body          []byte
		contentLength int64
		header        string
	}
	received := make(chan receivedRequest, len(tests))
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read upstream request body: %v", err)
			return
		}
		received <- receivedRequest{
			body:          body,
			contentLength: request.ContentLength,
			header:        request.Header.Get("X-Inspection-Test"),
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	gateway := httptest.NewServer(NewHandler(transport.NewClient(), upstream.URL, "upstream-secret"))
	t.Cleanup(gateway.Close)

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var body io.Reader = bytes.NewReader(test.body)
			if test.unknownLength {
				body = io.NopCloser(bytes.NewReader(test.body))
			}
			request, err := http.NewRequest(http.MethodPost, gateway.URL+test.path, body)
			if err != nil {
				t.Fatalf("create request: %v", err)
			}
			request.Header.Set("Content-Type", test.contentType)
			request.Header.Set("X-Inspection-Test", test.name)
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatalf("POST %s: %v", test.path, err)
			}
			response.Body.Close()

			select {
			case got := <-received:
				if !bytes.Equal(got.body, test.body) {
					t.Fatalf("upstream body changed: got %q, want %q", got.body, test.body)
				}
				wantLength := int64(len(test.body))
				if test.unknownLength {
					wantLength = -1
				}
				if got.contentLength != wantLength {
					t.Fatalf("upstream ContentLength = %d, want %d", got.contentLength, wantLength)
				}
				if got.header != test.name {
					t.Fatalf("upstream X-Inspection-Test = %q, want %q", got.header, test.name)
				}
			case <-time.After(time.Second):
				t.Fatal("upstream did not receive request body")
			}
		})
	}
}

func TestProxyDispatchReceivesDistinctChatStreamStates(t *testing.T) {
	type streamState struct {
		present bool
		value   bool
	}
	states := make(chan streamState, 3)
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	proxy := newProxyHandler(transport.NewClient(), upstream.URL, "upstream-secret")
	proxy.responseDispatch = func(response http.ResponseWriter, upstreamResponse *http.Response, metadata *openai.RequestMetadata) {
		state := streamState{}
		if metadata != nil && metadata.Stream != nil {
			state.present = true
			state.value = *metadata.Stream
		}
		states <- state
		response.WriteHeader(upstreamResponse.StatusCode)
	}
	gateway := httptest.NewServer(proxy)
	t.Cleanup(gateway.Close)

	for _, test := range []struct {
		name    string
		body    string
		present bool
		value   bool
	}{
		{name: "stream false", body: `{"stream":false}`, present: true, value: false},
		{name: "stream true", body: `{"stream":true}`, present: true, value: true},
		{name: "stream absent", body: `{}`, present: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(http.MethodPost, gateway.URL+"/v1/chat/completions", bytes.NewBufferString(test.body))
			if err != nil {
				t.Fatalf("create request: %v", err)
			}
			request.Header.Set("Content-Type", "application/json")
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatalf("POST chat completions: %v", err)
			}
			response.Body.Close()

			select {
			case got := <-states:
				if got.present != test.present || (got.present && got.value != test.value) {
					t.Fatalf("stream state = %+v, want present=%t value=%t", got, test.present, test.value)
				}
			case <-time.After(time.Second):
				t.Fatal("response dispatch did not receive request metadata")
			}
		})
	}
}
