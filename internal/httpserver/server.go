package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pestit/9gateway/internal/protocol/openai"
)

const requestIDHeader = "X-Gateway-Request-ID"

const requestInspectionLimit int64 = 64 * 1024

type requestIDContextKey struct{}

// NewHandler returns the gateway's HTTP handler using the provided upstream client.
func NewHandler(upstreamClient *http.Client, upstreamBaseURL, upstreamAPIKey string) http.Handler {
	proxy := newProxyHandler(upstreamClient, upstreamBaseURL, upstreamAPIKey)
	router := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/health":
			health(response, request)
		case strings.HasPrefix(request.URL.Path, "/v1/"):
			proxy.ServeHTTP(response, request)
		default:
			http.NotFound(response, request)
		}
	})
	return newHandler(slog.Default(), router)
}

type proxyHandler struct {
	client           *http.Client
	baseURL          *url.URL
	apiKey           string
	responseDispatch responseDispatchFunc
}

type responseDispatchFunc func(http.ResponseWriter, *http.Response, *openai.RequestMetadata)

func newProxyHandler(client *http.Client, baseURL, apiKey string) *proxyHandler {
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return &proxyHandler{client: client, apiKey: apiKey, responseDispatch: func(response http.ResponseWriter, _ *http.Response, _ *openai.RequestMetadata) {
			http.Error(response, "invalid upstream URL", http.StatusInternalServerError)
		}}
	}

	return &proxyHandler{client: client, baseURL: parsedURL, apiKey: apiKey, responseDispatch: dispatchResponse}
}

