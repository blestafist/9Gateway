package httpserver

import (
	"mime"
	"net/http"
	"strings"

	"github.com/pestit/9gateway/internal/protocol/openai"
)

// ResponseMode identifies the transport behavior selected from an upstream
// response's media type.
type ResponseMode string

const (
	ResponseModeOpaque ResponseMode = "opaque"
	ResponseModeJSON   ResponseMode = "json"
	ResponseModeSSE    ResponseMode = "sse"
)

func classifyResponse(contentType string) ResponseMode {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return ResponseModeOpaque
	}

	mediaType = strings.ToLower(mediaType)
	switch {
	case mediaType == "text/event-stream":
		return ResponseModeSSE
	case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		return ResponseModeJSON
	default:
		return ResponseModeOpaque
	}
}

func classifyResponseHeader(header http.Header) ResponseMode {
	contentTypes := header.Values("Content-Type")
	if len(contentTypes) != 1 {
		return ResponseModeOpaque
	}
	return classifyResponse(contentTypes[0])
}

// shouldAggregateSSE reports whether a known chat-completions request asked
// for a non-streaming response while upstream actually returned SSE.
func shouldAggregateSSE(request *http.Request, metadata *openai.RequestMetadata, responseMode ResponseMode) bool {
	if request == nil || request.URL == nil || request.Method != http.MethodPost || request.URL.Path != "/v1/chat/completions" {
		return false
	}
	return metadata != nil && metadata.Stream != nil && !*metadata.Stream && responseMode == ResponseModeSSE
}
