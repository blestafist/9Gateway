package httpserver

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/pestit/9gateway/internal/accounting"
	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/config"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/protocol/openai"
)

const requestIDHeader = "X-Gateway-Request-ID"

const requestInspectionLimit int64 = config.DefaultMaxInspectedRequestBytes

// TokenAdmissionConfig contains the bounded, deployment-wide settings used by
// token preflight. The key's effective token mode is already compiled into its
// authentication policy; these values are intentionally not per-key.
type TokenAdmissionConfig struct {
	MaxInspectedRequestBytes   int64
	FallbackUnknownInputTokens int64
	FallbackMaxOutputTokens    int64
	// MaxObservedResponseBytes bounds the wire representation retained for
	// deferred JSON usage observation. Transparent responses are never buffered
	// in full, and the worker applies its own decoded-representation bound.
	MaxObservedResponseBytes int64
	// PricingResolver and BudgetLimiter are process-owned startup dependencies.
	// A zero resolver and nil limiter preserve the legacy unrestricted
	// constructor behavior; budget-governed keys fail closed without both.
	PricingResolver accounting.PricingResolver
	BudgetLimiter   *limiter.BudgetLimiter
}

// Bifrost provenance review: commit 03ab391865710462302bbcf52dca2f32682b91b5
// (branch dev), .references/bifrost/plugins/governance/resolver.go (limit
// evaluation), .references/bifrost/plugins/governance/store.go (CheckBudget),
// and .references/bifrost/plugins/governance/tracker.go (settlement) were
// inspected for HTTP governance boundaries. The reference is Apache-2.0 under
// .references/bifrost/LICENSE; its dependency/license chain was verified in
// .references/bifrost/THIRD_PARTY_NOTICES.md. Nothing was copied or adapted,
// and no dependency or import architecture was added.

func (configuration TokenAdmissionConfig) withDefaults() TokenAdmissionConfig {
	if configuration.MaxInspectedRequestBytes == 0 {
		configuration.MaxInspectedRequestBytes = config.DefaultMaxInspectedRequestBytes
	}
	if configuration.FallbackUnknownInputTokens == 0 {
		configuration.FallbackUnknownInputTokens = config.DefaultFallbackUnknownInputTokens
	}
	if configuration.FallbackMaxOutputTokens == 0 {
		configuration.FallbackMaxOutputTokens = config.DefaultFallbackMaxOutputTokens
	}
	if configuration.MaxObservedResponseBytes <= 0 {
		configuration.MaxObservedResponseBytes = DefaultUsageObservationMaxBytes
	}
	if configuration.MaxObservedResponseBytes > DefaultUsageObservationMaxBytes {
		configuration.MaxObservedResponseBytes = DefaultUsageObservationMaxBytes
	}
	return configuration
}

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
	return NewHandlerWithAdminAndLimitersAndTokenConfig(upstreamClient, upstreamBaseURL, upstreamAPIKey, adminCredential, authPepper, repository, requestLimiter, concurrencyLimiter, completionLogger, TokenAdmissionConfig{}, tokenModes...)
}

// NewHandlerWithAdminAndLimitersAndTokenConfig is the fully configured form of
// the administration/public constructor. Token admission remains process-local
// and shares the supplied clock with the other limiters when its token limiter
// is constructed by the caller.
func NewHandlerWithAdminAndLimitersAndTokenConfig(upstreamClient *http.Client, upstreamBaseURL, upstreamAPIKey, adminCredential, authPepper string, repository apiKeyRepository, requestLimiter *limiter.RequestLimiter, concurrencyLimiter *limiter.ConcurrencyLimiter, completionLogger *CompletionLogger, tokenConfig TokenAdmissionConfig, tokenModes ...auth.TokenMode) (http.Handler, error) {
	return NewHandlerWithAdminAndLimitersAndTokenConfigAndUsageObservationWorker(upstreamClient, upstreamBaseURL, upstreamAPIKey, adminCredential, authPepper, repository, requestLimiter, concurrencyLimiter, completionLogger, tokenConfig, nil, tokenModes...)
}

// NewHandlerWithAdminAndLimitersAndTokenConfigAndUsageObservationWorker is the
// process-wiring form of the admin/public constructor. The supplied worker is
// owned by the caller and is never shut down by the handler.
func NewHandlerWithAdminAndLimitersAndTokenConfigAndUsageObservationWorker(upstreamClient *http.Client, upstreamBaseURL, upstreamAPIKey, adminCredential, authPepper string, repository apiKeyRepository, requestLimiter *limiter.RequestLimiter, concurrencyLimiter *limiter.ConcurrencyLimiter, completionLogger *CompletionLogger, tokenConfig TokenAdmissionConfig, usageWorker *UsageObservationWorker, tokenModes ...auth.TokenMode) (http.Handler, error) {
	return NewHandlerWithAdminAndLimitersAndTokenConfigAndTokenLimiterAndUsageObservationWorker(upstreamClient, upstreamBaseURL, upstreamAPIKey, adminCredential, authPepper, repository, requestLimiter, concurrencyLimiter, completionLogger, nil, tokenConfig, usageWorker, tokenModes...)
}

