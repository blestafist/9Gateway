package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"time"
)

const requestIDHeader = "X-Gateway-Request-ID"

type requestIDContextKey struct{}

// NewHandler returns the gateway's HTTP handler using the provided upstream client.
func NewHandler(upstreamClient *http.Client) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", health)
	return newHandler(slog.Default(), mux)
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
