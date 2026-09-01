package httpserver

import (
	"net/http/httptest"
	"testing"
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
