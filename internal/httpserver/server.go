package httpserver

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/protocol/openai"
)

const requestIDHeader = "X-Gateway-Request-ID"

const requestInspectionLimit int64 = 64 * 1024

type requestIDContextKey struct{}

// NewHandler returns the gateway's HTTP handler using the provided upstream client.
func NewHandler(upstreamClient *http.Client, upstreamBaseURL, upstreamAPIKey string, authenticators ...*auth.Authenticator) http.Handler {
	// The convenience API does not own a completion worker. Applications that
	// want asynchronous completion logging should construct and own one with
	// NewCompletionLogger and pass it to NewHandlerWithCompletionLogger.
	return NewHandlerWithCompletionLogger(upstreamClient, upstreamBaseURL, upstreamAPIKey, nil, authenticators...)
}

// NewHandlerWithAdmin adds the bootstrap administration endpoint while
// retaining the transparent gateway routes. The repository is intentionally
// accepted through its domain API rather than a database handle.
func NewHandlerWithAdmin(upstreamClient *http.Client, upstreamBaseURL, upstreamAPIKey, adminCredential, authPepper string, repository apiKeyRepository, tokenModes ...auth.TokenMode) (http.Handler, error) {
	return NewHandlerWithAdminAndCompletionLogger(upstreamClient, upstreamBaseURL, upstreamAPIKey, adminCredential, authPepper, repository, nil, tokenModes...)
}

// NewHandlerWithAdminAndCompletionLogger builds a handler with admin key
// creation and the caller-owned completion logger.
func NewHandlerWithAdminAndCompletionLogger(upstreamClient *http.Client, upstreamBaseURL, upstreamAPIKey, adminCredential, authPepper string, repository apiKeyRepository, completionLogger *CompletionLogger, tokenModes ...auth.TokenMode) (http.Handler, error) {
	return NewHandlerWithAdminAndRequestLimiter(upstreamClient, upstreamBaseURL, upstreamAPIKey, adminCredential, authPepper, repository, limiter.NewRequestLimiter(nil), completionLogger, tokenModes...)
}

// NewHandlerWithAdminAndRequestLimiter builds the administration and public
// routes with an explicitly owned request limiter. This form keeps the clock
// injectable for embedders and HTTP tests while the convenience constructors
// use the wall clock.
func NewHandlerWithAdminAndRequestLimiter(upstreamClient *http.Client, upstreamBaseURL, upstreamAPIKey, adminCredential, authPepper string, repository apiKeyRepository, requestLimiter *limiter.RequestLimiter, completionLogger *CompletionLogger, tokenModes ...auth.TokenMode) (http.Handler, error) {
	return NewHandlerWithAdminAndLimiters(upstreamClient, upstreamBaseURL, upstreamAPIKey, adminCredential, authPepper, repository, requestLimiter, limiter.NewConcurrencyLimiter(), completionLogger, tokenModes...)
}

// NewHandlerWithAdminAndLimiters builds the administration and public routes
// with explicitly owned request-window and concurrency limiters. Both
// limiters are process-local and may be shared by handlers when an application
// needs one policy domain across more than one listener.
func NewHandlerWithAdminAndLimiters(upstreamClient *http.Client, upstreamBaseURL, upstreamAPIKey, adminCredential, authPepper string, repository apiKeyRepository, requestLimiter *limiter.RequestLimiter, concurrencyLimiter *limiter.ConcurrencyLimiter, completionLogger *CompletionLogger, tokenModes ...auth.TokenMode) (http.Handler, error) {
	if requestLimiter == nil {
		requestLimiter = limiter.NewRequestLimiter(nil)
	}
	if concurrencyLimiter == nil {
		concurrencyLimiter = limiter.NewConcurrencyLimiter()
	}
	proxy := newProxyHandlerWithLimiters(upstreamClient, upstreamBaseURL, upstreamAPIKey, requestLimiter, concurrencyLimiter)
	service, err := newAdminKeyService(repository, []byte(authPepper), tokenModes...)
	if err != nil {
		return nil, err
	}
	admin := &adminHandler{credential: adminCredential, service: service}
	router := routeWithAdmin(proxy, admin, service.auth)
	return newHandlerWithCompletionLogger(completionLogger, router), nil
}