// NewHandlerWithAdminAndLimitersAndTokenConfigAndTokenLimiterAndUsageObservationWorker
// is the process-wiring form used when startup restores committed token state.
func NewHandlerWithAdminAndLimitersAndTokenConfigAndTokenLimiterAndUsageObservationWorker(upstreamClient *http.Client, upstreamBaseURL, upstreamAPIKey, adminCredential, authPepper string, repository apiKeyRepository, requestLimiter *limiter.RequestLimiter, concurrencyLimiter *limiter.ConcurrencyLimiter, completionLogger *CompletionLogger, tokenLimiter *limiter.TokenLimiter, tokenConfig TokenAdmissionConfig, usageWorker *UsageObservationWorker, tokenModes ...auth.TokenMode) (http.Handler, error) {
	if requestLimiter == nil {
		requestLimiter = limiter.NewRequestLimiter(nil)
	}
	if concurrencyLimiter == nil {
		concurrencyLimiter = limiter.NewConcurrencyLimiter()
	}
	proxy := newProxyHandlerWithLimitersAndTokenLimiter(upstreamClient, upstreamBaseURL, upstreamAPIKey, requestLimiter, concurrencyLimiter, tokenLimiter, tokenConfig)
	proxy.usageObservationWorker = usageWorker
	service, err := newAdminKeyService(repository, []byte(authPepper), tokenModes...)
	if err != nil {
		return nil, err
	}
	if tokenLimiter != nil {
		service.allowTokenPolicyReplacement = func(id string, oldWindows, newWindows []auth.TokenWindow) bool {
			return tokenLimiter.AllowsPolicyReplacement(id, oldWindows, newWindows)
		}
		service.replaceTokenPolicy = func(id string, oldWindows, newWindows []auth.TokenWindow, commit func() error) error {
			if err := tokenLimiter.ReplacePolicy(id, oldWindows, newWindows, commit); errors.Is(err, limiter.ErrTokenPolicyReplacementConflict) {
				return errPolicyConflict
			} else {
				return err
			}
		}
		if records, listErr := repository.List(context.Background()); listErr != nil {
			return nil, errAdminKeyCreation
		} else {
			for _, record := range records {
				policy, parseErr := auth.ParsePolicyJSON([]byte(record.PolicyJSON))
				if parseErr != nil {
					return nil, errAdminKeyCreation
				}
				tokenLimiter.RegisterPolicy(record.ID, policy.TokenWindows())
			}
		}
	}
	if tokenConfig.BudgetLimiter != nil {
		service.allowBudgetPolicyReplacement = func(id string, oldPolicy, newPolicy limiter.BudgetPolicy) bool {
			return tokenConfig.BudgetLimiter.AllowsPolicyReplacement(id, oldPolicy, newPolicy)
		}
		service.replaceBudgetPolicy = func(id string, oldPolicy, newPolicy limiter.BudgetPolicy, commit func() error) error {
			if err := tokenConfig.BudgetLimiter.ReplacePolicy(id, oldPolicy, newPolicy, commit); errors.Is(err, limiter.ErrBudgetPolicyReplacementConflict) {
				return errPolicyConflict
			} else {
				return err
			}
		}
		if records, listErr := repository.List(context.Background()); listErr != nil {
			return nil, errAdminKeyCreation
		} else {
			for _, record := range records {
				policy, parseErr := auth.ParsePolicyJSON([]byte(record.PolicyJSON))
				if parseErr != nil {
					return nil, errAdminKeyCreation
				}
				tokenConfig.BudgetLimiter.RegisterPolicy(record.ID, budgetPolicy(policy))
			}
		}
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
	return NewHandlerWithAuthenticatorAndLimitersAndTokenConfig(upstreamClient, upstreamBaseURL, upstreamAPIKey, authenticator, requestLimiter, concurrencyLimiter, completionLogger, TokenAdmissionConfig{})
}

// NewHandlerWithAuthenticatorAndLimitersAndTokenConfig is the configured form
// of NewHandlerWithAuthenticatorAndLimiters.
func NewHandlerWithAuthenticatorAndLimitersAndTokenConfig(upstreamClient *http.Client, upstreamBaseURL, upstreamAPIKey string, authenticator *auth.Authenticator, requestLimiter *limiter.RequestLimiter, concurrencyLimiter *limiter.ConcurrencyLimiter, completionLogger *CompletionLogger, tokenConfig TokenAdmissionConfig) http.Handler {
	return NewHandlerWithAuthenticatorAndLimitersAndTokenConfigAndUsageObservationWorker(upstreamClient, upstreamBaseURL, upstreamAPIKey, authenticator, requestLimiter, concurrencyLimiter, completionLogger, tokenConfig, nil)
}

// NewHandlerWithAuthenticatorAndLimitersAndTokenConfigAndUsageObservationWorker
// wires a caller-owned usage-observation worker into the public handler.
func NewHandlerWithAuthenticatorAndLimitersAndTokenConfigAndUsageObservationWorker(upstreamClient *http.Client, upstreamBaseURL, upstreamAPIKey string, authenticator *auth.Authenticator, requestLimiter *limiter.RequestLimiter, concurrencyLimiter *limiter.ConcurrencyLimiter, completionLogger *CompletionLogger, tokenConfig TokenAdmissionConfig, usageWorker *UsageObservationWorker) http.Handler {
	return NewHandlerWithAuthenticatorAndLimitersAndTokenLimiterAndUsageObservationWorker(upstreamClient, upstreamBaseURL, upstreamAPIKey, authenticator, requestLimiter, concurrencyLimiter, completionLogger, nil, tokenConfig, usageWorker)
}

// NewHandlerWithAuthenticatorAndLimitersAndTokenLimiter is the test and
// embedding form that supplies the process-owned token limiter (notably for an
// injectable clock). A nil limiter creates the normal wall-clock limiter.
func NewHandlerWithAuthenticatorAndLimitersAndTokenLimiter(upstreamClient *http.Client, upstreamBaseURL, upstreamAPIKey string, authenticator *auth.Authenticator, requestLimiter *limiter.RequestLimiter, concurrencyLimiter *limiter.ConcurrencyLimiter, completionLogger *CompletionLogger, tokenLimiter *limiter.TokenLimiter, tokenConfig TokenAdmissionConfig) http.Handler {
	return NewHandlerWithAuthenticatorAndLimitersAndTokenLimiterAndUsageObservationWorker(upstreamClient, upstreamBaseURL, upstreamAPIKey, authenticator, requestLimiter, concurrencyLimiter, completionLogger, tokenLimiter, tokenConfig, nil)
}

// NewHandlerWithAuthenticatorAndLimitersAndTokenLimiterAndUsageObservationWorker
// is the fully injectable public constructor. The token limiter and usage
// worker are both process-owned by the caller.
func NewHandlerWithAuthenticatorAndLimitersAndTokenLimiterAndUsageObservationWorker(upstreamClient *http.Client, upstreamBaseURL, upstreamAPIKey string, authenticator *auth.Authenticator, requestLimiter *limiter.RequestLimiter, concurrencyLimiter *limiter.ConcurrencyLimiter, completionLogger *CompletionLogger, tokenLimiter *limiter.TokenLimiter, tokenConfig TokenAdmissionConfig, usageWorker *UsageObservationWorker) http.Handler {
	if requestLimiter == nil {
		requestLimiter = limiter.NewRequestLimiter(nil)
	}
	if concurrencyLimiter == nil {
		concurrencyLimiter = limiter.NewConcurrencyLimiter()
	}
	proxy := newProxyHandlerWithLimitersAndTokenLimiter(upstreamClient, upstreamBaseURL, upstreamAPIKey, requestLimiter, concurrencyLimiter, tokenLimiter, tokenConfig)
	proxy.usageObservationWorker = usageWorker
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
			writeGatewayError(response, gatewayErrorNotFound, "")
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
			writeGatewayError(response, gatewayErrorNotFound, "")
		}
	})
}

type proxyHandler struct {
	client                 *http.Client
	baseURL                *url.URL
	apiKey                 string
	requestLimiter         *limiter.RequestLimiter
	concurrencyLimiter     *limiter.ConcurrencyLimiter
	tokenLimiter           *limiter.TokenLimiter
	tokenConfig            TokenAdmissionConfig
	pricingResolver        accounting.PricingResolver
	leaseCoordinator       *limiter.ResourceLeaseCoordinator
	usageObservationWorker *UsageObservationWorker
	responseDispatch       responseDispatchFunc
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
	// Compressed conversion input is bounded independently of its decoded
	// representation. This uses the existing observation-size ceiling rather
	// than adding another public setting; it also covers bytes read while
	// draining gzip trailers and subsequent members after [DONE].
	aggregationMaxWireBytes int64 = DefaultUsageObservationMaxBytes
)