func (handler *proxyHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if handler.baseURL == nil {
		http.Error(response, "invalid upstream URL", http.StatusInternalServerError)
		return
	}

	targetURL := *handler.baseURL
	targetURL.Path, targetURL.RawPath = joinURLPath(handler.baseURL, request.URL)
	targetURL.RawQuery = request.URL.RawQuery
	targetURL.Fragment = ""

	requestBody, metadata := inspectChatRequest(request)
	upstreamRequest, err := http.NewRequestWithContext(request.Context(), request.Method, targetURL.String(), requestBody)
	if err != nil {
		http.Error(response, "failed to create upstream request", http.StatusBadGateway)
		return
	}
	upstreamRequest.ContentLength = request.ContentLength
	copyEndToEndHeaders(upstreamRequest.Header, request.Header)
	upstreamRequest.Header.Set("Authorization", "Bearer "+handler.apiKey)

	upstreamResponse, err := handler.client.Do(upstreamRequest)
	if err != nil {
		http.Error(response, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer upstreamResponse.Body.Close()
	dispatch := handler.responseDispatch
	if dispatch == nil {
		dispatch = dispatchResponse
	}
	dispatch(response, upstreamResponse, metadata)
}

func inspectChatRequest(request *http.Request) (io.ReadCloser, *openai.RequestMetadata) {
	if request.Method != http.MethodPost || request.URL.Path != "/v1/chat/completions" || !isJSONMediaType(request.Header) {
		return request.Body, nil
	}
	if request.Body == nil {
		return nil, nil
	}

	inspected, replacement, available, _ := openai.InspectRequestBody(request.Body, requestInspectionLimit)
	if replacement == nil {
		return request.Body, nil
	}
	body := &replayedRequestBody{Reader: replacement, source: request.Body}
	if !available {
		return body, nil
	}
	metadata, err := openai.ParseRequestMetadata(inspected)
	if err != nil {
		return body, nil
	}
	return body, &metadata
}

func isJSONMediaType(header http.Header) bool {
	contentTypes := header.Values("Content-Type")
	if len(contentTypes) != 1 {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(contentTypes[0])
	if err != nil {
		return false
	}
	mediaType = strings.ToLower(mediaType)
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

type replayedRequestBody struct {
	io.Reader
	source io.Closer
}

func (body *replayedRequestBody) Close() error {
	if body.source == nil {
		return nil
	}
	return body.source.Close()
}

func dispatchResponse(response http.ResponseWriter, upstreamResponse *http.Response, _ *openai.RequestMetadata) {
	copyResponseHeaders(response.Header(), upstreamResponse.Header)
	response.WriteHeader(upstreamResponse.StatusCode)
	if classifyResponseHeader(upstreamResponse.Header) == ResponseModeSSE {
		_ = streamResponseBody(response, upstreamResponse.Body)
		return
	}
	_, _ = io.Copy(response, upstreamResponse.Body)
}

func streamResponseBody(response http.ResponseWriter, body io.Reader) error {
	controller := http.NewResponseController(response)
	buffer := make([]byte, 32*1024)
	for {
		read, readErr := body.Read(buffer)
		if read > 0 {
			written, writeErr := response.Write(buffer[:read])
			if writeErr != nil {
				return writeErr
			}
			if written != read {
				return io.ErrShortWrite
			}
			if flushErr := controller.Flush(); flushErr != nil {
				return flushErr
			}
		}

		if readErr == io.EOF {
			return nil
		}
		if readErr != nil {
			return readErr
		}
	}
}

func joinURLPath(baseURL, requestURL *url.URL) (string, string) {
	if baseURL.RawPath == "" && requestURL.RawPath == "" {
		return joinPath(baseURL.Path, requestURL.Path)
	}

	baseEscapedPath := baseURL.EscapedPath()
	requestEscapedPath := requestURL.EscapedPath()
	baseSlash := strings.HasSuffix(baseEscapedPath, "/")
	requestSlash := strings.HasPrefix(requestEscapedPath, "/")
	switch {
	case baseSlash && requestSlash:
		return baseURL.Path + requestURL.Path[1:], baseEscapedPath + requestEscapedPath[1:]
	case !baseSlash && !requestSlash:
		return baseURL.Path + "/" + requestURL.Path, baseEscapedPath + "/" + requestEscapedPath
	default:
		return baseURL.Path + requestURL.Path, baseEscapedPath + requestEscapedPath
	}
}

func joinPath(basePath, requestPath string) (string, string) {
	baseSlash := strings.HasSuffix(basePath, "/")
	requestSlash := strings.HasPrefix(requestPath, "/")
	switch {
	case baseSlash && requestSlash:
		return basePath + requestPath[1:], ""
	case !baseSlash && !requestSlash:
		return basePath + "/" + requestPath, ""
	default:
		return basePath + requestPath, ""
	}
}

var hopByHopHeaders = map[string]struct{}{
	"connection":          {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"proxy-connection":    {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},
}

func copyEndToEndHeaders(destination, source http.Header) {
	copyHeaders(destination, source, nil)
}

func copyResponseHeaders(destination, source http.Header) {
	copyHeaders(destination, source, map[string]struct{}{
		"authorization":       {},
		"proxy-authorization": {},
	})
}

func copyHeaders(destination, source http.Header, deniedHeaders map[string]struct{}) {
	connectionTokens := make(map[string]struct{})
	for _, value := range source.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			connectionTokens[strings.ToLower(strings.TrimSpace(token))] = struct{}{}
		}
	}

	for name, values := range source {
		lowerName := strings.ToLower(name)
		if _, ok := hopByHopHeaders[lowerName]; ok {
			continue
		}
		if _, ok := connectionTokens[lowerName]; ok {
			continue
		}
		if _, ok := deniedHeaders[lowerName]; ok {
			continue
		}
		for _, value := range values {
			destination.Add(name, value)
		}
	}
}

func newHandler(logger *slog.Logger, next http.Handler) http.Handler {
	return withRequestID(withCompletionLog(logger, next))
}

func health(response http.ResponseWriter, request *http.Request) {
	response.WriteHeader(http.StatusOK)
}

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		id, err := newRequestID()
		if err != nil {
			http.Error(response, "failed to create request ID", http.StatusInternalServerError)
			return
		}

		response.Header().Set(requestIDHeader, id)
		request = request.WithContext(context.WithValue(request.Context(), requestIDContextKey{}, id))
		next.ServeHTTP(response, request)
	})
}

func withCompletionLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		writer := &completionResponseWriter{ResponseWriter: response}
		next.ServeHTTP(writer, request)

		logger.Info("request completed",
			"request_id", requestIDFromContext(request.Context()),
			"method", request.Method,
			"path", request.URL.Path,
			"status", writer.statusCode(),
			"duration", time.Since(startedAt),
		)
	})
}

func requestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDContextKey{}).(string)
	return id
}

type completionResponseWriter struct {
	http.ResponseWriter
	status int
}

func (writer *completionResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *completionResponseWriter) FlushError() error {
	err := http.NewResponseController(writer.ResponseWriter).Flush()
	if err == nil && writer.status == 0 {
		writer.status = http.StatusOK
	}
	return err
}

func (writer *completionResponseWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *completionResponseWriter) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(body)
}

func (writer *completionResponseWriter) statusCode() int {
	if writer.status == 0 {
		return http.StatusOK
	}
	return writer.status
}

func newRequestID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}