// NewHandlerWithAuthenticator builds a handler whose public /v1/* routes
// require a gateway bearer key. The authenticator is expected to be populated
// before serving requests and can be atomically refreshed by its owner.
func NewHandlerWithAuthenticator(upstreamClient *http.Client, upstreamBaseURL, upstreamAPIKey string, authenticator *auth.Authenticator, completionLogger *CompletionLogger) http.Handler {
	return NewHandlerWithAuthenticatorAndRequestLimiter(upstreamClient, upstreamBaseURL, upstreamAPIKey, authenticator, limiter.NewRequestLimiter(nil), completionLogger)
}

// NewHandlerWithAuthenticatorAndRequestLimiter builds an authenticated public
// handler with the supplied process-local request limiter. A nil limiter keeps
// the existing unrestricted behavior and is useful for callers that do not
// configure request windows.
func NewHandlerWithAuthenticatorAndRequestLimiter(upstreamClient *http.Client, upstreamBaseURL, upstreamAPIKey string, authenticator *auth.Authenticator, requestLimiter *limiter.RequestLimiter, completionLogger *CompletionLogger) http.Handler {
	return NewHandlerWithAuthenticatorAndLimiters(upstreamClient, upstreamBaseURL, upstreamAPIKey, authenticator, requestLimiter, limiter.NewConcurrencyLimiter(), completionLogger)
}

// NewHandlerWithAuthenticatorAndLimiters builds an authenticated public
// handler with explicitly owned request-window and concurrency limiters.
func NewHandlerWithAuthenticatorAndLimiters(upstreamClient *http.Client, upstreamBaseURL, upstreamAPIKey string, authenticator *auth.Authenticator, requestLimiter *limiter.RequestLimiter, concurrencyLimiter *limiter.ConcurrencyLimiter, completionLogger *CompletionLogger) http.Handler {
	if requestLimiter == nil {
		requestLimiter = limiter.NewRequestLimiter(nil)
	}
	if concurrencyLimiter == nil {
		concurrencyLimiter = limiter.NewConcurrencyLimiter()
	}
	proxy := newProxyHandlerWithLimiters(upstreamClient, upstreamBaseURL, upstreamAPIKey, requestLimiter, concurrencyLimiter)
	router := routeWithAuthenticator(proxy, nil, authenticator)
	return newHandlerWithCompletionLogger(completionLogger, router)
}

// NewHandlerWithCompletionLogger builds a handler using the caller-owned
// completion logger. The process entry point should shut that logger down.
func NewHandlerWithCompletionLogger(upstreamClient *http.Client, upstreamBaseURL, upstreamAPIKey string, completionLogger *CompletionLogger, authenticators ...*auth.Authenticator) http.Handler {
	proxy := newProxyHandlerWithLimiters(upstreamClient, upstreamBaseURL, upstreamAPIKey, limiter.NewRequestLimiter(nil), limiter.NewConcurrencyLimiter())
	router := routeWithAdmin(proxy, nil, authenticators...)
	return newHandlerWithCompletionLogger(completionLogger, router)
}

func route(proxy http.Handler) http.Handler {
	// This package-private route is retained for focused transport tests that
	// exercise proxy behavior without constructing gateway credentials. Exported
	// constructors never use it; public constructors always pass an
	// authenticator (or fail closed when one is absent).
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/health":
			health(response, request)
		case strings.HasPrefix(request.URL.Path, "/v1/"):
			proxy.ServeHTTP(response, request)
		default:
			http.NotFound(response, request)
		}
	})
}

func routeWithAdmin(proxy http.Handler, admin http.Handler, authenticators ...*auth.Authenticator) http.Handler {
	var authenticator *auth.Authenticator
	if len(authenticators) != 0 {
		authenticator = authenticators[0]
	}
	return routeWithAuthenticator(proxy, admin, authenticator)
}