var errDecodedRepresentationTooLarge = errors.New("decoded representation exceeds limit")
var errCompressedWireTooLarge = errors.New("compressed wire representation exceeds limit")
var errUnsupportedResponse = errors.New("unsupported upstream response")

func newProxyHandler(client *http.Client, baseURL, apiKey string) *proxyHandler {
	return newProxyHandlerWithRequestLimiter(client, baseURL, apiKey, nil)
}

func newProxyHandlerWithRequestLimiter(client *http.Client, baseURL, apiKey string, requestLimiter *limiter.RequestLimiter) *proxyHandler {
	return newProxyHandlerWithLimiters(client, baseURL, apiKey, requestLimiter, limiter.NewConcurrencyLimiter())
}

func newProxyHandlerWithLimiters(client *http.Client, baseURL, apiKey string, requestLimiter *limiter.RequestLimiter, concurrencyLimiter *limiter.ConcurrencyLimiter) *proxyHandler {
	return newProxyHandlerWithLimitersAndTokenConfig(client, baseURL, apiKey, requestLimiter, concurrencyLimiter, TokenAdmissionConfig{})
}

func newProxyHandlerWithLimitersAndTokenConfig(client *http.Client, baseURL, apiKey string, requestLimiter *limiter.RequestLimiter, concurrencyLimiter *limiter.ConcurrencyLimiter, tokenConfig TokenAdmissionConfig) *proxyHandler {
	return newProxyHandlerWithLimitersAndTokenLimiter(client, baseURL, apiKey, requestLimiter, concurrencyLimiter, nil, tokenConfig)
}

func newProxyHandlerWithLimitersAndTokenLimiter(client *http.Client, baseURL, apiKey string, requestLimiter *limiter.RequestLimiter, concurrencyLimiter *limiter.ConcurrencyLimiter, tokenLimiter *limiter.TokenLimiter, tokenConfig TokenAdmissionConfig) *proxyHandler {
	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return &proxyHandler{client: client, apiKey: apiKey, requestLimiter: requestLimiter, concurrencyLimiter: concurrencyLimiter, tokenConfig: tokenConfig.withDefaults(), responseDispatch: func(response http.ResponseWriter, _ *http.Response, _ *openai.RequestMetadata) {
			writeGatewayError(response, gatewayErrorInternal, "")
		}}
	}

	// A nil responseDispatch selects the built-in dispatcher. Keeping the
	// injectable legacy-shaped callback available is useful to transport tests
	// and avoids making request classification part of that callback's API.
	tokenConfig = tokenConfig.withDefaults()
	if tokenLimiter == nil {
		tokenLimiter = limiter.NewTokenLimiter(nil)
	}
	return &proxyHandler{client: client, baseURL: parsedURL, apiKey: apiKey, requestLimiter: requestLimiter, concurrencyLimiter: concurrencyLimiter, tokenLimiter: tokenLimiter, tokenConfig: tokenConfig, leaseCoordinator: limiter.NewResourceLeaseCoordinator(concurrencyLimiter, tokenLimiter, tokenConfig.BudgetLimiter), pricingResolver: tokenConfig.PricingResolver}
}

