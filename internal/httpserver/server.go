package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	mux.Handle("/v1/", newProxyHandler(upstreamClient, upstreamBaseURL, upstreamAPIKey))
	return newHandler(slog.Default(), mux)
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
	targetURL.Path = request.URL.Path
	targetURL.RawPath = request.URL.RawPath
	targetURL.RawQuery = request.URL.RawQuery
	targetURL.Fragment = ""

	upstreamRequest, err := http.NewRequestWithContext(request.Context(), request.Method, targetURL.String(), request.Body)
	if err != nil {
		http.Error(response, "failed to create upstream request", http.StatusBadGateway)
		return
	}
	copyEndToEndHeaders(upstreamRequest.Header, request.Header)
	upstreamRequest.Header.Set("Authorization", "Bearer "+handler.apiKey)

	upstreamResponse, err := handler.client.Do(upstreamRequest)
	if err != nil {
		http.Error(response, "upstream request failed", http.StatusBadGateway)
		return
	}
	response.WriteHeader(upstreamResponse.StatusCode)
	upstreamResponse.Body.Close()
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