func routeWithAuthenticator(proxy http.Handler, admin http.Handler, authenticator *auth.Authenticator) http.Handler {
	publicV1 := withGatewayAuthentication(authenticator, proxy)
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/health":
			health(response, request)
		case admin != nil && strings.HasPrefix(request.URL.Path, "/admin/"):
			admin.ServeHTTP(response, request)
		case strings.HasPrefix(request.URL.Path, "/v1/"):
			publicV1.ServeHTTP(response, request)
		default:
			http.NotFound(response, request)
		}
	})
}

type proxyHandler struct {
	client             *http.Client
	baseURL            *url.URL
	apiKey             string
	requestLimiter     *limiter.RequestLimiter
	concurrencyLimiter *limiter.ConcurrencyLimiter
	responseDispatch   responseDispatchFunc
}

type responseDispatchFunc func(http.ResponseWriter, *http.Response, *openai.RequestMetadata)

const (
	// These limits apply only to the explicit stream:false/SSE compatibility
	// path. Transparent responses never use them or buffer their body.
	aggregationMaxEventSize   = 64 * 1024
	aggregationMaxPayloadSize = 4 * 1024 * 1024
	// This bounds the complete decoded representation, including framing and
	// any bytes after [DONE] that are consumed to validate content codings.
	// Keep it above the accumulated payload limit so a normal 4 MiB result can
	// still be represented without making the decoder an unbounded work sink.
	aggregationMaxDecodedBytes int64 = 8 * 1024 * 1024
)

var errDecodedRepresentationTooLarge = errors.New("decoded representation exceeds limit")

func newProxyHandler(client *http.Client, baseURL, apiKey string) *proxyHandler {
	return newProxyHandlerWithRequestLimiter(client, baseURL, apiKey, nil)
}

func newProxyHandlerWithRequestLimiter(client *http.Client, baseURL, apiKey string, requestLimiter *limiter.RequestLimiter) *proxyHandler {
	return newProxyHandlerWithLimiters(client, baseURL, apiKey, requestLimiter, limiter.NewConcurrencyLimiter())
}

func newProxyHandlerWithLimiters(client *http.Client, baseURL, apiKey string, requestLimiter *limiter.RequestLimiter, concurrencyLimiter *limiter.ConcurrencyLimiter) *proxyHandler {
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return &proxyHandler{client: client, apiKey: apiKey, requestLimiter: requestLimiter, concurrencyLimiter: concurrencyLimiter, responseDispatch: func(response http.ResponseWriter, _ *http.Response, _ *openai.RequestMetadata) {
			writeGatewayError(response, gatewayErrorInternal, "")
		}}
	}

	// A nil responseDispatch selects the built-in dispatcher. Keeping the
	// injectable legacy-shaped callback available is useful to transport tests
	// and avoids making request classification part of that callback's API.
	return &proxyHandler{client: client, baseURL: parsedURL, apiKey: apiKey, requestLimiter: requestLimiter, concurrencyLimiter: concurrencyLimiter}
}