func (handler *proxyHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	terminal := terminalMetadataFromContext(request.Context())
	trace := TraceFromContext(request.Context())
	if handler.baseURL == nil {
		terminal.set(TerminalMetadata{Outcome: TerminalOutcomePreUpstream})
		writeGatewayError(response, gatewayErrorInternal, "")
		return
	}

	targetURL := *handler.baseURL
	targetURL.Path, targetURL.RawPath = joinURLPath(handler.baseURL, request.URL)
	targetURL.RawQuery = request.URL.RawQuery
	targetURL.Fragment = ""

	principal, authenticated := PrincipalFromContext(request.Context())
	if trace != nil && authenticated {
		trace.SetAuthentication(principal.ID, principal.Name)
	}
	if authenticated && handler.tokenConfig.BudgetLimiter != nil && !handler.tokenConfig.BudgetLimiter.PolicyCurrent(principal.ID, budgetPolicy(principal.Policy)) {
		setTerminal := terminalMetadataFromContext(request.Context()).set
		setTerminal(TerminalMetadata{Outcome: TerminalOutcomePreUpstream})
		writeGatewayError(response, gatewayErrorInvalidAPIKey, "")
		return
	}
	var inspectionLease *limiter.Lease
	var upstreamResponse *http.Response
	var lifecycleLease *limiter.ResourceLease
	var tokenAdmission bool
	var budgetAdmission bool
	var responseObservation *responseObservation
	var dispatchErr error
	// client.Do owns the ambiguous boundary. Before it is called, cleanup can
	// prove that no upstream work was possible and release the reservation at
	// zero. Once it starts, even an upload, header, or cancellation error may
	// have reached upstream, so the reservation must be settled conservatively.
	upstreamStarted := false
	setTerminal := func(outcome TerminalOutcome) {
		terminal.set(TerminalMetadata{Outcome: outcome, UpstreamStarted: upstreamStarted})
		if trace != nil {
			if outcome == TerminalOutcomeCancelled {
				trace.setCancellation()
			} else {
				trace.SetTerminalOutcome(outcome)
			}
		}
	}
	proxyContext, cancelUpstream := context.WithCancel(request.Context())
	// Restricted inspection may block while reading a client body. Reserve a
	// provisional slot for that phase and promote it into the full lifecycle
	// lease only after model/request/token admission. A rejected request never
	// retains a lifecycle slot or token reservation.
	defer func() {
		cancelUpstream()
		if upstreamResponse != nil && upstreamResponse.Body != nil {
			_ = upstreamResponse.Body.Close()
		}
		if request.Body != nil {
			_ = request.Body.Close()
		}
		inspectionLease.Release()
		if lifecycleLease != nil {
			switch {
			case !upstreamStarted:
				// Admission and request construction completed, but client.Do
				// was never entered. No upstream work is possible.
				lifecycleLease.ReleaseBeforeUpstream()
			case (tokenAdmission || budgetAdmission) && responseObservation != nil:
				responseObservation.settle(lifecycleLease, handler.usageObservationWorker, budgetAdmission)
			case tokenAdmission || budgetAdmission:
				// No observation can be submitted for an opaque response or an
				// unsupported coding. Settle both resources conservatively here
				// rather than creating an orphaned deferred ticket.
				_ = lifecycleLease.CompleteConservative()
			default:
				_ = lifecycleLease.CompleteConservative()
			}
		}
		if terminal.get().Outcome == TerminalOutcomeUnknown {
			if upstreamStarted {
				setTerminal(TerminalOutcomeComplete)
			} else {
				setTerminal(TerminalOutcomePreUpstream)
			}
		}
	}()
	if authenticated && handler.concurrencyLimiter != nil && shouldInspectRequestMetadata(request) {
		inspectionLease, _ = handler.concurrencyLimiter.Acquire(principal.ID, principal.Policy.MaxConcurrency())
		if inspectionLease == nil {
			setTerminal(TerminalOutcomePreUpstream)
			writeGatewayError(response, gatewayErrorConcurrencyLimit, "")
			return
		}
	}

	requestBody, metadata := request.Body, (*openai.RequestMetadata)(nil)
	var inspected []byte
	var inspectionAvailable bool
	if shouldInspectRequestMetadata(request) {
		requestBody, metadata, inspected, inspectionAvailable = inspectRequest(request, handler.tokenConfig.MaxInspectedRequestBytes)
	}
	if trace != nil {
		mode := RequestModeUnknown
		model := ""
		if metadata != nil {
			model = metadata.Model
			if metadata.Stream != nil {
				mode = RequestModeJSON
				if *metadata.Stream {
					mode = RequestModeSSE
				}
			}
		}
		// ParseRequestMetadata is intentionally the only body metadata source.
		// A malformed/oversized or otherwise unbounded model is represented as
		// unknown, while an independently known boolean stream value is retained.
		if !validTraceText(model, 512) {
			model = ""
		}
		trace.SetRequestMetadataPath(request.Method, boundedEscapedPath(request), ClassifyRoute(request.Method, request.URL.Path), model, mode)
	}
	if metadata != nil && metadata.Model != "" {
		if authenticated && !principal.Policy.AllowsModel(metadata.Model) {
			setTerminal(TerminalOutcomePreUpstream)
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
					setTerminal(TerminalOutcomePreUpstream)
					writeGatewayErrorRetryAfter(response, gatewayErrorRequestLimit, "", handler.requestLimiter.RetryAfterSeconds(resetAt))
					return
				}
			}
		}
	}
	var tokenPlan *accounting.ReservationPlan
	if authenticated && len(principal.Policy.TokenWindows()) != 0 {
		tokenAdmission = true
		var err error
		tokenPlan, err = handler.tokenPlan(request, principal, metadata, inspected, inspectionAvailable)
		if err != nil {
			setTerminal(TerminalOutcomePreUpstream)
			writeGatewayError(response, gatewayErrorInternal, "")
			return
		}
		if tokenPlan == nil {
			tokenAdmission = false
		}
	}
	var budgetPlan *accounting.BudgetReservationPlan
	var selectedPricing accounting.PricingResolution
	if authenticated {
		if total, limited := principal.Policy.TotalBudget(); limited || func() bool { _, day := principal.Policy.DailyBudget(); return day }() || func() bool { _, month := principal.Policy.MonthlyBudget(); return month }() {
			// Lifetime budget admission is intentionally restricted to known
			// generation endpoints. Generic endpoints remain transparent only
			// for keys without a budget policy.
			if request.Method == http.MethodGet && request.URL.Path == "/v1/models" {
				// This endpoint is non-generating and budget-free.
			} else if handler.tokenConfig.BudgetLimiter == nil || !handler.pricingResolver.Present() {
				// A configured budget must never silently become unlimited when a
				// startup dependency is absent. Keep the response deliberately
				// generic so wiring details cannot escape the gateway.
				writeGatewayError(response, gatewayErrorInternal, "")
				setTerminal(TerminalOutcomePreUpstream)
				return
			} else if !eligibleTokenRequest(request) || !inspectionAvailable || metadata == nil {
				writeGatewayError(response, gatewayErrorInvalidRequest, "")
				setTerminal(TerminalOutcomePreUpstream)
				return
			} else {
				if tokenPlan == nil {
					var err error
					tokenPlan, err = handler.tokenPlan(request, principal, metadata, inspected, inspectionAvailable)
					if err != nil || tokenPlan == nil {
						setTerminal(TerminalOutcomePreUpstream)
						writeGatewayError(response, gatewayErrorInvalidRequest, "")
						return
					}
				}
				plan, err := accounting.PlanBudgetReservation(accounting.BudgetReservationOptions{
					Required: true, Model: metadata.Model, MaxModelBytes: handler.tokenConfig.MaxInspectedRequestBytes,
					Reservation: *tokenPlan, Resolver: handler.pricingResolver,
				})
				if err != nil {
					setTerminal(TerminalOutcomePreUpstream)
					writeGatewayError(response, gatewayErrorInvalidRequest, "")
					return
				}
				budgetAdmission = true
				budgetPlan = &plan
				selectedPricing = plan.SelectedPricing
				_ = total // policy values are read again when building lease options
			}
		}
	}
	if authenticated {
		options := limiter.ResourceLeaseOptions{KeyID: principal.ID, MaxConcurrency: principal.Policy.MaxConcurrency()}
		if tokenPlan != nil {
			options.TokenWindows = principal.Policy.TokenWindows()
			options.TokenAmount = tokenPlan.Total.Int64()
		}
		if budgetPlan != nil {
			total, totalLimited := principal.Policy.TotalBudget()
			day, dayLimited := principal.Policy.DailyBudget()
			month, monthLimited := principal.Policy.MonthlyBudget()
			options.BudgetPolicy = limiter.BudgetPolicy{Total: total, Limited: totalLimited, Day: day, DayLimited: dayLimited, Month: month, MonthLimited: monthLimited}
			options.BudgetCandidate = budgetPlan.Reserved()
		}
		var admissionErr *limiter.AdmissionError
		if inspectionLease != nil {
			lifecycleLease, admissionErr = handler.leaseCoordinator.AcquireWithProvisional(inspectionLease, options)
			inspectionLease = nil
		} else {
			lifecycleLease, admissionErr = handler.leaseCoordinator.Acquire(options)
		}
		if admissionErr != nil {
			setTerminal(TerminalOutcomePreUpstream)
			handler.writeAdmissionError(response, admissionErr)
			return
		}
	}
	// A budget replacement may add enforcement to a previously unlimited
	// principal, in which case no BudgetReservation was created above. Recheck
	// immediately before constructing/starting upstream work so a snapshot that
	// became stale during the rest of admission cannot bypass the new policy.
	if authenticated && handler.tokenConfig.BudgetLimiter != nil && !handler.tokenConfig.BudgetLimiter.PolicyCurrent(principal.ID, budgetPolicy(principal.Policy)) {
		setTerminal(TerminalOutcomePreUpstream)
		writeGatewayError(response, gatewayErrorInvalidAPIKey, "")
		return
	}
	upstreamRequest, err := http.NewRequestWithContext(proxyContext, request.Method, targetURL.String(), requestBody)
	if err != nil {
		setTerminal(TerminalOutcomePreUpstream)
		writeGatewayError(response, gatewayErrorInternal, "")
		return
	}
	upstreamRequest.ContentLength = request.ContentLength
	copyEndToEndHeaders(upstreamRequest.Header, request.Header)
	upstreamRequest.Header.Set("Authorization", "Bearer "+handler.apiKey)

	if handler.client == nil {
		setTerminal(TerminalOutcomePreUpstream)
		writeGatewayError(response, gatewayErrorInternal, "")
		return
	}
	upstreamStarted = true
	if trace != nil {
		trace.SetUpstreamStart()
	}
	upstreamResponse, err = handler.client.Do(upstreamRequest)
	if err != nil {
		if proxyContext.Err() != nil {
			setTerminal(TerminalOutcomeCancelled)
			writeGatewayError(response, gatewayErrorCancellation, "")
		} else if timeoutError(err) {
			setTerminal(TerminalOutcomeUpstreamError)
			writeGatewayError(response, gatewayErrorUpstreamTimeout, "")
		} else {
			setTerminal(TerminalOutcomeUpstreamError)
			writeGatewayError(response, gatewayErrorUpstreamConnection, "")
		}
		return
	}
	if upstreamResponse == nil {
		setTerminal(TerminalOutcomeUpstreamError)
		writeGatewayError(response, gatewayErrorUpstreamConnection, "")
		return
	}
	if handler.responseDispatch != nil {
		setTerminal(TerminalOutcomeCustomDispatch)
		handler.responseDispatch(response, upstreamResponse, metadata)
		return
	}
	responseMode := classifyResponseHeader(upstreamResponse.Header)
	if trace != nil {
		trace.SetUpstreamHeaders(upstreamResponse.StatusCode)
		trace.SetUpstreamResponseMode(responseMode)
	}
	if (tokenAdmission || budgetAdmission) && (responseMode == ResponseModeJSON || responseMode == ResponseModeSSE) {
		if coding, err := responseObservationCoding(upstreamResponse.Header); err == nil {
			responseObservation = newResponseObservation(handler.tokenConfig.MaxObservedResponseBytes, coding, selectedPricing)
			if responseMode == ResponseModeJSON {
				response = responseObservation.wrap(response)
			}
		}
	}
	dispatchErr = dispatchResponseResultWithLeaseAndObservationAndPricing(response, upstreamResponse, metadata, lifecycleLease, responseObservation, selectedPricing, request.WithContext(proxyContext))
	if trace != nil && dispatchErr == nil {
		deliveredMode := responseMode
		if shouldAggregateSSE(request, metadata, responseMode) {
			deliveredMode = ResponseModeJSON
		}
		trace.SetDeliveredMode(deliveredMode)
	}
	if responseObservation != nil {
		responseObservation.finish(dispatchErr)
	}
	if dispatchErr != nil {
		if proxyContext.Err() != nil || errors.Is(dispatchErr, context.Canceled) || errors.Is(dispatchErr, context.DeadlineExceeded) {
			setTerminal(TerminalOutcomeCancelled)
		} else if trace.errorCode() == ErrorCodeUnknown {
			if errors.Is(dispatchErr, errDecodedRepresentationTooLarge) || errors.Is(dispatchErr, errCompressedWireTooLarge) {
				if trace != nil {
					trace.SetErrorCode(ErrorCodeConversion)
				}
			} else if trace != nil {
				trace.SetErrorCode(ErrorCodeResponseTransport)
			}
		} else {
			setTerminal(TerminalOutcomeResponseError)
		}
	} else {
		if proxyContext.Err() != nil {
			setTerminal(TerminalOutcomeCancelled)
			return
		}
		if upstreamResponse.StatusCode >= http.StatusBadRequest {
			// Upstream application errors remain byte-for-byte transparent. They
			// are a lifecycle classification, not a gateway-owned error code or
			// a reason to replace the provider's response body.
			setTerminal(TerminalOutcomeUpstreamError)
		} else {
			setTerminal(TerminalOutcomeComplete)
		}
	}
}

