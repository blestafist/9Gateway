package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const requestIDHeader = "X-Gateway-Request-ID"

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
	client  *http.Client
	baseURL *url.URL
	apiKey  string
}

func newProxyHandler(client *http.Client, baseURL, apiKey string) http.Handler {
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			http.Error(response, "invalid upstream URL", http.StatusInternalServerError)
		})
	}

	return &proxyHandler{client: client, baseURL: parsedURL, apiKey: apiKey}
}

func (handler *proxyHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	targetURL := *handler.baseURL
	targetURL.Path, targetURL.RawPath = joinURLPath(handler.baseURL, request.URL)
	targetURL.RawQuery = request.URL.RawQuery
	targetURL.Fragment = ""

	upstreamRequest, err := http.NewRequestWithContext(request.Context(), request.Method, targetURL.String(), request.Body)
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
	copyEndToEndHeaders(response.Header(), upstreamResponse.Header)
	response.WriteHeader(upstreamResponse.StatusCode)
	_, _ = io.Copy(response, upstreamResponse.Body)
}

func joinURLPath(baseURL, requestURL *url.URL) (string, string) {
	join := func(basePath, requestPath string) string {
		baseSlash := strings.HasSuffix(basePath, "/")
		requestSlash := strings.HasPrefix(requestPath, "/")
		switch {
		case baseSlash && requestSlash:
			return basePath + requestPath[1:]
		case !baseSlash && !requestSlash:
			return basePath + "/" + requestPath
		default:
			return basePath + requestPath
		}
	}

	path := join(baseURL.Path, requestURL.Path)
	if baseURL.RawPath == "" && requestURL.RawPath == "" {
		return path, ""
	}
	return path, join(baseURL.EscapedPath(), requestURL.EscapedPath())
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