func (handler *proxyHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if handler.baseURL == nil {
		writeGatewayError(response, gatewayErrorInternal, "")
		return
	}

	targetURL := *handler.baseURL
	targetURL.Path, targetURL.RawPath = joinURLPath(handler.baseURL, request.URL)
	targetURL.RawQuery = request.URL.RawQuery
	targetURL.Fragment = ""

	principal, authenticated := PrincipalFromContext(request.Context())
	var inspectionLease *limiter.Lease
	var concurrencyLease *limiter.Lease
	var upstreamResponse *http.Response
	proxyContext, cancelUpstream := context.WithCancel(request.Context())
	// Restricted model inspection may block while reading a client body. Reserve
	// a slot for that phase, but do not retain it as the full-lifecycle lease:
	// inspection must finish and release its provisional reservation before the
	// request window is consumed. The lifecycle lease is acquired only after
	// that admission, so a rate-rejected request never retains a slot.
	defer func() {
		cancelUpstream()
		if upstreamResponse != nil && upstreamResponse.Body != nil {
			_ = upstreamResponse.Body.Close()
		}
		if request.Body != nil {
			_ = request.Body.Close()
		}
		inspectionLease.Release()
		concurrencyLease.Release()
	}()
	if authenticated && handler.concurrencyLimiter != nil && shouldInspectRequestMetadata(request) {
		inspectionLease, _ = handler.concurrencyLimiter.Acquire(principal.ID, principal.Policy.MaxConcurrency())
		if inspectionLease == nil {
			writeGatewayError(response, gatewayErrorConcurrencyLimit, "")
			return
		}
	}

	requestBody, metadata := request.Body, (*openai.RequestMetadata)(nil)
	if shouldInspectRequestMetadata(request) {
		requestBody, metadata = inspectChatRequest(request)
	}
	// End the potentially blocking inspection phase before evaluating the model
	// decision and, importantly, before request-window admission. A later
	// lifecycle acquisition may race with another inspection reservation and
	// reject; consumed request capacity is intentionally not refunded.
	inspectionLease.Release()
	inspectionLease = nil
	if metadata != nil && metadata.Model != "" {
		if authenticated && !principal.Policy.AllowsModel(metadata.Model) {
			writeGatewayError(response, gatewayErrorModelNotAllowed, "model")
			return
		}
	}
	if handler.requestLimiter != nil {
		if authenticated {
			windows := principal.Policy.RequestWindows()
			if len(windows) != 0 {
				allowed, resetAt := handler.requestLimiter.Allow(principal.ID, windows)
				if !allowed {
					writeGatewayErrorRetryAfter(response, gatewayErrorRequestLimit, "", handler.requestLimiter.RetryAfterSeconds(resetAt))
					return
				}
			}
		}
	}
	if handler.concurrencyLimiter != nil {
		if authenticated {
			concurrencyLease, _ = handler.concurrencyLimiter.Acquire(principal.ID, principal.Policy.MaxConcurrency())
			if concurrencyLease == nil {
				writeGatewayError(response, gatewayErrorConcurrencyLimit, "")
				return
			}
		}
	}
	upstreamRequest, err := http.NewRequestWithContext(proxyContext, request.Method, targetURL.String(), requestBody)
	if err != nil {
		writeGatewayError(response, gatewayErrorInternal, "")
		return
	}
	upstreamRequest.ContentLength = request.ContentLength
	copyEndToEndHeaders(upstreamRequest.Header, request.Header)
	upstreamRequest.Header.Set("Authorization", "Bearer "+handler.apiKey)

	upstreamResponse, err = handler.client.Do(upstreamRequest)
	if err != nil {
		writeGatewayError(response, gatewayErrorUpstreamConnection, "")
		return
	}
	if handler.responseDispatch != nil {
		handler.responseDispatch(response, upstreamResponse, metadata)
		return
	}
	dispatchResponse(response, upstreamResponse, metadata, request.WithContext(proxyContext))
}

func shouldInspectRequestMetadata(request *http.Request) bool {
	if request.Method != http.MethodPost || request.URL.Path != "/v1/chat/completions" || !isJSONMediaType(request.Header) {
		return false
	}
	principal, authenticated := PrincipalFromContext(request.Context())
	if !authenticated {
		return true
	}
	// An unrestricted policy has no model decision to make. Avoid touching the
	// body in that common case, in particular before a request-count rejection.
	return len(principal.Policy.AllowedModels()) != 0 || len(principal.Policy.DeniedModels()) != 0
}