func timeoutError(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

func (handler *proxyHandler) writeAdmissionError(response http.ResponseWriter, rejection *limiter.AdmissionError) {
	if rejection != nil && rejection.Resource == limiter.AdmissionTokens {
		retryAfter := 0
		if !rejection.ResetAt.IsZero() {
			retryAfter = handler.tokenLimiter.RetryAfterSeconds(rejection.ResetAt)
		}
		if retryAfter <= 0 {
			retryAfter = 1
		}
		writeGatewayErrorRetryAfter(response, gatewayErrorTokenLimit, "", retryAfter)
		return
	}
	if rejection != nil && rejection.Resource == limiter.AdmissionBudget {
		if rejection.Invalid {
			writeGatewayError(response, gatewayErrorInternal, "")
			return
		}
		retry := 0
		retry = rejection.RetryAfterSeconds
		if retry <= 0 && !rejection.ResetAt.IsZero() {
			retry = limiter.RetryAfterSecondsAt(time.Now().UTC(), rejection.ResetAt)
		}
		if retry > 0 {
			writeGatewayErrorRetryAfter(response, gatewayErrorBudgetLimit, "", retry)
		} else {
			writeGatewayError(response, gatewayErrorBudgetLimit, "")
		}
		return
	}
	writeGatewayError(response, gatewayErrorConcurrencyLimit, "")
}

func (handler *proxyHandler) tokenPlan(request *http.Request, principal auth.Principal, metadata *openai.RequestMetadata, inspected []byte, available bool) (*accounting.ReservationPlan, error) {
	if request.Method == http.MethodGet && request.URL.Path == "/v1/models" {
		return nil, nil
	}
	responses := request.URL.Path == "/v1/responses"
	input := accounting.UnknownInputTokens()
	quality := accounting.EstimateQualityUnknown
	// A nil metadata value with an available inspection is a malformed known
	// request, not an empty request. Do not let the estimator's zero-input result
	// turn malformed output metadata into a smaller-than-conservative reservation.
	if principal.Policy.TokenMode() == auth.TokenModeEstimate && metadata != nil && available && eligibleTokenRequest(request) {
		estimator := accounting.NewApproximateEstimator(handler.tokenConfig.FallbackUnknownInputTokens)
		var err error
		model := ""
		if metadata != nil {
			model = metadata.Model
		}
		input, quality, err = estimator.EstimateInputTokens(model, inspected)
		if err != nil && !errors.Is(err, accounting.ErrEstimateMalformed) {
			// Estimator failures use the configured conservative fallback.
			quality = accounting.EstimateQualityUnknown
		}
	}
	reservationMetadata := accounting.ReservationMetadata{}
	if metadata != nil {
		reservationMetadata = accounting.ReservationMetadata{MaxTokens: metadata.MaxTokens, MaxCompletionTokens: metadata.MaxCompletionTokens, MaxOutputTokens: metadata.MaxOutputTokens}
	}
	plan, err := accounting.PlanReservation(accounting.ReservationOptions{
		Mode:                    accounting.ReservationMode(principal.Policy.TokenMode()),
		UnknownInputFallback:    handler.tokenConfig.FallbackUnknownInputTokens,
		FallbackMaxOutputTokens: handler.tokenConfig.FallbackMaxOutputTokens,
		ResponsesEndpoint:       responses,
		Metadata:                reservationMetadata,
		Input:                   input,
		Quality:                 quality,
	})
	if err != nil {
		if errors.Is(err, accounting.ErrReservationUnavailable) || errors.Is(err, accounting.ErrReservationOverflow) {
			// Unusable request metadata must never disable token admission. A
			// conservative plan is safer than forwarding an unreserved request;
			// in particular this covers negative output limits rejected by the
			// accounting planner.
			fallback, fallbackErr := accounting.PlanReservation(accounting.ReservationOptions{
				Mode:                    accounting.ReservationMode(principal.Policy.TokenMode()),
				UnknownInputFallback:    handler.tokenConfig.FallbackUnknownInputTokens,
				FallbackMaxOutputTokens: handler.tokenConfig.FallbackMaxOutputTokens,
				ResponsesEndpoint:       responses,
				Input:                   accounting.UnknownInputTokens(),
				Quality:                 accounting.EstimateQualityUnknown,
			})
			if fallbackErr != nil {
				return nil, fallbackErr
			}
			return &fallback, nil
		}
		return nil, err
	}
	return &plan, nil
}

func shouldInspectRequestMetadata(request *http.Request) bool {
	if request.Method != http.MethodPost || (request.URL.Path != "/v1/chat/completions" && request.URL.Path != "/v1/responses") || !isJSONMediaType(request.Header) {
		return false
	}
	principal, authenticated := PrincipalFromContext(request.Context())
	if !authenticated {
		return true
	}
	// An unrestricted policy has no model decision to make. Token-window keys
	// still inspect eligible bodies for admission; all other requests remain
	// byte-transparent and are not read solely to discover their size.
	_, budgetLimited := principal.Policy.TotalBudget()
	_, dayLimited := principal.Policy.DailyBudget()
	_, monthLimited := principal.Policy.MonthlyBudget()
	return len(principal.Policy.AllowedModels()) != 0 || len(principal.Policy.DeniedModels()) != 0 || len(principal.Policy.TokenWindows()) != 0 || budgetLimited || dayLimited || monthLimited
}

func eligibleTokenRequest(request *http.Request) bool {
	return request.Method == http.MethodPost && (request.URL.Path == "/v1/chat/completions" || request.URL.Path == "/v1/responses") && isJSONMediaType(request.Header)
}

func inspectRequest(request *http.Request, limit int64) (io.ReadCloser, *openai.RequestMetadata, []byte, bool) {
	if request.Body == nil || request.Body == http.NoBody || request.ContentLength == 0 {
		return request.Body, nil, nil, false
	}
	inspected, replacement, available, _ := openai.InspectRequestBody(request.Body, limit)
	if replacement == nil {
		return request.Body, nil, nil, false
	}
	body := &replayedRequestBody{Reader: replacement, source: request.Body}
	if !available {
		return body, nil, nil, false
	}
	metadata, err := openai.ParseRequestMetadata(inspected)
	if err != nil {
		return body, nil, inspected, true
	}
	return body, &metadata, inspected, true
}

func inspectChatRequest(request *http.Request) (io.ReadCloser, *openai.RequestMetadata) {
	if request.Method != http.MethodPost || request.URL.Path != "/v1/chat/completions" || !isJSONMediaType(request.Header) {
		return request.Body, nil
	}
	body, metadata, _, _ := inspectRequest(request, requestInspectionLimit)
	return body, metadata
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
	_ = dispatchResponseResult(response, upstreamResponse, metadata, requests...)
}

func dispatchResponseResult(response http.ResponseWriter, upstreamResponse *http.Response, metadata *openai.RequestMetadata, requests ...*http.Request) error {
	return dispatchResponseResultWithLease(response, upstreamResponse, metadata, nil, requests...)
}

func dispatchResponseResultWithLease(response http.ResponseWriter, upstreamResponse *http.Response, metadata *openai.RequestMetadata, lease *limiter.ResourceLease, requests ...*http.Request) error {
	return dispatchResponseResultWithLeaseAndObservation(response, upstreamResponse, metadata, lease, nil, requests...)
}

func dispatchResponseResultWithLeaseAndObservation(response http.ResponseWriter, upstreamResponse *http.Response, metadata *openai.RequestMetadata, lease *limiter.ResourceLease, observation *responseObservation, requests ...*http.Request) error {
	return dispatchResponseResultWithLeaseAndObservationAndPricing(response, upstreamResponse, metadata, lease, observation, accounting.UnknownPricingResolution(), requests...)
}

func dispatchResponseResultWithLeaseAndObservationAndPricing(response http.ResponseWriter, upstreamResponse *http.Response, metadata *openai.RequestMetadata, lease *limiter.ResourceLease, observation *responseObservation, pricing accounting.PricingResolution, requests ...*http.Request) error {
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
			writeGatewayError(response, dispatchErrorCode(err), "")
			finalizeConvertedLeaseConservatively(lease)
			return err
		}
		if closeAggregationBody != nil {
			defer closeAggregationBody()
		}
		aggregation, err := openai.AggregateSSEToJSONWithResult(aggregationBody, aggregationMaxEventSize, aggregationMaxPayloadSize)
		if err != nil {
			// Aggregation happens before any downstream headers or body bytes are
			// committed. Deliberately expose no upstream body or parser detail.
			writeGatewayError(response, dispatchErrorCode(err), "")
			if errors.Is(err, errCompressedWireTooLarge) {
				finalizeConvertedLeaseConservatively(lease)
			} else {
				// A conversion error means the generated response was never
				// complete. Partial usage is not sufficient evidence for a
				// synchronous actual-cost outcome.
				finalizeConvertedLeaseConservatively(lease)
			}
			return err
		}
		body, done := aggregation.JSON, aggregation.Done
		// The compatibility decoder has already produced canonical usage. Settle
		// the in-memory lease now, before bounded trailer draining or downstream
		// writes can fail; neither failure invalidates usage already observed.
		settleConvertedLease(lease, aggregation, pricing)
		// AggregateSSEToJSON intentionally stops reading at [DONE]. Encodings
		// with trailers still need a bounded drain to validate them; an identity
		// representation has nothing left to validate and must not wait for EOF.
		if !done || requiresDrain {
			if err := drainAggregationBody(aggregationBody, aggregationContext(upstreamResponse, request)); err != nil {
				writeGatewayError(response, dispatchErrorCode(err), "")
				if errors.Is(err, errCompressedWireTooLarge) {
					finalizeConvertedLeaseConservatively(lease)
				}
				return err
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
		written, writeErr := response.Write(body)
		if writeErr == nil && written != len(body) {
			writeErr = io.ErrShortWrite
		}
		return writeErr
	}

	copyResponseHeaders(response.Header(), upstreamResponse.Header)
	response.WriteHeader(upstreamResponse.StatusCode)
	if upstreamResponse.Body == nil {
		// A RoundTripper is allowed to return a malformed response. It is still
		// post-start ambiguity: preserve the upstream status, report a bounded
		// transport error, and let the request defer conservatively settle the
		// lease rather than panicking while copying a nil body.
		return io.ErrUnexpectedEOF
	}
	if responseMode == ResponseModeSSE {
		return streamResponseBody(response, upstreamResponse.Body, observation)
	}
	_, err := io.Copy(response, upstreamResponse.Body)
	return err
}

func dispatchErrorCode(err error) string {
	if errors.Is(err, errUnsupportedResponse) {
		return gatewayErrorUnsupportedResponse
	}
	return gatewayErrorConversion
}

// finalizeConvertedLeaseConservatively settles a conversion that cannot use a
// valid canonical total. It intentionally performs only the bounded in-memory
// lease operation; no observation worker, parser, logging, channel, or SQL is
// involved.
func finalizeConvertedLeaseConservatively(lease *limiter.ResourceLease) {
	if lease != nil {
		_ = lease.CompleteConservative()
	}
}

// settleConvertedLease commits a canonical total as soon as the compatibility
// decoder has observed one. Downstream write, flush, or bounded-drain errors do
// not erase valid upstream usage that was already observed; without a valid
// total the lease remains conservatively charged.
func settleConvertedLease(lease *limiter.ResourceLease, aggregation openai.AggregationResult, pricing accounting.PricingResolution) {
	if lease == nil {
		return
	}
	if aggregation.Observed && aggregation.Usage.Total().Known() {
		cost, err := accounting.CalculateActualCost(aggregation.Usage, pricing)
		if err != nil {
			cost = accounting.UnknownMoney()
		}
		// CommitKnownUsage independently reconciles the known token total and
		// settles a budget conservatively when differentiated cost is unknown.
		_ = lease.CommitKnownUsage(aggregation.Usage, cost)
		return
	}
	finalizeConvertedLeaseConservatively(lease)
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
				return nil, nil, false, errUnsupportedResponse
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
	compressed := false
	for _, coding := range codings {
		if strings.EqualFold(coding, "gzip") {
			compressed = true
		}
	}
	if compressed {
		reader = &compressedWireReader{reader: reader, remaining: aggregationMaxWireBytes}
	}
	for index := len(codings) - 1; index >= 0; index-- {
		if strings.EqualFold(codings[index], "identity") {
			continue
		}
		requiresDrain = true
		if !strings.EqualFold(codings[index], "gzip") {
			closeReaders(closers)
			return nil, nil, false, errUnsupportedResponse
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

// compressedWireReader bounds bytes consumed from the upstream wire before
// gzip decoding. Its one-byte probe after the exact limit distinguishes an
// exact-size representation from a representation with more compressed bytes.
// Because the same reader remains underneath gzip during the post-[DONE] drain,
// trailer validation and concatenated-member reads are covered as well.
type compressedWireReader struct {
	reader    io.Reader
	remaining int64
}

func (reader *compressedWireReader) Read(destination []byte) (int, error) {
	if len(destination) == 0 {
		return 0, nil
	}
	if reader.remaining == 0 {
		var probe [1]byte
		read, err := reader.reader.Read(probe[:])
		if read > 0 {
			return 0, errCompressedWireTooLarge
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

func streamResponseBody(response http.ResponseWriter, body io.Reader, observations ...*responseObservation) error {
	controller := http.NewResponseController(response)
	buffer := make([]byte, 32*1024)
	var observation *responseObservation
	if len(observations) != 0 {
		observation = observations[0]
	}
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
			// Capture only after both the downstream write and its flush have
			// succeeded. The read buffer is reused on the next iteration, so
			// ownership is copied at this point rather than handed to a worker.
			if observation != nil {
				observation.record(buffer[:read])
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
		trace := NewRequestTraceState(id)
		if trace == nil {
			writeGatewayError(response, gatewayErrorInternal, "")
			return
		}
		terminal := newTerminalMetadataState()
		requestContext := context.WithValue(request.Context(), requestIDContextKey{}, id)
		requestContext = context.WithValue(requestContext, terminalMetadataContextKey{}, terminal)
		request = request.WithContext(withTrace(requestContext, trace))
		// Capture only the bounded route facts available from the request line.
		// EscapedPath deliberately excludes RawQuery, so credentials in queries
		// can never enter the trace.
		trace.SetRouteMetadata(request.Method, boundedEscapedPath(request), ClassifyRoute(request.Method, request.URL.Path))
		writer, wrapped := completionWriter(response, trace, terminal)
		next.ServeHTTP(wrapped, request)
		// The request-ID boundary owns the final trace handoff. This is outside
		// the transport path and remains idempotent when the completion wrapper
		// also completes the trace.
		if terminal := terminalMetadataFromContext(request.Context()); terminal != nil {
			metadata := terminal.get()
			if metadata.Outcome != TerminalOutcomeUnknown {
				trace.SetTerminalMetadata(metadata)
			}
		}
		writer.complete()
	})
}

func withCompletionLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		terminal := newTerminalMetadataState()
		if existing := terminalMetadataFromContext(request.Context()); existing != nil {
			terminal = existing
		}
		request = request.WithContext(context.WithValue(request.Context(), terminalMetadataContextKey{}, terminal))
		writer, wrapped := completionWriter(response, TraceFromContext(request.Context()), terminal)
		next.ServeHTTP(wrapped, request)
		if trace := TraceFromContext(request.Context()); trace != nil {
			trace.SetTerminalMetadata(terminal.get())
			record, err := trace.Complete()
			if err == nil {
				logger.Info("request completed", "request_id", record.RequestID, "method", record.Method,
					"path", record.Path, "route", record.Route.String(), "model", record.Model,
					"requested_mode", record.RequestedMode.String(), "status", writer.statusCode(),
					"duration", time.Since(startedAt), "terminal_outcome", record.Terminal.Outcome,
					"upstream_started", record.Terminal.UpstreamStarted, "error_code", record.ErrorCode.String())
				return
			}
		}
		logger.Info("request completed", "request_id", requestIDFromContext(request.Context()), "method", request.Method,
			"path", boundedEscapedPath(request), "status", writer.statusCode(), "duration", time.Since(startedAt),
			"terminal_outcome", terminal.get().Outcome, "upstream_started", terminal.get().UpstreamStarted,
			"error_code", ErrorCodeUnknown.String())
	})
}

func withCompletionLogger(completionLogger *CompletionLogger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		terminal := newTerminalMetadataState()
		if existing := terminalMetadataFromContext(request.Context()); existing != nil {
			terminal = existing
		}
		request = request.WithContext(context.WithValue(request.Context(), terminalMetadataContextKey{}, terminal))
		writer, wrapped := completionWriter(response, TraceFromContext(request.Context()), terminal)
		next.ServeHTTP(wrapped, request)

		// Copy only safe scalar values into the record before handing it to the
		// worker. No request object or headers are retained by the logger.
		if completionLogger != nil {
			if trace := TraceFromContext(request.Context()); trace != nil {
				trace.SetTerminalMetadata(terminal.get())
				if record, err := trace.Complete(); err == nil {
					completionLogger.Enqueue(record)
					return
				}
			}
			completionLogger.Enqueue(CompletionRecord{RequestID: requestIDFromContext(request.Context()), Method: request.Method,
				Path: boundedEscapedPath(request), Route: ClassifyRoute(request.Method, request.URL.Path), Status: writer.statusCode(),
				Duration: time.Since(startedAt), Terminal: terminal.get(), ErrorCode: ErrorCodeUnknown})
		}
	})
}

func boundedEscapedPath(request *http.Request) string {
	if request == nil || request.URL == nil {
		return ""
	}
	path := request.URL.EscapedPath()
	if !utf8.ValidString(path) || utf8.RuneCountInString(path) > 2048 {
		return ""
	}
	return path
}

// completionWriter reuses the request-bound wrapper when completion logging is
// layered inside the request-ID boundary. A response must have one owner for
// transport bookkeeping: nesting two completionResponseWriters would count
// every downstream write twice while still exposing the same controller.
func completionWriter(response http.ResponseWriter, trace *RequestTraceState, terminal *terminalMetadataState) (*completionResponseWriter, http.ResponseWriter) {
	if writer, ok := response.(*completionResponseWriter); ok {
		return writer, decorateCompletionWriter(writer)
	}
	if existing, ok := response.(interface {
		completionWriter() *completionResponseWriter
	}); ok {
		writer := existing.completionWriter()
		return writer, response
	}
	writer := &completionResponseWriter{ResponseWriter: response, trace: trace, terminal: terminal}
	return writer, decorateCompletionWriter(writer)
}

func decorateCompletionWriter(writer *completionResponseWriter) http.ResponseWriter {
	var wrapped http.ResponseWriter = writer
	_, flushable := writer.ResponseWriter.(http.Flusher)
	_, hijackable := writer.ResponseWriter.(http.Hijacker)
	switch {
	case flushable && hijackable:
		wrapped = &completionResponseWriterFlushHijacker{ResponseWriter: wrapped, core: writer}
	case flushable:
		wrapped = &completionResponseWriterFlusher{ResponseWriter: wrapped, core: writer}
	case hijackable:
		wrapped = &completionResponseWriterHijacker{ResponseWriter: wrapped, core: writer}
	}
	return wrapped
}

func requestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDContextKey{}).(string)
	return id
}

