package httpserver

import (
	"mime"
	"net/http"
	"strings"
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