func inspectChatRequest(request *http.Request) (io.ReadCloser, *openai.RequestMetadata) {
	if request.Method != http.MethodPost || request.URL.Path != "/v1/chat/completions" || !isJSONMediaType(request.Header) {
		return request.Body, nil
	}
	if request.Body == nil || request.Body == http.NoBody || request.ContentLength == 0 {
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

// dispatchResponse forwards an upstream response. The optional request is used
// for the one compatibility transformation; omitting it retains the original
// direct-call behavior for callers that only need transparent dispatch.
func dispatchResponse(response http.ResponseWriter, upstreamResponse *http.Response, metadata *openai.RequestMetadata, requests ...*http.Request) {
	var request *http.Request
	if len(requests) != 0 {
		request = requests[0]
	}
	responseMode := classifyResponseHeader(upstreamResponse.Header)
	if shouldAggregateSSE(request, metadata, responseMode) {
		aggregationBody, closeAggregationBody, requiresDrain, err := aggregationReaderWithDrain(upstreamResponse)
		if err != nil {
			// A response transformation cannot safely preserve an unsupported or
			// malformed representation. Fail before copying upstream headers or
			// committing any downstream bytes.
			writeGatewayError(response, gatewayErrorUpstreamConnection, "")
			return
		}
		if closeAggregationBody != nil {
			defer closeAggregationBody()
		}
		body, done, err := openai.AggregateSSEToJSONWithTermination(aggregationBody, aggregationMaxEventSize, aggregationMaxPayloadSize)
		if err != nil {
			// Aggregation happens before any downstream headers or body bytes are
			// committed. Deliberately expose no upstream body or parser detail.
			writeGatewayError(response, gatewayErrorUpstreamConnection, "")
			return
		}
		// AggregateSSEToJSON intentionally stops reading at [DONE]. Encodings
		// with trailers still need a bounded drain to validate them; an identity
		// representation has nothing left to validate and must not wait for EOF.
		if !done || requiresDrain {
			if err := drainAggregationBody(aggregationBody, aggregationContext(upstreamResponse, request)); err != nil {
				writeGatewayError(response, gatewayErrorUpstreamConnection, "")
				return
			}
		} else {
			// There is no decoder trailer to validate for an identity body. Close
			// it before committing the generated response so a handler blocked
			// after DONE observes cancellation and the transport cannot reuse the
			// incomplete connection.
			_ = upstreamResponse.Body.Close()
		}
		copyTransformedResponseHeaders(response.Header(), upstreamResponse.Header)
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Content-Length", strconv.Itoa(len(body)))
		response.WriteHeader(upstreamResponse.StatusCode)
		_, _ = response.Write(body)
		return
	}

	copyResponseHeaders(response.Header(), upstreamResponse.Header)
	response.WriteHeader(upstreamResponse.StatusCode)
	if responseMode == ResponseModeSSE {
		_ = streamResponseBody(response, upstreamResponse.Body)
		return
	}
	_, _ = io.Copy(response, upstreamResponse.Body)
}

// aggregationReader returns the decoded representation needed by the bounded
// SSE converter. Transparent dispatch deliberately does not call this helper:
// compressed responses remain byte- and header-preserving in that mode.
func aggregationReader(upstreamResponse *http.Response) (io.Reader, func(), error) {
	reader, closeReader, _, err := aggregationReaderWithDrain(upstreamResponse)
	return reader, closeReader, err
}

func aggregationReaderWithDrain(upstreamResponse *http.Response) (io.Reader, func(), bool, error) {
	if upstreamResponse == nil || upstreamResponse.Body == nil {
		return nil, nil, false, io.ErrUnexpectedEOF
	}

	var codings []string
	for _, value := range upstreamResponse.Header.Values("Content-Encoding") {
		for _, part := range strings.Split(value, ",") {
			coding := strings.TrimSpace(part)
			if coding == "" {
				return nil, nil, false, errors.New("invalid content encoding")
			}
			codings = append(codings, coding)
		}
	}
	if len(codings) == 0 {
		return &decodedRepresentationReader{reader: upstreamResponse.Body, remaining: aggregationMaxDecodedBytes}, nil, false, nil
	}

	reader := io.Reader(upstreamResponse.Body)
	closers := make([]io.Closer, 0, len(codings))
	requiresDrain := false
	for index := len(codings) - 1; index >= 0; index-- {
		if strings.EqualFold(codings[index], "identity") {
			continue
		}
		requiresDrain = true
		if !strings.EqualFold(codings[index], "gzip") {
			closeReaders(closers)
			return nil, nil, false, errors.New("unsupported content encoding")
		}
		decoded, err := gzip.NewReader(reader)
		if err != nil {
			closeReaders(closers)
			return nil, nil, false, errors.New("invalid gzip content encoding")
		}
		reader = decoded
		closers = append(closers, decoded)
	}

	return &decodedRepresentationReader{reader: reader, remaining: aggregationMaxDecodedBytes}, func() { closeReaders(closers) }, requiresDrain, nil
}

// decodedRepresentationReader bounds bytes after content decoding rather than
// compressed wire bytes. The one-byte probe after the exact limit distinguishes
// an exact-size clean EOF from a representation that has more decoded data.
type decodedRepresentationReader struct {
	reader    io.Reader
	remaining int64
}

func (reader *decodedRepresentationReader) Read(destination []byte) (int, error) {
	if len(destination) == 0 {
		return 0, nil
	}
	if reader.remaining == 0 {
		var probe [1]byte
		read, err := reader.reader.Read(probe[:])
		if read > 0 {
			return 0, errDecodedRepresentationTooLarge
		}
		return 0, err
	}
	if int64(len(destination)) > reader.remaining {
		destination = destination[:reader.remaining]
	}
	read, err := reader.reader.Read(destination)
	if read > 0 {
		reader.remaining -= int64(read)
	}
	return read, err
}

func drainAggregationBody(body io.Reader, context context.Context) error {
	buffer := make([]byte, 32*1024)
	for {
		if err := context.Err(); err != nil {
			return err
		}
		read, err := body.Read(buffer)
		if read == 0 && err == nil {
			return io.ErrNoProgress
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func aggregationContext(upstreamResponse *http.Response, request *http.Request) context.Context {
	if request != nil {
		return request.Context()
	}
	if upstreamResponse != nil && upstreamResponse.Request != nil {
		return upstreamResponse.Request.Context()
	}
	return context.Background()
}

func closeReaders(closers []io.Closer) {
	for index := len(closers) - 1; index >= 0; index-- {
		_ = closers[index].Close()
	}
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

// These headers describe the upstream representation, which is discarded when
// an SSE response is replaced with generated JSON. Other end-to-end headers
// remain safe to forward through the conversion.
var transformedRepresentationHeaders = []string{
	"Content-Type",
	"Content-Encoding",
	"Content-Length",
	"Content-Range",
	"Accept-Ranges",
	"ETag",
	"Content-MD5",
	"Digest",
	"Content-Digest",
	"Last-Modified",
}

func copyTransformedResponseHeaders(destination, source http.Header) {
	copyResponseHeaders(destination, source)
	for _, name := range transformedRepresentationHeaders {
		destination.Del(name)
	}
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

func newHandlerWithCompletionLogger(completionLogger *CompletionLogger, next http.Handler) http.Handler {
	if completionLogger == nil {
		// The convenience constructor does not own a completion logger. In
		// particular, do not fall back to synchronous slog logging: a blocked
		// handler must never delay normal or streaming response completion.
		return withRequestID(next)
	}
	return withRequestID(withCompletionLogger(completionLogger, next))
}

func health(response http.ResponseWriter, request *http.Request) {
	response.WriteHeader(http.StatusOK)
}

func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		id, err := newRequestID()
		if err != nil {
			writeGatewayError(response, gatewayErrorInternal, "")
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

func withCompletionLogger(completionLogger *CompletionLogger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		writer := &completionResponseWriter{ResponseWriter: response}
		next.ServeHTTP(writer, request)

		// Copy only safe scalar values into the record before handing it to the
		// worker. No request object or headers are retained by the logger.
		if completionLogger != nil {
			completionLogger.Enqueue(CompletionRecord{
				RequestID: requestIDFromContext(request.Context()),
				Method:    request.Method,
				Path:      request.URL.Path,
				Status:    writer.statusCode(),
				Duration:  time.Since(startedAt),
			})
		}
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