type completionResponseWriter struct {
	http.ResponseWriter
	status   int
	trace    *RequestTraceState
	terminal *terminalMetadataState
}

// Bifrost provenance review: commit 03ab391865710462302bbcf52dca2f32682b91b5,
// .references/bifrost/plugins/logging/writer.go, was inspected for bounded
// observability handoff patterns. Nothing was copied or adapted here; this
// writer remains the sole response wrapper and records only scalar trace facts.

func (writer *completionResponseWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

// Optional response-writer interfaces are exposed only by these conditional
// decorators. The core wrapper remains honest for underlying writers that do
// not support the operation, while Unwrap keeps ResponseController traversal.
type completionResponseWriterFlusher struct {
	http.ResponseWriter
	core *completionResponseWriter
}

func (writer *completionResponseWriterFlusher) completionWriter() *completionResponseWriter {
	return writer.core
}
func (writer *completionResponseWriterFlusher) setGatewayErrorCode(code SafeErrorCode) {
	writer.core.setGatewayErrorCode(code)
}
func (writer *completionResponseWriterFlusher) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}
func (writer *completionResponseWriterFlusher) Flush()            { _ = writer.core.FlushError() }
func (writer *completionResponseWriterFlusher) FlushError() error { return writer.core.FlushError() }

type completionResponseWriterHijacker struct {
	http.ResponseWriter
	core *completionResponseWriter
}

