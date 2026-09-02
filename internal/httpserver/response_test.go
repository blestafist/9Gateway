package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pestit/9gateway/internal/protocol/openai"
)

func TestClassifyResponse(t *testing.T) {
	for _, test := range []struct {
		name        string
		contentType string
		want        ResponseMode
	}{
		{
			name:        "SSE",
			contentType: "text/event-stream",
			want:        ResponseModeSSE,
		},
		{
			name:        "SSE with parameters",
			contentType: "text/event-stream; charset=utf-8",
			want:        ResponseModeSSE,
		},
		{
			name:        "JSON",
			contentType: "application/json",
			want:        ResponseModeJSON,
		},
		{
			name:        "JSON with parameters",
			contentType: "application/json; charset=utf-8",
			want:        ResponseModeJSON,
		},
		{
			name:        "JSON structured syntax suffix",
			contentType: "application/problem+json",
			want:        ResponseModeJSON,
		},
		{
			name:        "JSON structured syntax suffix outside application",
			contentType: "text/problem+json",
			want:        ResponseModeJSON,
		},
		{
			name:        "mixed case media type",
			contentType: "Application/Problem+JSON; Charset=UTF-8",
			want:        ResponseModeJSON,
		},
		{
			name:        "binary",
			contentType: "application/octet-stream",
			want:        ResponseModeOpaque,
		},
		{
			name:        "empty",
			contentType: "",
			want:        ResponseModeOpaque,
		},
		{
			name:        "malformed",
			contentType: "application/json; charset",
			want:        ResponseModeOpaque,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := classifyResponse(test.contentType); got != test.want {
				t.Fatalf("classifyResponse(%q) = %q, want %q", test.contentType, got, test.want)
			}
		})
	}
}

func TestClassifyResponseHeaderRejectsMultipleContentTypes(t *testing.T) {
	header := make(http.Header)
	header.Add("Content-Type", "text/event-stream")
	header.Add("Content-Type", "application/json")

	if got := classifyResponseHeader(header); got != ResponseModeOpaque {
		t.Fatalf("classifyResponseHeader() = %q, want %q", got, ResponseModeOpaque)
	}
}

func TestClassifyResponseDoesNotReadRequestStreamField(t *testing.T) {
	for _, stream := range []string{"true", "false"} {
		t.Run(stream, func(t *testing.T) {
			request := httptest.NewRequest("POST", "http://gateway.example.test/v1/responses", nil)
			request.Header.Set("Content-Type", "application/json")
			request.Form = map[string][]string{"stream": {stream}}

			if got := classifyResponse("text/event-stream"); got != ResponseModeSSE {
				t.Fatalf("classifyResponse with request stream=%s = %q, want %q", stream, got, ResponseModeSSE)
			}
		})
	}
}

func TestShouldAggregateSSE(t *testing.T) {
	streamFalse := false
	streamTrue := true

	for _, endpoint := range []struct {
		name string
		path string
		want bool
	}{
		{name: "eligible chat completions", path: "/v1/chat/completions", want: true},
		{name: "unknown endpoint", path: "/v1/unknown", want: false},
	} {
		for _, stream := range []struct {
			name     string
			metadata *openai.RequestMetadata
		}{
			{name: "false", metadata: &openai.RequestMetadata{Stream: &streamFalse}},
			{name: "true", metadata: &openai.RequestMetadata{Stream: &streamTrue}},
			{name: "absent", metadata: &openai.RequestMetadata{}},
			{name: "unavailable", metadata: nil},
		} {
			for _, responseMode := range []struct {
				name string
				mode ResponseMode
				want bool
			}{
				{name: "JSON", mode: ResponseModeJSON},
				{name: "SSE", mode: ResponseModeSSE, want: endpoint.want && stream.name == "false"},
				{name: "opaque", mode: ResponseModeOpaque},
			} {
				t.Run(endpoint.name+"/stream "+stream.name+"/response "+responseMode.name, func(t *testing.T) {
					request := httptest.NewRequest(http.MethodPost, "http://gateway.example.test"+endpoint.path, nil)
					if got := shouldAggregateSSE(request, stream.metadata, responseMode.mode); got != responseMode.want {
						t.Fatalf("shouldAggregateSSE(%q, %q, %q) = %t, want %t", endpoint.path, stream.name, responseMode.mode, got, responseMode.want)
					}
				})
			}
		}
	}
}