func (writer *completionResponseWriterHijacker) completionWriter() *completionResponseWriter {
	return writer.core
}
func (writer *completionResponseWriterHijacker) setGatewayErrorCode(code SafeErrorCode) {
	writer.core.setGatewayErrorCode(code)
}
func (writer *completionResponseWriterHijacker) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}
func (writer *completionResponseWriterHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := writer.core.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

type completionResponseWriterFlushHijacker struct {
	http.ResponseWriter
	core *completionResponseWriter
}

func (writer *completionResponseWriterFlushHijacker) completionWriter() *completionResponseWriter {
	return writer.core
}
func (writer *completionResponseWriterFlushHijacker) setGatewayErrorCode(code SafeErrorCode) {
	writer.core.setGatewayErrorCode(code)
}
func (writer *completionResponseWriterFlushHijacker) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}
func (writer *completionResponseWriterFlushHijacker) Flush() { _ = writer.core.FlushError() }
func (writer *completionResponseWriterFlushHijacker) FlushError() error {
	return writer.core.FlushError()
}
func (writer *completionResponseWriterFlushHijacker) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := writer.core.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

func (writer *completionResponseWriter) complete() {
	if writer.trace != nil {
		_, _ = writer.trace.Complete()
	}
}

func (writer *completionResponseWriter) setGatewayErrorCode(code SafeErrorCode) {
	if writer.trace != nil && code != ErrorCodeUnknown {
		writer.trace.SetErrorCode(code)
	}
	if recorder, ok := writer.ResponseWriter.(gatewayErrorTraceRecorder); ok {
		recorder.setGatewayErrorCode(code)
	}
}

func (writer *completionResponseWriter) FlushError() error {
	err := http.NewResponseController(writer.ResponseWriter).Flush()
	if err == nil && writer.status == 0 {
		writer.status = http.StatusOK
		if writer.trace != nil {
			writer.trace.SetDownstreamStatus(http.StatusOK)
		}
	}
	return err
}

func (writer *completionResponseWriter) WriteHeader(status int) {
	if writer.status != 0 {
		return
	}
	writer.status = status
	if writer.trace != nil {
		writer.trace.SetDownstreamStatus(status)
	}
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *completionResponseWriter) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	written, err := writer.ResponseWriter.Write(body)
	if writer.trace != nil && written >= 0 && written <= len(body) {
		if written > 0 || err == nil {
			// n is the number accepted by the underlying writer, even when a
			// short write or error accompanies it. Never substitute len(body).
			writer.trace.recordDownstreamWrite(written, err == nil && written == len(body) && written > 0, written == 0 && err == nil)
		}
	}
	return written, err
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
