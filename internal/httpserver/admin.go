package httpserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pestit/9gateway/internal/analytics"
	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/security"
	"github.com/pestit/9gateway/internal/session"
	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/version"
)

const adminRequestBodyLimit int64 = 16 * 1024

var (
	errInvalidAdminRequest = errors.New("invalid admin request")
	errAdminKeyCreation    = errors.New("admin key creation failed")
	errPolicyConflict      = errors.New("policy replacement conflicts with active usage")
)

// apiKeyInserter is the only storage capability needed by key creation. SQL
// remains behind the storage repository, allowing the service to be reused by
// later administrative transports.
type apiKeyInserter interface {
	Insert(context.Context, storage.APIKeyRecord) error
}

type apiKeyLister interface {
	List(context.Context) ([]storage.APIKeyRecord, error)
}

type apiKeyRepository interface {
	apiKeyInserter
	apiKeyLister
}

type apiKeyPageLister interface {
	ListAPIKeys(context.Context, int, string) ([]storage.KeyListRecord, string, error)
}

type apiKeyDetailGetter interface {
	GetAPIKeyByID(context.Context, string) (*storage.KeyDetailRecord, error)
}

type requestPageLister interface {
	ListRequests(context.Context, storage.ListRequestsFilter, int, string) ([]storage.RequestListRecord, string, error)
}

type requestDetailGetter interface {
	GetRequestByID(context.Context, string) (*storage.RequestDetailRecord, error)
}

type requestBodyGetter interface {
	GetRequestBody(context.Context, string, string) (*storage.BodyContent, error)
}

type overviewGetter interface {
	GetOverview(context.Context, time.Time, time.Time, int64) (*storage.OverviewData, error)
}

type usageTimeseriesGetter interface {
	GetUsageTimeseries(context.Context, *time.Time, time.Time, string) (*storage.UsageTimeseriesData, error)
}

type usageBreakdownGetter interface {
	GetUsageBreakdown(context.Context, *time.Time, time.Time, string) (*storage.UsageBreakdownData, error)
}

type apiKeyPolicyUpdater interface {
	UpdatePolicy(context.Context, string, bool, string) error
}

type apiKeyPolicyRecordUpdater interface {
	UpdatePolicyRecord(context.Context, string, bool, string) (storage.APIKeyRecord, error)
}

type gatewayKeyGenerator interface {
	Generate([]byte) (auth.GeneratedGatewayKey, error)
}

type adminKeyService struct {
	repository                   apiKeyRepository
	pepper                       []byte
	generator                    gatewayKeyGenerator
	auth                         *auth.Authenticator
	allowTokenPolicyReplacement  func(string, []auth.TokenWindow, []auth.TokenWindow) bool
	replaceTokenPolicy           func(string, []auth.TokenWindow, []auth.TokenWindow, func() error) error
	allowBudgetPolicyReplacement func(string, limiter.BudgetPolicy, limiter.BudgetPolicy) bool
	replaceBudgetPolicy          func(string, limiter.BudgetPolicy, limiter.BudgetPolicy, func() error) error
	refreshMu                    sync.Mutex
}

func newAdminKeyService(repository apiKeyRepository, pepper []byte, tokenModes ...auth.TokenMode) (*adminKeyService, error) {
	if repository == nil {
		return nil, errAdminKeyCreation
	}
	authenticator, err := auth.NewAuthenticator(pepper, nil, tokenModes...)
	if err != nil {
		return nil, errAdminKeyCreation
	}
	if configured, ok := repository.(interface{ SetTokenMode(auth.TokenMode) }); ok {
		mode := auth.TokenModeEstimate
		if len(tokenModes) == 1 {
			mode = tokenModes[0]
		}
		configured.SetTokenMode(mode)
	}
	service := &adminKeyService{
		repository: repository,
		pepper:     append([]byte(nil), pepper...),
		generator:  auth.NewGatewayKeyGenerator(),
		auth:       authenticator,
	}
	if err := service.loadSnapshot(context.Background()); err != nil {
		return nil, err
	}
	return service, nil
}

func (service *adminKeyService) loadSnapshot(ctx context.Context) error {
	if service.repository == nil || service.auth == nil {
		return errAdminKeyCreation
	}
	records, err := service.repository.List(ctx)
	if err != nil {
		return errAdminKeyCreation
	}
	if err := service.auth.Load(authRecords(records)); err != nil {
		return errAdminKeyCreation
	}
	return nil
}

func authRecords(records []storage.APIKeyRecord) []auth.Record {
	result := make([]auth.Record, 0, len(records))
	for _, record := range records {
		result = append(result, auth.Record{
			ID:            record.ID,
			Name:          record.Name,
			DisplayPrefix: record.DisplayPrefix,
			Digest:        append([]byte(nil), record.Digest...),
			Enabled:       record.Enabled,
			ExpiresAt:     record.ExpiresAt,
			PolicyJSON:    []byte(record.PolicyJSON),
		})
	}
	return result
}

type adminKeyRequest struct {
	Name      string  `json:"name"`
	ExpiresAt *string `json:"expires_at"`
}

type createdAdminKey struct {
	ID        string
	Name      string
	Prefix    string
	Enabled   bool
	Policy    json.RawMessage
	ExpiresAt *time.Time
	CreatedAt time.Time
	RawKey    string
}

type updatedAdminKey struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Prefix    string          `json:"display_prefix"`
	Enabled   bool            `json:"enabled"`
	ExpiresAt *time.Time      `json:"expires_at,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
	Policy    json.RawMessage `json:"policy"`
}

type adminKeyListItem struct {
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	DisplayPrefix string             `json:"display_prefix"`
	Enabled       bool               `json:"enabled"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
	ExpiresAt     *time.Time         `json:"expires_at"`
	PolicySummary adminPolicySummary `json:"policy_summary"`
}

type adminPolicySummary struct {
	AllowModels     bool `json:"allow_models"`
	DenyModels      bool `json:"deny_models"`
	LogRequestBody  bool `json:"log_request_body"`
	LogResponseBody bool `json:"log_response_body"`
}

type adminPolicyWindow struct {
	Amount   int64 `json:"amount"`
	Duration int64 `json:"duration"`
}

type adminRequestPolicyWindow struct {
	Amount   int   `json:"amount"`
	Duration int64 `json:"duration"`
}

type adminBudgetLimit struct {
	Period       string `json:"period"`
	AmountMicros int64  `json:"amount_micros"`
}

type adminKeyPolicy struct {
	AllowedModels         []string                   `json:"allowed_models"`
	DeniedModels          []string                   `json:"denied_models"`
	RequestWindows        []adminRequestPolicyWindow `json:"request_windows"`
	TokenWindows          []adminPolicyWindow        `json:"token_windows"`
	TokenMode             auth.TokenMode             `json:"token_mode"`
	MaxConcurrentRequests int                        `json:"max_concurrent_requests"`
	BudgetLimits          []adminBudgetLimit         `json:"budget_limits"`
	LogRequestBody        bool                       `json:"log_request_body"`
	LogResponseBody       bool                       `json:"log_response_body"`
}

type adminKeyDetailItem struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	DisplayPrefix string         `json:"display_prefix"`
	Enabled       bool           `json:"enabled"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	ExpiresAt     *time.Time     `json:"expires_at"`
	Policy        adminKeyPolicy `json:"policy"`
}

func validAPIKeyID(value string) bool {
	return security.ValidateIdentifier(value)
}

func validAdminID(value string) bool { return security.ValidateIdentifier(value) }

// create makes one persistent key. A small retry budget handles an extremely
// unlikely random identity collision without ever returning a colliding raw
// credential. All errors intentionally lose repository and randomness detail.
func (service *adminKeyService) create(ctx context.Context, name string, expiresAt *time.Time) (createdAdminKey, error) {
	if service == nil || service.repository == nil || service.generator == nil || len(service.pepper) == 0 || service.auth == nil {
		return createdAdminKey{}, errAdminKeyCreation
	}
	if strings.TrimSpace(name) == "" || !validBoundedText(name, 256) {
		return createdAdminKey{}, errInvalidAdminRequest
	}
	if expiresAt != nil {
		expires := expiresAt.UTC().Truncate(time.Second)
		if expires.IsZero() {
			return createdAdminKey{}, errInvalidAdminRequest
		}
		expiresAt = &expires
	}
	service.refreshMu.Lock()
	defer service.refreshMu.Unlock()

	for attempt := 0; attempt < 3; attempt++ {
		generated, err := service.generator.Generate(service.pepper)
		if err != nil {
			return createdAdminKey{}, errAdminKeyCreation
		}
		// Derive the indexed identity from the raw value rather than trusting a
		// generator implementation's duplicate metadata. This also ensures every
		// newly published record uses the exact parser-compatible prefix.
		prefix, err := auth.DisplayPrefix(generated.RawKey)
		if err != nil {
			return createdAdminKey{}, errAdminKeyCreation
		}
		id, err := newAPIKeyID()
		if err != nil {
			return createdAdminKey{}, errAdminKeyCreation
		}
		now := time.Now().UTC().Truncate(time.Second)
		record := storage.APIKeyRecord{
			ID:            id,
			Name:          name,
			DisplayPrefix: prefix,
			Digest:        generated.Digest,
			Enabled:       true,
			ExpiresAt:     expiresAt,
			CreatedAt:     now,
			UpdatedAt:     now,
			PolicyJSON:    `{}`,
		}
		// Prepare the complete replacement before insertion. After insertion,
		// publication is an infallible atomic store and does not depend on the
		// request context or another SQL operation.
		records, err := service.repository.List(ctx)
		if err != nil {
			return createdAdminKey{}, errAdminKeyCreation
		}
		records = append(records, record)
		prepared, err := service.auth.Prepare(authRecords(records))
		if err != nil {
			return createdAdminKey{}, errAdminKeyCreation
		}
		if err := service.repository.Insert(ctx, record); err != nil {
			if errors.Is(err, storage.ErrConflict) {
				continue
			}
			return createdAdminKey{}, errAdminKeyCreation
		}
		service.auth.Publish(prepared)
		return createdAdminKey{
			ID:        id,
			Name:      name,
			Prefix:    prefix,
			Enabled:   true,
			Policy:    json.RawMessage(`{}`),
			ExpiresAt: expiresAt,
			CreatedAt: now,
			RawKey:    generated.RawKey,
		}, nil
	}
	return createdAdminKey{}, errAdminKeyCreation
}

// updatePolicy validates and prepares the complete replacement before the
// durable update. The prepared snapshot is published only after the one-statement
// status/policy update succeeds, so an invalid replacement cannot alter either
// persistent or in-memory state.
func (service *adminKeyService) updatePolicy(ctx context.Context, id string, enabled bool, policyJSON []byte) (updatedAdminKey, error) {
	if service == nil || service.repository == nil || service.auth == nil {
		return updatedAdminKey{}, errAdminKeyCreation
	}
	if strings.TrimSpace(id) == "" {
		return updatedAdminKey{}, storage.ErrNotFound
	}
	if _, err := auth.ParsePolicyJSON(policyJSON); err != nil {
		return updatedAdminKey{}, errInvalidAdminRequest
	}
	updater, ok := service.repository.(apiKeyPolicyUpdater)
	if !ok {
		return updatedAdminKey{}, errAdminKeyCreation
	}

	service.refreshMu.Lock()
	defer service.refreshMu.Unlock()
	records, err := service.repository.List(ctx)
	if err != nil {
		return updatedAdminKey{}, errAdminKeyCreation
	}
	var replacement storage.APIKeyRecord
	found := false
	unchanged := false
	var oldWindows, newWindows []auth.TokenWindow
	var oldBudget, newBudget limiter.BudgetPolicy
	for index := range records {
		if records[index].ID != id {
			continue
		}
		found = true
		oldPolicy, policyErr := auth.ParsePolicyJSON([]byte(records[index].PolicyJSON))
		newPolicy, newPolicyErr := auth.ParsePolicyJSON(policyJSON)
		if policyErr != nil || newPolicyErr != nil {
			return updatedAdminKey{}, errInvalidAdminRequest
		}
		oldWindows, newWindows = oldPolicy.TokenWindows(), newPolicy.TokenWindows()
		oldBudget = budgetPolicy(oldPolicy)
		newBudget = budgetPolicy(newPolicy)
		if service.allowTokenPolicyReplacement != nil && !service.allowTokenPolicyReplacement(id, oldWindows, newWindows) {
			return updatedAdminKey{}, errPolicyConflict
		}
		if service.allowBudgetPolicyReplacement != nil && !service.allowBudgetPolicyReplacement(id, oldBudget, newBudget) {
			return updatedAdminKey{}, errPolicyConflict
		}
		unchanged = records[index].Enabled == enabled && records[index].PolicyJSON == string(policyJSON)
		replacement = records[index]
		replacement.Enabled = enabled
		replacement.PolicyJSON = string(policyJSON)
		records[index] = replacement
		break
	}
	if !found {
		return updatedAdminKey{}, storage.ErrNotFound
	}
	prepared, err := service.auth.Prepare(authRecords(records))
	if err != nil {
		return updatedAdminKey{}, errAdminKeyCreation
	}
	if unchanged {
		// A durable no-op still publishes the prepared equivalent so the request
		// has the same immediate effect even if a prior process refresh lagged.
		service.auth.Publish(prepared)
		return updatedAdminKeyFromRecord(replacement, policyJSON), nil
	}
	var persisted storage.APIKeyRecord
	commit := func() error {
		if recordUpdater, ok := service.repository.(apiKeyPolicyRecordUpdater); ok {
			// The snapshot has already been prepared and the mutation is now the
			// commit point. Finish independently of request cancellation.
			persisted, err = recordUpdater.UpdatePolicyRecord(context.WithoutCancel(ctx), id, enabled, string(policyJSON))
		} else {
			err = updater.UpdatePolicy(context.WithoutCancel(ctx), id, enabled, string(policyJSON))
		}
		if err != nil {
			return err
		}
		return nil
	}
	// Admission acquires token policy before budget policy. Use the same lock
	// order for the nested replacement transaction; reversing it would permit a
	// token/budget admission deadlock during an admin update.
	switch {
	case service.replaceTokenPolicy != nil && service.replaceBudgetPolicy != nil:
		err = service.replaceTokenPolicy(id, oldWindows, newWindows, func() error {
			return service.replaceBudgetPolicy(id, oldBudget, newBudget, commit)
		})
	case service.replaceBudgetPolicy != nil:
		err = service.replaceBudgetPolicy(id, oldBudget, newBudget, commit)
	case service.replaceTokenPolicy != nil:
		err = service.replaceTokenPolicy(id, oldWindows, newWindows, commit)
	default:
		err = commit()
	}
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return updatedAdminKey{}, storage.ErrNotFound
		}
		return updatedAdminKey{}, errAdminKeyCreation
	}
	// Every limiter replacement has now published its runtime identity. Publish
	// the authentication snapshot last, so no request can observe durable JSON
	// and a new principal while one of the admission limiters still carries the
	// previous policy.
	service.auth.Publish(prepared)
	if persisted.ID != "" {
		replacement = persisted
	} else {
		// Compatibility repositories may only expose the legacy update method.
		// The prepared replacement is sufficient for authentication publication;
		// do not perform a cancelable read after a known durable update.
	}
	return updatedAdminKeyFromRecord(replacement, policyJSON), nil
}

func budgetPolicy(policy auth.EffectivePolicy) limiter.BudgetPolicy {
	total, totalSet := policy.TotalBudget()
	day, daySet := policy.DailyBudget()
	month, monthSet := policy.MonthlyBudget()
	return limiter.BudgetPolicy{Total: total, Limited: totalSet, Day: day, DayLimited: daySet, Month: month, MonthLimited: monthSet}
}

func updatedAdminKeyFromRecord(record storage.APIKeyRecord, policyJSON []byte) updatedAdminKey {
	return updatedAdminKey{
		ID: record.ID, Name: record.Name, Prefix: record.DisplayPrefix,
		Enabled: record.Enabled, ExpiresAt: record.ExpiresAt,
		CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
		Policy: append(json.RawMessage(nil), policyJSON...),
	}
}

func newAPIKeyID() (string, error) {
	var random [16]byte
	if _, err := io.ReadFull(rand.Reader, random[:]); err != nil {
		return "", err
	}
	return "key-" + hex.EncodeToString(random[:]), nil
}

func (handler *adminHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	handler.initSessions()
	setSecurityHeaders(response)

	if request.URL.Path == "/admin/ui/v1/session" {
		handler.serveSession(response, request)
		return
	}

	if request.URL.Path == "/admin/v1/system" {
		if request.Method != http.MethodGet {
			writeAdminError(response, http.StatusMethodNotAllowed, gatewayErrorMethodNotAllowed, "method not allowed")
			return
		}
		handler.getSystem(response, request)
		return
	}

	if request.URL.Path == "/admin/v1/overview" {
		if request.Method != http.MethodGet {
			writeAdminError(response, http.StatusMethodNotAllowed, gatewayErrorMethodNotAllowed, "method not allowed")
			return
		}
		handler.getOverview(response, request)
		return
	}

	if request.URL.Path == "/admin/v1/usage/timeseries" {
		if request.Method != http.MethodGet {
			writeAdminError(response, http.StatusMethodNotAllowed, gatewayErrorMethodNotAllowed, "method not allowed")
			return
		}
		handler.getUsageTimeseries(response, request)
		return
	}

	if request.URL.Path == "/admin/v1/usage/breakdown" {
		if request.Method != http.MethodGet {
			writeAdminError(response, http.StatusMethodNotAllowed, gatewayErrorMethodNotAllowed, "method not allowed")
			return
		}
		handler.getUsageBreakdown(response, request)
		return
	}

	if request.Method == http.MethodGet && isRequestBodyPath(request.URL.Path) {
		handler.getRequestBody(response, request)
		return
	}
	if request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/admin/v1/requests/") {
		handler.getRequest(response, request)
		return
	}
	if request.Method == http.MethodGet && request.URL.Path == "/admin/v1/requests" {
		handler.listRequests(response, request)
		return
	}
	if request.Method == http.MethodGet && request.URL.Path == "/admin/v1/keys" {
		handler.listKeys(response, request)
		return
	}
	if request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/admin/v1/keys/") {
		handler.getKey(response, request)
		return
	}
	if request.Method == http.MethodPut && strings.HasPrefix(request.URL.Path, "/admin/v1/keys/") && strings.HasSuffix(request.URL.Path, "/policy") {
		handler.updatePolicy(response, request)
		return
	}
	if request.Method != http.MethodPost || request.URL.Path != "/admin/v1/keys" {
		writeAdminError(response, http.StatusNotFound, gatewayErrorNotFound, "")
		return
	}
	if !handler.authenticate(response, request) {
		return
	}

	body, err := decodeAdminKeyRequest(response, request)
	if err != nil {
		if errors.Is(err, errAdminBodyTooLarge) {
			recordBodyTooLarge(request)
			writeAdminError(response, http.StatusRequestEntityTooLarge, "invalid_request", "request body is too large")
		} else {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid request body")
		}
		return
	}
	expiresAt, err := parseAdminExpiration(body.ExpiresAt)
	if err != nil {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid request body")
		return
	}
	created, err := handler.service.create(request.Context(), body.Name, expiresAt)
	if err != nil {
		if errors.Is(err, errInvalidAdminRequest) {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid request body")
		} else {
			writeAdminError(response, http.StatusInternalServerError, "internal_error", "key creation failed")
		}
		return
	}

	responseBody := struct {
		ID        string          `json:"id"`
		Name      string          `json:"name"`
		Prefix    string          `json:"prefix"`
		Enabled   bool            `json:"enabled"`
		ExpiresAt *time.Time      `json:"expires_at,omitempty"`
		CreatedAt time.Time       `json:"created_at"`
		Key       string          `json:"key"`
		Policy    json.RawMessage `json:"policy"`
	}{created.ID, created.Name, created.Prefix, created.Enabled, created.ExpiresAt, created.CreatedAt, created.RawKey, created.Policy}
	writeAdminJSON(response, http.StatusCreated, responseBody)
}

func isRequestBodyPath(path string) bool {
	const prefix = "/admin/v1/requests/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	return len(parts) >= 2 && parts[1] == "bodies"
}

func (handler *adminHandler) getRequestBody(response http.ResponseWriter, request *http.Request) {
	if !handler.authenticate(response, request) {
		return
	}
	const prefix = "/admin/v1/requests/"
	parts := strings.Split(strings.TrimPrefix(request.URL.Path, prefix), "/")
	if len(parts) != 3 || parts[1] != "bodies" || !security.ValidateRequestID(parts[0]) {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid request id or body kind")
		return
	}
	kind := parts[2]
	if !security.ValidateKind(kind, "client_request", "upstream_request", "response") {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid request id or body kind")
		return
	}
	getter, ok := handler.service.repository.(requestBodyGetter)
	if !ok {
		writeAdminError(response, http.StatusInternalServerError, "internal_error", "request body lookup failed")
		return
	}
	content, err := getter.GetRequestBody(request.Context(), parts[0], kind)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAdminError(response, http.StatusNotFound, gatewayErrorNotFound, "")
		} else {
			writeAdminError(response, http.StatusInternalServerError, "internal_error", "request body lookup failed")
		}
		return
	}
	response.Header().Set("Content-Type", "application/octet-stream")
	response.Header().Set("X-Original-Size", strconv.FormatInt(content.OriginalSize, 10))
	response.Header().Set("X-Truncated", strconv.FormatBool(content.Truncated))
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(content.Bytes)
}

type adminRequestListItem struct {
	RequestID                   string     `json:"request_id"`
	APIKeyID                    *string    `json:"api_key_id"`
	APIKeyName                  *string    `json:"api_key_name"`
	Method                      *string    `json:"method"`
	Path                        *string    `json:"path"`
	Route                       *string    `json:"route"`
	Model                       *string    `json:"model"`
	RequestedMode               *string    `json:"requested_mode"`
	UpstreamMode                *string    `json:"upstream_mode"`
	DeliveredMode               *string    `json:"delivered_mode"`
	DownstreamStatus            *int64     `json:"downstream_status"`
	UpstreamStatus              *int64     `json:"upstream_status"`
	TerminalOutcome             *string    `json:"terminal_outcome"`
	UpstreamStarted             bool       `json:"upstream_started"`
	ErrorCode                   *string    `json:"error_code"`
	ClientBytes                 *int64     `json:"client_bytes"`
	UpstreamBytes               *int64     `json:"upstream_bytes"`
	DeliveredBytes              *int64     `json:"delivered_bytes"`
	InputTokens                 *int64     `json:"input_tokens"`
	OutputTokens                *int64     `json:"output_tokens"`
	TotalTokens                 *int64     `json:"total_tokens"`
	CachedInputTokens           *int64     `json:"cached_input_tokens"`
	ReasoningOutputTokens       *int64     `json:"reasoning_output_tokens"`
	CostMicros                  *int64     `json:"cost_micros"`
	StartedAt                   *time.Time `json:"started_at"`
	UpstreamStartedAt           *time.Time `json:"upstream_started_at"`
	UpstreamHeadersAt           *time.Time `json:"upstream_headers_at"`
	FirstByteAt                 *time.Time `json:"first_byte_at"`
	FinishedAt                  *time.Time `json:"finished_at"`
	TotalMicros                 *int64     `json:"total_micros"`
	TimeToUpstreamHeadersMicros *int64     `json:"time_to_upstream_headers_micros"`
	TimeToFirstByteMicros       *int64     `json:"time_to_first_byte_micros"`
	StreamCloseDelayMicros      *int64     `json:"stream_close_delay_micros"`
}

type adminRequestDetailItem struct {
	adminRequestListItem
	HasBodies []string `json:"has_bodies"`
}

func adminRequestListItemFromRecord(record storage.RequestListRecord) adminRequestListItem {
	text := func(value string) *string {
		if value == "" {
			return nil
		}
		copy := value
		return &copy
	}
	number := func(value storage.OptionalInt64) *int64 {
		if !value.Known {
			return nil
		}
		copy := value.Value
		return &copy
	}
	timestamp := func(value storage.OptionalInt64) *time.Time {
		if !value.Known {
			return nil
		}
		stamp := time.UnixMicro(value.Value).UTC()
		return &stamp
	}
	return adminRequestListItem{
		RequestID: record.RequestID, APIKeyID: text(record.APIKeyID), APIKeyName: text(record.KeyName),
		Method: text(record.Method), Path: text(record.Path), Route: text(record.Route), Model: text(record.Model),
		RequestedMode: text(record.RequestedMode), UpstreamMode: text(record.UpstreamMode), DeliveredMode: text(record.DeliveredMode),
		DownstreamStatus: number(record.DownstreamStatus), UpstreamStatus: number(record.UpstreamStatus), TerminalOutcome: text(record.TerminalOutcome), UpstreamStarted: record.UpstreamStarted, ErrorCode: text(record.ErrorCode),
		ClientBytes: number(record.ClientBytes), UpstreamBytes: number(record.UpstreamBytes), DeliveredBytes: number(record.DeliveredBytes),
		InputTokens: number(record.InputTokens), OutputTokens: number(record.OutputTokens), TotalTokens: number(record.TotalTokens), CachedInputTokens: number(record.CachedInputTokens), ReasoningOutputTokens: number(record.ReasoningOutputTokens), CostMicros: number(record.CostMicros),
		StartedAt: timestamp(record.StartedAt), UpstreamStartedAt: timestamp(record.UpstreamStartedAt), UpstreamHeadersAt: timestamp(record.UpstreamHeadersAt), FirstByteAt: timestamp(record.FirstByteAt), FinishedAt: timestamp(record.FinishedAt),
		TotalMicros: number(record.TotalMicros), TimeToUpstreamHeadersMicros: number(record.TimeToUpstreamHeadersMicros), TimeToFirstByteMicros: number(record.TimeToFirstByteMicros), StreamCloseDelayMicros: number(record.StreamCloseDelayMicros),
	}
}

func (handler *adminHandler) listRequests(response http.ResponseWriter, request *http.Request) {
	if !handler.authenticate(response, request) {
		return
	}
	lister, ok := handler.service.repository.(requestPageLister)
	if !ok {
		writeAdminError(response, http.StatusInternalServerError, "internal_error", "request listing failed")
		return
	}
	query, err := adminQuery(request)
	if err != nil {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid request parameters")
		return
	}
	limit, err := parseAdminListLimit(query)
	if err != nil {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid request parameters")
		return
	}
	cursor, err := singleAdminQueryValue(query, "cursor", false)
	if err != nil || !security.ValidateCursorSyntax(cursor) {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid request parameters")
		return
	}
	keyID, err := singleAdminQueryValue(query, "key_id", false)
	if err != nil || (keyID != "" && !validAdminID(keyID)) {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid request parameters")
		return
	}
	after, err := parseAdminListTime(query, "after")
	if err != nil {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid request parameters")
		return
	}
	before, err := parseAdminListTime(query, "before")
	if err != nil || (after != nil && before != nil && after.After(*before)) {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid request parameters")
		return
	}
	records, nextCursor, err := lister.ListRequests(request.Context(), storage.ListRequestsFilter{KeyID: keyID, After: after, Before: before}, limit, cursor)
	if err != nil {
		if errors.Is(err, storage.ErrCursorExpired) {
			writeAdminError(response, http.StatusBadRequest, gatewayErrorCursorExpired, "")
			return
		}
		if errors.Is(err, storage.ErrInvalidCursor) {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid request parameters")
		} else {
			writeAdminError(response, http.StatusInternalServerError, "internal_error", "request listing failed")
		}
		return
	}
	items := make([]adminRequestListItem, 0, len(records))
	for _, record := range records {
		items = append(items, adminRequestListItemFromRecord(record))
	}
	writeAdminJSON(response, http.StatusOK, struct {
		Requests   []adminRequestListItem `json:"requests"`
		NextCursor string                 `json:"next_cursor,omitempty"`
	}{Requests: items, NextCursor: nextCursor})
}

type adminOverviewAggregate struct {
	Requests           int64  `json:"requests"`
	TotalRequests      int64  `json:"total_requests"`
	SuccessfulRequests int64  `json:"successful_requests"`
	ErrorRequests      int64  `json:"error_requests"`
	RejectedRequests   int64  `json:"rejected_requests"`
	InputTokens        *int64 `json:"input_tokens"`
	CachedInputTokens  *int64 `json:"cached_input_tokens"`
	OutputTokens       *int64 `json:"output_tokens"`
	CostMicros         *int64 `json:"cost_micros"`
}

type adminOverviewKeyCounts struct {
	Total       int64 `json:"total"`
	Enabled     int64 `json:"enabled"`
	TotalKeys   int64 `json:"total_keys"`
	EnabledKeys int64 `json:"enabled_keys"`
}

type adminOverviewResponse struct {
	CurrentRangeStart  time.Time              `json:"current_range_start"`
	CurrentRangeEnd    time.Time              `json:"current_range_end"`
	PreviousRangeStart time.Time              `json:"previous_range_start"`
	PreviousRangeEnd   time.Time              `json:"previous_range_end"`
	DataTimestamp      time.Time              `json:"data_timestamp"`
	Current            adminOverviewAggregate `json:"current"`
	Previous           adminOverviewAggregate `json:"previous"`

	Requests           int64  `json:"requests"`
	TotalRequests      int64  `json:"total_requests"`
	SuccessfulRequests int64  `json:"successful_requests"`
	ErrorRequests      int64  `json:"error_requests"`
	RejectedRequests   int64  `json:"rejected_requests"`
	InputTokens        *int64 `json:"input_tokens"`
	CachedInputTokens  *int64 `json:"cached_input_tokens"`
	OutputTokens       *int64 `json:"output_tokens"`
	CostMicros         *int64 `json:"cost_micros"`

	ActiveRequests int64                  `json:"active_requests"`
	KeyCounts      adminOverviewKeyCounts `json:"key_counts"`
	RecentRequests []adminRequestListItem `json:"recent_requests"`
}

func adminOverviewResponseFromData(data *storage.OverviewData) adminOverviewResponse {
	recent := make([]adminRequestListItem, 0, len(data.RecentRequests))
	for _, rec := range data.RecentRequests {
		recent = append(recent, adminRequestListItemFromRecord(rec))
	}
	return adminOverviewResponse{
		CurrentRangeStart:  data.CurrentRangeStart,
		CurrentRangeEnd:    data.CurrentRangeEnd,
		PreviousRangeStart: data.PreviousRangeStart,
		PreviousRangeEnd:   data.PreviousRangeEnd,
		DataTimestamp:      data.DataTimestamp,
		Current: adminOverviewAggregate{
			Requests:           data.Current.TotalRequests,
			TotalRequests:      data.Current.TotalRequests,
			SuccessfulRequests: data.Current.SuccessfulRequests,
			ErrorRequests:      data.Current.ErrorRequests,
			RejectedRequests:   data.Current.RejectedRequests,
			InputTokens:        data.Current.InputTokens,
			CachedInputTokens:  data.Current.CachedInputTokens,
			OutputTokens:       data.Current.OutputTokens,
			CostMicros:         data.Current.CostMicros,
		},
		Previous: adminOverviewAggregate{
			Requests:           data.Previous.TotalRequests,
			TotalRequests:      data.Previous.TotalRequests,
			SuccessfulRequests: data.Previous.SuccessfulRequests,
			ErrorRequests:      data.Previous.ErrorRequests,
			RejectedRequests:   data.Previous.RejectedRequests,
			InputTokens:        data.Previous.InputTokens,
			CachedInputTokens:  data.Previous.CachedInputTokens,
			OutputTokens:       data.Previous.OutputTokens,
			CostMicros:         data.Previous.CostMicros,
		},
		Requests:           data.Current.TotalRequests,
		TotalRequests:      data.Current.TotalRequests,
		SuccessfulRequests: data.Current.SuccessfulRequests,
		ErrorRequests:      data.Current.ErrorRequests,
		RejectedRequests:   data.Current.RejectedRequests,
		InputTokens:        data.Current.InputTokens,
		CachedInputTokens:  data.Current.CachedInputTokens,
		OutputTokens:       data.Current.OutputTokens,
		CostMicros:         data.Current.CostMicros,
		ActiveRequests:     data.ActiveRequests,
		KeyCounts: adminOverviewKeyCounts{
			Total:       data.KeyCounts.Total,
			Enabled:     data.KeyCounts.Enabled,
			TotalKeys:   data.KeyCounts.Total,
			EnabledKeys: data.KeyCounts.Enabled,
		},
		RecentRequests: recent,
	}
}

var processStartTime = time.Now()

// AdminReadinessSummary contains individual and aggregate readiness check outcomes.
// Semantics: snapshot.
type AdminReadinessSummary struct {
	Ready  bool                      `json:"ready"`
	Checks map[string]readinessCheck `json:"checks"`
}

// AdminStorageSummary contains SQLite connectivity and schema status.
// Semantics:
// - status: snapshot string ("healthy", "degraded", or "unavailable")
// - healthy: snapshot boolean
// - schema_version: nullable snapshot integer (*int, null if closed or unavailable)
// - current_schema_version: snapshot integer (expected schema version)
type AdminStorageSummary struct {
	Status               string `json:"status"`
	Healthy              bool   `json:"healthy"`
	SchemaVersion        *int   `json:"schema_version"`
	CurrentSchemaVersion int    `json:"current_schema_version"`
}

// AdminTelemetrySummary contains telemetry queue pressure and drop counters.
// Semantics:
// - queue_depth: snapshot gauge integer
// - queue_capacity: snapshot integer
// - dropped_records: monotonic counter int64
type AdminTelemetrySummary struct {
	QueueDepth     int   `json:"queue_depth"`
	QueueCapacity  int   `json:"queue_capacity"`
	DroppedRecords int64 `json:"dropped_records"`
}

// AdminSystemLimits contains safe operational limits.
// Semantics: snapshot.
type AdminSystemLimits struct {
	RequestRetentionSeconds int64 `json:"request_retention_seconds"`
	BodyRetentionSeconds    int64 `json:"body_retention_seconds"`
	MaxCapturedBodyBytes    int64 `json:"max_captured_body_bytes"`
}

// AdminSystemResponse represents the allowlisted GET /admin/v1/system response payload.
// All fields have typed, stable semantics:
// - version: snapshot string ("dev" or SemVer)
// - commit: snapshot string (7-character commit or "unknown")
// - build_time: snapshot RFC3339 string or "unknown"
// - build_date: snapshot RFC3339 string or "unknown" (alias for build_time)
// - start_time: snapshot RFC3339 timestamp (process startup instant)
// - uptime_seconds: monotonic non-negative int64 (seconds since start_time)
// - ready: snapshot boolean (overall readiness status)
// - readiness: snapshot AdminReadinessSummary
// - storage: snapshot AdminStorageSummary
// - sqlite: snapshot AdminStorageSummary (alias for storage)
// - telemetry: snapshot AdminTelemetrySummary
// - active_requests: snapshot gauge int64 (>= 0)
// - limits: snapshot AdminSystemLimits
type AdminSystemResponse struct {
	Version        string                `json:"version"`
	Commit         string                `json:"commit"`
	BuildTime      string                `json:"build_time"`
	BuildDate      string                `json:"build_date"`
	StartTime      string                `json:"start_time"`
	UptimeSeconds  int64                 `json:"uptime_seconds"`
	Ready          bool                  `json:"ready"`
	Readiness      AdminReadinessSummary `json:"readiness"`
	Storage        AdminStorageSummary   `json:"storage"`
	SQLite         AdminStorageSummary   `json:"sqlite"`
	Telemetry      AdminTelemetrySummary `json:"telemetry"`
	ActiveRequests int64                 `json:"active_requests"`
	Limits         AdminSystemLimits     `json:"limits"`
}

func inspectStorage(ctx context.Context, database *storage.DB) AdminStorageSummary {
	expectedVersion := storage.CurrentSchemaVersion
	if database == nil {
		return AdminStorageSummary{
			Status:               "unavailable",
			Healthy:              false,
			SchemaVersion:        nil,
			CurrentSchemaVersion: expectedVersion,
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	var schemaVersion int
	if err := database.QueryRowContext(ctx, "PRAGMA user_version").Scan(&schemaVersion); err != nil {
		return AdminStorageSummary{
			Status:               "unavailable",
			Healthy:              false,
			SchemaVersion:        nil,
			CurrentSchemaVersion: expectedVersion,
		}
	}

	var ping int
	if err := database.QueryRowContext(ctx, "SELECT 1").Scan(&ping); err != nil || ping != 1 {
		return AdminStorageSummary{
			Status:               "unavailable",
			Healthy:              false,
			SchemaVersion:        &schemaVersion,
			CurrentSchemaVersion: expectedVersion,
		}
	}

	if schemaVersion != expectedVersion {
		return AdminStorageSummary{
			Status:               "degraded",
			Healthy:              false,
			SchemaVersion:        &schemaVersion,
			CurrentSchemaVersion: expectedVersion,
		}
	}

	return AdminStorageSummary{
		Status:               "healthy",
		Healthy:              true,
		SchemaVersion:        &schemaVersion,
		CurrentSchemaVersion: expectedVersion,
	}
}

func (handler *adminHandler) inspectTelemetry(request *http.Request) AdminTelemetrySummary {
	depth := 0
	capacity := handler.telemetryCapacity
	var dropped int64

	if handler.completionLogger != nil {
		depth += len(handler.completionLogger.queue)
		if capacity == 0 {
			capacity = cap(handler.completionLogger.queue)
		}
		dropped += int64(handler.completionLogger.dropped.Load())
	}
	if handler.usageWorker != nil {
		depth += handler.usageWorker.Pending()
		if capacity == 0 {
			capacity = cap(handler.usageWorker.queue)
		}
		dropped += int64(handler.usageWorker.Dropped())
	}
	if handler.historyWorker != nil {
		depth += handler.historyWorker.Pending()
		if capacity == 0 {
			capacity = cap(handler.historyWorker.queue)
		}
		dropped += int64(handler.historyWorker.Dropped())
	}

	if metrics := metricsFromRequest(request); metrics != nil {
		if depth == 0 {
			depth = metrics.queueDepth()
		}
		metricDrops := int64(metrics.telemetry.values[1].Load())
		if metricDrops > dropped {
			dropped = metricDrops
		}
	}
	if handler.metrics != nil {
		if depth == 0 {
			depth = handler.metrics.queueDepth()
		}
		metricDrops := int64(handler.metrics.telemetry.values[1].Load())
		if metricDrops > dropped {
			dropped = metricDrops
		}
	}

	return AdminTelemetrySummary{
		QueueDepth:     depth,
		QueueCapacity:  capacity,
		DroppedRecords: dropped,
	}
}

func (handler *adminHandler) getSystem(response http.ResponseWriter, request *http.Request) {
	if !handler.authenticate(response, request) {
		return
	}
	if request.Context().Err() != nil {
		return
	}
	query, err := adminQuery(request)
	if err != nil {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid request parameters")
		return
	}
	if len(query) > 0 {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "unsupported query parameter")
		return
	}

	ctx, cancel := context.WithTimeout(request.Context(), 2*time.Second)
	defer cancel()

	readiness := readinessFromRequest(request)
	if readiness == nil {
		readiness = handler.readiness
	}

	var readinessRes readinessResult
	var db *storage.DB
	if readiness != nil {
		readinessRes = readiness.Check(ctx)
		db = readiness.Database()
	} else {
		metadata := version.Current()
		readinessRes = readinessResult{
			ready:   false,
			checks:  readinessUnavailableChecks(),
			version: metadata.Version,
			commit:  metadata.Commit,
		}
	}
	if db == nil && handler.service != nil && handler.service.repository != nil {
		if provider, ok := handler.service.repository.(interface{ ReadinessDatabase() *storage.DB }); ok {
			db = provider.ReadinessDatabase()
		}
	}

	storageSummary := inspectStorage(ctx, db)
	if check, ok := readinessRes.checks["sqlite"]; ok && check.Status != "pass" {
		storageSummary.Healthy = false
		if storageSummary.Status == "healthy" {
			storageSummary.Status = "unavailable"
		}
	}
	if check, ok := readinessRes.checks["schema"]; ok && check.Status != "pass" {
		storageSummary.Healthy = false
		if storageSummary.Status == "healthy" {
			storageSummary.Status = "degraded"
		}
	}

	metadata := version.Current()
	versionStr := metadata.Version
	commitStr := metadata.Commit
	buildTimeStr := metadata.BuildDate
	if readinessRes.version != "" && readinessRes.version != "dev" {
		versionStr = readinessRes.version
	}
	if readinessRes.commit != "" && readinessRes.commit != "unknown" {
		commitStr = readinessRes.commit
	}

	startTime := handler.startTime
	if startTime.IsZero() {
		startTime = processStartTime
	}
	uptime := int64(time.Since(startTime).Seconds())
	if uptime < 0 {
		uptime = 0
	}

	var activeRequests int64
	if metrics := metricsFromRequest(request); metrics != nil {
		activeRequests = metrics.active.Load()
		if activeRequests < 0 {
			activeRequests = 0
		}
	}

	telemetrySummary := handler.inspectTelemetry(request)

	limits := handler.systemLimits
	if limits.RequestRetentionSeconds == 0 {
		limits.RequestRetentionSeconds = 2592000
	}
	if limits.BodyRetentionSeconds == 0 {
		limits.BodyRetentionSeconds = 604800
	}

	systemResp := AdminSystemResponse{
		Version:       versionStr,
		Commit:        commitStr,
		BuildTime:     buildTimeStr,
		BuildDate:     buildTimeStr,
		StartTime:     startTime.UTC().Format(time.RFC3339),
		UptimeSeconds: uptime,
		Ready:         readinessRes.ready,
		Readiness: AdminReadinessSummary{
			Ready:  readinessRes.ready,
			Checks: readinessRes.checks,
		},
		Storage:        storageSummary,
		SQLite:         storageSummary,
		Telemetry:      telemetrySummary,
		ActiveRequests: activeRequests,
		Limits:         limits,
	}

	if request.Context().Err() != nil {
		return
	}

	writeAdminJSON(response, http.StatusOK, systemResp)
}

func (handler *adminHandler) getOverview(response http.ResponseWriter, request *http.Request) {
	if !handler.authenticate(response, request) {
		return
	}
	getter, ok := handler.service.repository.(overviewGetter)
	if !ok {
		writeAdminError(response, http.StatusInternalServerError, "internal_error", "overview repository unavailable")
		return
	}
	query, err := adminQuery(request)
	if err != nil {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid request parameters")
		return
	}
	for key := range query {
		if key != "after" && key != "before" {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "unsupported query parameter: "+key)
			return
		}
	}
	afterParam, err := singleAdminQueryValue(query, "after", false)
	if err != nil {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid after parameter")
		return
	}
	beforeParam, err := singleAdminQueryValue(query, "before", false)
	if err != nil {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid before parameter")
		return
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	var after, before time.Time

	if beforeParam == "" {
		before = now
	} else {
		parsedBefore, err := time.Parse(time.RFC3339, beforeParam)
		if err != nil {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid before timestamp; RFC3339 required")
			return
		}
		before = parsedBefore.UTC().Truncate(time.Microsecond)
	}

	if afterParam == "" {
		after = before.Add(-24 * time.Hour)
	} else {
		parsedAfter, err := time.Parse(time.RFC3339, afterParam)
		if err != nil {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid after timestamp; RFC3339 required")
			return
		}
		after = parsedAfter.UTC().Truncate(time.Microsecond)
	}

	if !after.Before(before) {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "after must be before before")
		return
	}
	if before.Sub(after) > 366*24*time.Hour {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "range must not exceed one year")
		return
	}

	var activeRequests int64
	if metrics := metricsFromRequest(request); metrics != nil {
		activeRequests = metrics.active.Load()
		if activeRequests < 0 {
			activeRequests = 0
		}
	}

	cacheKey := fmt.Sprintf("overview:%d:%d", after.UnixMicro(), before.UnixMicro())
	data, err := handler.analyticsCoordinator.Do(request.Context(), cacheKey, func(ctx context.Context) (*storage.OverviewData, error) {
		return getter.GetOverview(ctx, after, before, activeRequests)
	})
	if err != nil {
		if errors.Is(err, analytics.ErrCapacityExceeded) {
			response.Header().Set("Retry-After", "1")
			writeAdminError(response, http.StatusServiceUnavailable, gatewayErrorServiceUnavailable, "analytics capacity exceeded")
			return
		}
		if errors.Is(err, context.Canceled) || request.Context().Err() != nil {
			return
		}
		writeAdminError(response, http.StatusInternalServerError, "internal_error", "overview aggregation failed")
		return
	}

	resp := adminOverviewResponseFromData(data)
	resp.ActiveRequests = activeRequests
	writeAdminJSON(response, http.StatusOK, resp)
}

func (handler *adminHandler) getUsageTimeseries(response http.ResponseWriter, request *http.Request) {
	if !handler.authenticate(response, request) {
		return
	}
	getter, ok := handler.service.repository.(usageTimeseriesGetter)
	if !ok {
		writeAdminError(response, http.StatusInternalServerError, "internal_error", "usage timeseries repository unavailable")
		return
	}
	query, err := adminQuery(request)
	if err != nil {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid request parameters")
		return
	}
	for key := range query {
		if key != "after" && key != "before" && key != "bucket" {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "unsupported query parameter: "+key)
			return
		}
	}
	afterParam, err := singleAdminQueryValue(query, "after", false)
	if err != nil {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid after parameter")
		return
	}
	beforeParam, err := singleAdminQueryValue(query, "before", false)
	if err != nil {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid before parameter")
		return
	}
	bucketParam, err := singleAdminQueryValue(query, "bucket", false)
	if err != nil {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid bucket parameter")
		return
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	var before time.Time
	if beforeParam == "" {
		before = now
	} else {
		parsedBefore, err := time.Parse(time.RFC3339, beforeParam)
		if err != nil {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid before timestamp; RFC3339 required")
			return
		}
		before = parsedBefore.UTC().Truncate(time.Microsecond)
	}

	var reqAfter *time.Time
	if afterParam != "" {
		parsedAfter, err := time.Parse(time.RFC3339, afterParam)
		if err != nil {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid after timestamp; RFC3339 required")
			return
		}
		truncated := parsedAfter.UTC().Truncate(time.Microsecond)
		if !truncated.Before(before) {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "after must be before before")
			return
		}
		reqAfter = &truncated
	}

	switch bucketParam {
	case "", "auto", "five_minutes", "hour", "day", "week", "month":
		// valid
	default:
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid bucket parameter; must be five_minutes, hour, day, week, month, or auto")
		return
	}

	normalizedBucket := bucketParam
	if normalizedBucket == "" {
		normalizedBucket = "auto"
	}

	afterKey := int64(-1)
	if reqAfter != nil {
		afterKey = reqAfter.UnixMicro()
	}
	cacheKey := fmt.Sprintf("timeseries:%d:%d:%s", afterKey, before.UnixMicro(), normalizedBucket)
	data, err := handler.usageTimeseriesCoordinator.Do(request.Context(), cacheKey, func(ctx context.Context) (*storage.UsageTimeseriesData, error) {
		return getter.GetUsageTimeseries(ctx, reqAfter, before, bucketParam)
	})
	if err != nil {
		if errors.Is(err, analytics.ErrCapacityExceeded) {
			response.Header().Set("Retry-After", "1")
			writeAdminError(response, http.StatusServiceUnavailable, gatewayErrorServiceUnavailable, "analytics capacity exceeded")
			return
		}
		if errors.Is(err, storage.ErrOverDetailedRange) {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		if errors.Is(err, storage.ErrInvalidBucket) {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid bucket parameter")
			return
		}
		if errors.Is(err, context.Canceled) || request.Context().Err() != nil {
			return
		}
		writeAdminError(response, http.StatusInternalServerError, "internal_error", "usage timeseries aggregation failed")
		return
	}

	writeAdminJSON(response, http.StatusOK, data)
}

func (handler *adminHandler) getUsageBreakdown(response http.ResponseWriter, request *http.Request) {
	if !handler.authenticate(response, request) {
		return
	}
	getter, ok := handler.service.repository.(usageBreakdownGetter)
	if !ok {
		writeAdminError(response, http.StatusInternalServerError, "internal_error", "usage breakdown repository unavailable")
		return
	}
	query, err := adminQuery(request)
	if err != nil {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid request parameters")
		return
	}
	for key := range query {
		if key != "after" && key != "before" && key != "group_by" {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "unsupported query parameter: "+key)
			return
		}
	}
	afterParam, err := singleAdminQueryValue(query, "after", false)
	if err != nil {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid after parameter")
		return
	}
	beforeParam, err := singleAdminQueryValue(query, "before", false)
	if err != nil {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid before parameter")
		return
	}
	groupBy, err := singleAdminQueryValue(query, "group_by", false)
	if err != nil {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid group_by parameter")
		return
	}
	if groupBy == "" {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "group_by parameter is required; must be model, key, or outcome")
		return
	}
	if groupBy != "model" && groupBy != "key" && groupBy != "outcome" {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid group_by parameter; must be model, key, or outcome")
		return
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	var before time.Time
	if beforeParam == "" {
		before = now
	} else {
		parsedBefore, err := time.Parse(time.RFC3339, beforeParam)
		if err != nil {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid before timestamp; RFC3339 required")
			return
		}
		before = parsedBefore.UTC().Truncate(time.Microsecond)
	}

	var reqAfter *time.Time
	if afterParam != "" {
		parsedAfter, err := time.Parse(time.RFC3339, afterParam)
		if err != nil {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid after timestamp; RFC3339 required")
			return
		}
		truncated := parsedAfter.UTC().Truncate(time.Microsecond)
		if !truncated.Before(before) {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "after must be before before")
			return
		}
		reqAfter = &truncated
	}

	afterKey := int64(-1)
	if reqAfter != nil {
		afterKey = reqAfter.UnixMicro()
	}
	cacheKey := fmt.Sprintf("breakdown:%d:%d:%s", afterKey, before.UnixMicro(), groupBy)
	data, err := handler.usageBreakdownCoordinator.Do(request.Context(), cacheKey, func(ctx context.Context) (*storage.UsageBreakdownData, error) {
		return getter.GetUsageBreakdown(ctx, reqAfter, before, groupBy)
	})
	if err != nil {
		if errors.Is(err, analytics.ErrCapacityExceeded) {
			response.Header().Set("Retry-After", "1")
			writeAdminError(response, http.StatusServiceUnavailable, gatewayErrorServiceUnavailable, "analytics capacity exceeded")
			return
		}
		if errors.Is(err, storage.ErrInvalidGroupBy) {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid group_by parameter")
			return
		}
		if errors.Is(err, context.Canceled) || request.Context().Err() != nil {
			return
		}
		writeAdminError(response, http.StatusInternalServerError, "internal_error", "usage breakdown aggregation failed")
		return
	}

	writeAdminJSON(response, http.StatusOK, data)
}

func (handler *adminHandler) getRequest(response http.ResponseWriter, request *http.Request) {
	if !handler.authenticate(response, request) {
		return
	}
	const prefix = "/admin/v1/requests/"
	id := strings.TrimPrefix(request.URL.Path, prefix)
	if !security.ValidateRequestID(id) {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid request id")
		return
	}
	getter, ok := handler.service.repository.(requestDetailGetter)
	if !ok {
		writeAdminError(response, http.StatusInternalServerError, "internal_error", "request lookup failed")
		return
	}
	record, err := getter.GetRequestByID(request.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAdminError(response, http.StatusNotFound, gatewayErrorNotFound, "")
		} else {
			writeAdminError(response, http.StatusInternalServerError, "internal_error", "request lookup failed")
		}
		return
	}
	writeAdminJSON(response, http.StatusOK, adminRequestDetailItem{
		adminRequestListItem: adminRequestListItemFromRecord(record.RequestListRecord),
		HasBodies:            record.HasBodies,
	})
}

func parseAdminListLimit(query map[string][]string) (int, error) {
	value, err := singleAdminQueryValue(query, "limit", false)
	if err != nil {
		return 0, err
	}
	if value == "" {
		return 50, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 || parsed > 500 {
		return 0, errInvalidAdminRequest
	}
	return parsed, nil
}

func singleAdminQueryValue(query map[string][]string, name string, required bool) (string, error) {
	values, present := query[name]
	if !present {
		if required {
			return "", errInvalidAdminRequest
		}
		return "", nil
	}
	if len(values) != 1 || (!required && values[0] == "") {
		return "", errInvalidAdminRequest
	}
	return values[0], nil
}

func parseAdminListTime(query map[string][]string, name string) (*time.Time, error) {
	value, err := singleAdminQueryValue(query, name, false)
	if err != nil {
		return nil, err
	}
	if value == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, errInvalidAdminRequest
	}
	// Request history timestamps are stored at microsecond precision. Normalize
	// the user-supplied bound to that same precision before comparing bounds and
	// passing it to SQLite.
	parsed = parsed.UTC().Truncate(time.Microsecond)
	return &parsed, nil
}

func (handler *adminHandler) getKey(response http.ResponseWriter, request *http.Request) {
	const prefix = "/admin/v1/keys/"
	if !handler.authenticate(response, request) {
		return
	}
	id := strings.TrimPrefix(request.URL.Path, prefix)
	if !validAPIKeyID(id) {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid key id")
		return
	}
	getter, ok := handler.service.repository.(apiKeyDetailGetter)
	if !ok {
		writeAdminError(response, http.StatusInternalServerError, "internal_error", "key lookup failed")
		return
	}
	record, err := getter.GetAPIKeyByID(request.Context(), id)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeAdminError(response, http.StatusNotFound, gatewayErrorNotFound, "")
		} else {
			writeAdminError(response, http.StatusInternalServerError, "internal_error", "key lookup failed")
		}
		return
	}
	writeAdminJSON(response, http.StatusOK, adminKeyDetailFromRecord(*record))
}

func adminKeyDetailFromRecord(record storage.KeyDetailRecord) adminKeyDetailItem {
	policy := record.Policy
	requestWindows := policy.RequestWindows()
	requestPolicy := make([]adminRequestPolicyWindow, 0, len(requestWindows))
	for _, window := range requestWindows {
		requestPolicy = append(requestPolicy, adminRequestPolicyWindow{Amount: window.Amount, Duration: int64(window.Duration / time.Second)})
	}
	tokenWindows := policy.TokenWindows()
	tokenPolicy := make([]adminPolicyWindow, 0, len(tokenWindows))
	for _, window := range tokenWindows {
		tokenPolicy = append(tokenPolicy, adminPolicyWindow{Amount: window.Amount, Duration: int64(window.Duration / time.Second)})
	}
	budgets := policy.BudgetLimits()
	budgetPolicy := make([]adminBudgetLimit, 0, len(budgets))
	for _, budget := range budgets {
		amount, _ := budget.Amount.Micros()
		budgetPolicy = append(budgetPolicy, adminBudgetLimit{Period: string(budget.Period), AmountMicros: amount})
	}
	return adminKeyDetailItem{
		ID: record.ID, Name: record.Name, DisplayPrefix: record.DisplayPrefix,
		Enabled: record.Enabled, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
		ExpiresAt: record.ExpiresAt,
		Policy: adminKeyPolicy{
			AllowedModels: policy.AllowedModels(), DeniedModels: policy.DeniedModels(),
			RequestWindows: requestPolicy, TokenWindows: tokenPolicy,
			TokenMode: policy.TokenMode(), MaxConcurrentRequests: policy.MaxConcurrency(),
			BudgetLimits: budgetPolicy, LogRequestBody: policy.LogRequestBody(), LogResponseBody: policy.LogResponseBody(),
		},
	}
}

func (handler *adminHandler) listKeys(response http.ResponseWriter, request *http.Request) {
	if !handler.authenticate(response, request) {
		return
	}
	lister, ok := handler.service.repository.(apiKeyPageLister)
	if !ok {
		writeAdminError(response, http.StatusInternalServerError, "internal_error", "key listing failed")
		return
	}
	query, err := adminQuery(request)
	if err != nil {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid pagination")
		return
	}
	limit := 50
	values, present := query["limit"]
	if present {
		if len(values) != 1 || values[0] == "" {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid pagination")
			return
		}
		value := values[0]
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 || parsed > 500 {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid pagination")
			return
		}
		limit = parsed
	}
	cursorValues, cursorPresent := query["cursor"]
	if cursorPresent && len(cursorValues) != 1 {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid pagination")
		return
	}
	cursor := ""
	if cursorPresent {
		cursor = cursorValues[0]
		if cursor == "" {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid pagination")
			return
		}
	}
	if !security.ValidateCursorSyntax(cursor) {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid pagination")
		return
	}
	records, nextCursor, err := lister.ListAPIKeys(request.Context(), limit, cursor)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidCursor) {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid pagination")
		} else {
			writeAdminError(response, http.StatusInternalServerError, "internal_error", "key listing failed")
		}
		return
	}
	items := make([]adminKeyListItem, 0, len(records))
	for _, record := range records {
		items = append(items, adminKeyListItem{
			ID: record.ID, Name: record.Name, DisplayPrefix: record.DisplayPrefix,
			Enabled: record.Enabled, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
			ExpiresAt:     record.ExpiresAt,
			PolicySummary: adminPolicySummary{AllowModels: record.PolicySummary.AllowModels, DenyModels: record.PolicySummary.DenyModels, LogRequestBody: record.PolicySummary.LogRequestBody, LogResponseBody: record.PolicySummary.LogResponseBody},
		})
	}
	body := struct {
		Keys       []adminKeyListItem `json:"keys"`
		NextCursor string             `json:"next_cursor,omitempty"`
	}{Keys: items, NextCursor: nextCursor}
	writeAdminJSON(response, http.StatusOK, body)
}

func adminQuery(request *http.Request) (url.Values, error) {
	if request == nil || request.URL == nil {
		return nil, errInvalidAdminRequest
	}
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		return nil, errInvalidAdminRequest
	}
	return query, nil
}

func (handler *adminHandler) updatePolicy(response http.ResponseWriter, request *http.Request) {
	const prefix = "/admin/v1/keys/"
	id := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, prefix), "/policy")
	if !validAdminID(id) {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid key id")
		return
	}
	if !handler.authenticate(response, request) {
		return
	}
	body, err := decodeAdminPolicyRequest(response, request)
	if err != nil {
		if errors.Is(err, errAdminBodyTooLarge) {
			recordBodyTooLarge(request)
			writeAdminError(response, http.StatusRequestEntityTooLarge, "invalid_request", "request body is too large")
		} else {
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid request body")
		}
		return
	}
	updated, err := handler.service.updatePolicy(request.Context(), id, *body.Enabled, body.Policy)
	if err != nil {
		switch {
		case errors.Is(err, storage.ErrNotFound):
			writeAdminError(response, http.StatusNotFound, gatewayErrorNotFound, "")
		case errors.Is(err, errInvalidAdminRequest), errors.Is(err, auth.ErrInvalidPolicy):
			writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid request body")
		case errors.Is(err, errPolicyConflict):
			writeAdminError(response, http.StatusConflict, "conflict", "active token usage prevents this policy change")
		default:
			writeAdminError(response, http.StatusInternalServerError, "internal_error", "key policy update failed")
		}
		return
	}
	writeAdminJSON(response, http.StatusOK, updated)
}

type adminHandler struct {
	credential                 string
	service                    *adminKeyService
	sessionStore               *session.Store
	rateLimiter                *session.LoginRateLimiter
	trustedProxies             []*net.IPNet
	analyticsGate              *analytics.Gate
	analyticsCoordinator       *analytics.Coordinator[*storage.OverviewData]
	usageTimeseriesCoordinator *analytics.Coordinator[*storage.UsageTimeseriesData]
	usageBreakdownCoordinator  *analytics.Coordinator[*storage.UsageBreakdownData]
	sessionInitOnce            sync.Once

	startTime         time.Time
	readiness         *Readiness
	metrics           *gatewayMetrics
	completionLogger  *CompletionLogger
	usageWorker       *UsageObservationWorker
	historyWorker     *HistoryPersistenceWorker
	systemLimits      AdminSystemLimits
	telemetryCapacity int
}

func newAdminHandler(credential string, service *adminKeyService) (*adminHandler, error) {
	var trustedProxies []*net.IPNet
	if env := os.Getenv("GATEWAY_TRUSTED_PROXIES"); env != "" {
		proxies, err := session.ParseTrustedProxies(strings.Split(env, ","))
		if err != nil {
			return nil, fmt.Errorf("invalid GATEWAY_TRUSTED_PROXIES: %w", err)
		}
		trustedProxies = proxies
	}
	gate := analytics.NewGate(2)
	limits := AdminSystemLimits{
		RequestRetentionSeconds: 2592000,
		BodyRetentionSeconds:    604800,
		MaxCapturedBodyBytes:    0,
	}
	var readiness *Readiness
	if service != nil && service.repository != nil {
		if provider, ok := service.repository.(interface{ ReadinessDatabase() *storage.DB }); ok {
			readiness = NewReadiness(ReadinessConfig{
				Database: provider.ReadinessDatabase(),
			})
		}
	}
	return &adminHandler{
		credential:                 credential,
		service:                    service,
		sessionStore:               session.NewStore(session.StoreOptions{}),
		rateLimiter:                session.NewLoginRateLimiter(session.RateLimiterOptions{}),
		trustedProxies:             trustedProxies,
		analyticsGate:              gate,
		analyticsCoordinator:       analytics.NewCoordinatorWithGate[*storage.OverviewData](gate, 32, 15*time.Second),
		usageTimeseriesCoordinator: analytics.NewCoordinatorWithGate[*storage.UsageTimeseriesData](gate, 32, 15*time.Second),
		usageBreakdownCoordinator:  analytics.NewCoordinatorWithGate[*storage.UsageBreakdownData](gate, 32, 15*time.Second),
		startTime:                  processStartTime,
		readiness:                  readiness,
		systemLimits:               limits,
		telemetryCapacity:          128,
	}, nil
}

func (handler *adminHandler) setReadiness(readiness *Readiness) {
	handler.readiness = readiness
}

func (handler *adminHandler) setStartTime(t time.Time) {
	handler.startTime = t
}

func (handler *adminHandler) setSystemLimits(limits AdminSystemLimits) {
	handler.systemLimits = limits
}

func (handler *adminHandler) setTelemetryWorkers(logger *CompletionLogger, usage *UsageObservationWorker, history *HistoryPersistenceWorker, capacity int) {
	handler.completionLogger = logger
	handler.usageWorker = usage
	handler.historyWorker = history
	if capacity > 0 {
		handler.telemetryCapacity = capacity
	}
}

var (
	errDuplicateSessionCookie = errors.New("duplicate session cookie")
	errDuplicateCSRFToken     = errors.New("duplicate csrf token")
)

func getSessionCookie(request *http.Request) (*http.Cookie, error) {
	var found *http.Cookie
	for _, c := range request.Cookies() {
		if c.Name == session.CookieName {
			if found != nil {
				return nil, errDuplicateSessionCookie
			}
			found = c
		}
	}
	if found == nil {
		return nil, http.ErrNoCookie
	}
	return found, nil
}

func getCSRFToken(request *http.Request) (string, error) {
	var tokens []string
	if vals := request.Header.Values("X-CSRF-Token"); len(vals) > 0 {
		tokens = append(tokens, vals...)
	}
	if vals := request.Header.Values("X-Gateway-CSRF-Token"); len(vals) > 0 {
		tokens = append(tokens, vals...)
	}
	if len(tokens) > 1 {
		return "", errDuplicateCSRFToken
	}
	if len(tokens) == 1 {
		return tokens[0], nil
	}
	return "", nil
}

func (handler *adminHandler) initSessions() {
	handler.sessionInitOnce.Do(func() {
		if handler.sessionStore == nil {
			handler.sessionStore = session.NewStore(session.StoreOptions{})
		}
		if handler.rateLimiter == nil {
			handler.rateLimiter = session.NewLoginRateLimiter(session.RateLimiterOptions{})
		}
		if handler.analyticsGate == nil {
			handler.analyticsGate = analytics.NewGate(2)
		}
		if handler.analyticsCoordinator == nil {
			handler.analyticsCoordinator = analytics.NewCoordinatorWithGate[*storage.OverviewData](handler.analyticsGate, 32, 15*time.Second)
		}
		if handler.usageTimeseriesCoordinator == nil {
			handler.usageTimeseriesCoordinator = analytics.NewCoordinatorWithGate[*storage.UsageTimeseriesData](handler.analyticsGate, 32, 15*time.Second)
		}
		if handler.usageBreakdownCoordinator == nil {
			handler.usageBreakdownCoordinator = analytics.NewCoordinatorWithGate[*storage.UsageBreakdownData](handler.analyticsGate, 32, 15*time.Second)
		}
	})
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
}

func isUnsafeHTTPMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch:
		return true
	default:
		return false
	}
}

func (handler *adminHandler) isSameOrigin(request *http.Request) bool {
	if request.Header.Get("Sec-Fetch-Site") == "cross-site" {
		return false
	}
	origin := request.Header.Get("Origin")
	if origin == "" {
		origin = request.Header.Get("Referer")
	}
	if origin == "" {
		return false
	}

	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return false
	}

	if !strings.EqualFold(parsed.Host, request.Host) {
		return false
	}

	if session.IsSecureRequest(request, handler.trustedProxies) && parsed.Scheme != "https" {
		return false
	}

	return true
}

func (handler *adminHandler) authenticate(response http.ResponseWriter, request *http.Request) bool {
	authHeaders := request.Header.Values("Authorization")
	cookie, cookieErr := getSessionCookie(request)
	if errors.Is(cookieErr, errDuplicateSessionCookie) {
		writeAdminError(response, http.StatusBadRequest, "ambiguous_credentials", "duplicate session cookies are not permitted")
		return false
	}
	hasCookie := cookieErr == nil && cookie != nil && cookie.Value != ""
	hasAuth := len(authHeaders) > 0

	if len(authHeaders) > 1 {
		writeAdminError(response, http.StatusBadRequest, "ambiguous_credentials", "multiple Authorization headers are not permitted")
		return false
	}

	if hasAuth && hasCookie {
		writeAdminError(response, http.StatusBadRequest, "ambiguous_credentials", "request must not include both Authorization header and session cookie")
		return false
	}

	if hasAuth {
		if !adminBearerMatches(request, handler.credential) {
			writeAdminError(response, http.StatusUnauthorized, "unauthorized", "invalid admin credentials")
			return false
		}
		return true
	}

	if hasCookie {
		sess := handler.sessionStore.Get(cookie.Value)
		if sess == nil {
			session.ClearSessionCookie(response, request, handler.trustedProxies)
			writeAdminError(response, http.StatusUnauthorized, "unauthorized", "invalid or expired session")
			return false
		}

		if isUnsafeHTTPMethod(request.Method) {
			if !handler.isSameOrigin(request) {
				writeAdminError(response, http.StatusForbidden, "forbidden", "cross-origin request rejected")
				return false
			}
			csrfToken, csrfErr := getCSRFToken(request)
			if errors.Is(csrfErr, errDuplicateCSRFToken) {
				writeAdminError(response, http.StatusBadRequest, "ambiguous_credentials", "duplicate CSRF token headers are not permitted")
				return false
			}
			if !handler.sessionStore.ValidateCSRF(cookie.Value, csrfToken) {
				writeAdminError(response, http.StatusForbidden, "invalid_csrf_token", "invalid or missing CSRF token")
				return false
			}
		}
		return true
	}

	writeAdminError(response, http.StatusUnauthorized, "unauthorized", "invalid admin credentials")
	return false
}

func (handler *adminHandler) serveSession(response http.ResponseWriter, request *http.Request) {
	authHeaders := request.Header.Values("Authorization")
	cookie, cookieErr := getSessionCookie(request)
	if errors.Is(cookieErr, errDuplicateSessionCookie) {
		writeAdminError(response, http.StatusBadRequest, "ambiguous_credentials", "duplicate session cookies are not permitted")
		return
	}
	hasCookie := cookieErr == nil && cookie != nil && cookie.Value != ""
	if len(authHeaders) > 1 || (len(authHeaders) > 0 && hasCookie) {
		writeAdminError(response, http.StatusBadRequest, "ambiguous_credentials", "request must not include ambiguous credentials")
		return
	}

	switch request.Method {
	case http.MethodPost:
		handler.handleSessionLogin(response, request)
	case http.MethodGet:
		handler.handleSessionGet(response, request)
	case http.MethodDelete:
		handler.handleSessionLogout(response, request)
	default:
		response.Header().Set("Allow", "GET, POST, DELETE")
		writeAdminError(response, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

type adminSessionLoginRequest struct {
	Credential      string `json:"credential"`
	AdminCredential string `json:"admin_credential"`
	Password        string `json:"password"`
}

func (handler *adminHandler) handleSessionLogin(response http.ResponseWriter, request *http.Request) {
	clientIP := session.ClientIPFromRemoteAddr(request.RemoteAddr)
	if !handler.rateLimiter.Allow(clientIP) {
		writeAdminError(response, http.StatusTooManyRequests, "rate_limit_exceeded", "Too many failed login attempts. Please try again later.")
		return
	}

	cookie, cookieErr := getSessionCookie(request)
	if errors.Is(cookieErr, errDuplicateSessionCookie) {
		writeAdminError(response, http.StatusBadRequest, "ambiguous_credentials", "duplicate session cookies are not permitted")
		return
	}

	if request.Body == nil {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "request body is required")
		return
	}
	if !isJSONMediaType(request.Header) {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "application/json media type is required")
		return
	}

	request.Body = http.MaxBytesReader(response, request.Body, adminRequestBodyLimit)
	defer request.Body.Close()

	var body adminSessionLoginRequest
	decoder := json.NewDecoder(request.Body)
	if err := decoder.Decode(&body); err != nil {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "malformed JSON request")
		return
	}
	cred := body.Credential
	if cred == "" {
		cred = body.AdminCredential
	}
	if cred == "" {
		cred = body.Password
	}

	presentedDigest := sha256.Sum256([]byte(cred))
	expectedDigest := sha256.Sum256([]byte(handler.credential))
	matched := subtle.ConstantTimeCompare(presentedDigest[:], expectedDigest[:]) == 1

	if !matched || strings.TrimSpace(cred) == "" {
		handler.rateLimiter.RecordFailure(clientIP)
		writeAdminError(response, http.StatusUnauthorized, "invalid_api_key", "Incorrect API key provided.")
		return
	}

	handler.rateLimiter.RecordSuccess(clientIP)

	if cookieErr == nil && cookie != nil && cookie.Value != "" {
		handler.sessionStore.Revoke(cookie.Value)
	}

	sess, err := handler.sessionStore.Create()
	if err != nil {
		writeAdminError(response, http.StatusInternalServerError, "gateway_internal_error", "session creation failed")
		return
	}

	session.SetSessionCookie(response, request, sess.ID, int(handler.sessionStore.AbsoluteTimeout().Seconds()), handler.trustedProxies)

	writeAdminJSON(response, http.StatusOK, map[string]any{
		"authenticated":   true,
		"csrf_token":      sess.CSRFToken,
		"idle_expires_at": sess.IdleExpiresAt(handler.sessionStore.IdleTimeout()).Format(time.RFC3339),
		"expires_at":      sess.AbsoluteExpiresAt(handler.sessionStore.AbsoluteTimeout()).Format(time.RFC3339),
	})
}

func (handler *adminHandler) handleSessionGet(response http.ResponseWriter, request *http.Request) {
	cookie, err := getSessionCookie(request)
	if errors.Is(err, errDuplicateSessionCookie) {
		writeAdminError(response, http.StatusBadRequest, "ambiguous_credentials", "duplicate session cookies are not permitted")
		return
	}
	if err != nil || cookie == nil || cookie.Value == "" {
		writeAdminJSON(response, http.StatusOK, map[string]any{
			"authenticated": false,
		})
		return
	}

	sess := handler.sessionStore.Get(cookie.Value)
	if sess == nil {
		session.ClearSessionCookie(response, request, handler.trustedProxies)
		writeAdminJSON(response, http.StatusOK, map[string]any{
			"authenticated": false,
		})
		return
	}

	writeAdminJSON(response, http.StatusOK, map[string]any{
		"authenticated":   true,
		"csrf_token":      sess.CSRFToken,
		"idle_expires_at": sess.IdleExpiresAt(handler.sessionStore.IdleTimeout()).Format(time.RFC3339),
		"expires_at":      sess.AbsoluteExpiresAt(handler.sessionStore.AbsoluteTimeout()).Format(time.RFC3339),
	})
}

func (handler *adminHandler) handleSessionLogout(response http.ResponseWriter, request *http.Request) {
	cookie, err := getSessionCookie(request)
	if errors.Is(err, errDuplicateSessionCookie) {
		writeAdminError(response, http.StatusBadRequest, "ambiguous_credentials", "duplicate session cookies are not permitted")
		return
	}
	if err == nil && cookie != nil && cookie.Value != "" {
		sess := handler.sessionStore.GetWithoutTouch(cookie.Value)
		if sess != nil {
			if !handler.isSameOrigin(request) {
				writeAdminError(response, http.StatusForbidden, "forbidden", "cross-origin request rejected")
				return
			}
			csrfToken, csrfErr := getCSRFToken(request)
			if errors.Is(csrfErr, errDuplicateCSRFToken) {
				writeAdminError(response, http.StatusBadRequest, "ambiguous_credentials", "duplicate CSRF token headers are not permitted")
				return
			}
			if subtle.ConstantTimeCompare([]byte(sess.CSRFToken), []byte(csrfToken)) != 1 {
				writeAdminError(response, http.StatusForbidden, "invalid_csrf_token", "invalid or missing CSRF token")
				return
			}
			handler.sessionStore.Revoke(cookie.Value)
		}
	}

	session.ClearSessionCookie(response, request, handler.trustedProxies)
	writeAdminJSON(response, http.StatusOK, map[string]any{
		"authenticated": false,
	})
}

func adminBearerMatches(request *http.Request, credential string) bool {
	if strings.TrimSpace(credential) == "" {
		return false
	}
	values := request.Header.Values("Authorization")
	presented := ""
	if len(values) == 1 {
		presented = values[0]
	}
	presentedDigest := sha256.Sum256([]byte(presented))
	expectedDigest := sha256.Sum256([]byte("Bearer " + credential))
	return subtle.ConstantTimeCompare(presentedDigest[:], expectedDigest[:]) == 1
}

var errAdminBodyTooLarge = errors.New("admin request body too large")

func decodeAdminKeyRequest(response http.ResponseWriter, request *http.Request) (adminKeyRequest, error) {
	if request.Body == nil {
		return adminKeyRequest{}, errInvalidAdminRequest
	}
	if !isJSONMediaType(request.Header) {
		return adminKeyRequest{}, errInvalidAdminRequest
	}
	request.Body = http.MaxBytesReader(response, request.Body, adminRequestBodyLimit)
	defer request.Body.Close()
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var body adminKeyRequest
	if err := decoder.Decode(&body); err != nil {
		if isAdminBodyTooLarge(err) {
			return adminKeyRequest{}, errAdminBodyTooLarge
		}
		return adminKeyRequest{}, errInvalidAdminRequest
	}
	if body.Name == "" {
		return adminKeyRequest{}, errInvalidAdminRequest
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return adminKeyRequest{}, errInvalidAdminRequest
		}
		if isAdminBodyTooLarge(err) {
			return adminKeyRequest{}, errAdminBodyTooLarge
		}
		return adminKeyRequest{}, errInvalidAdminRequest
	}
	return body, nil
}

type adminPolicyRequest struct {
	Enabled *bool           `json:"enabled"`
	Policy  json.RawMessage `json:"policy"`
}

func decodeAdminPolicyRequest(response http.ResponseWriter, request *http.Request) (adminPolicyRequest, error) {
	if request.Body == nil || !isJSONMediaType(request.Header) {
		return adminPolicyRequest{}, errInvalidAdminRequest
	}
	request.Body = http.MaxBytesReader(response, request.Body, adminRequestBodyLimit)
	defer request.Body.Close()
	data, err := io.ReadAll(request.Body)
	if err != nil {
		if isAdminBodyTooLarge(err) {
			return adminPolicyRequest{}, errAdminBodyTooLarge
		}
		return adminPolicyRequest{}, errInvalidAdminRequest
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return adminPolicyRequest{}, errInvalidAdminRequest
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var body adminPolicyRequest
	if err := decoder.Decode(&body); err != nil {
		if isAdminBodyTooLarge(err) {
			return adminPolicyRequest{}, errAdminBodyTooLarge
		}
		return adminPolicyRequest{}, errInvalidAdminRequest
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if isAdminBodyTooLarge(err) {
			return adminPolicyRequest{}, errAdminBodyTooLarge
		}
		return adminPolicyRequest{}, errInvalidAdminRequest
	}
	if body.Enabled == nil || len(bytes.TrimSpace(body.Policy)) == 0 {
		return adminPolicyRequest{}, errInvalidAdminRequest
	}
	if _, err := auth.ParsePolicyJSON(body.Policy); err != nil {
		return adminPolicyRequest{}, errInvalidAdminRequest
	}
	compact := new(bytes.Buffer)
	if err := json.Compact(compact, body.Policy); err != nil {
		return adminPolicyRequest{}, errInvalidAdminRequest
	}
	body.Policy = compact.Bytes()
	return body, nil
}

// rejectDuplicateJSONKeys rejects duplicate names at every object level. The
// policy parser has the same rule, while this outer envelope must not silently
// choose between repeated enabled/policy members either.
func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return errInvalidAdminRequest
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch delimiter := token.(type) {
	case json.Delim:
		switch delimiter {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				name, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := name.(string)
				if !ok {
					return errInvalidAdminRequest
				}
				if _, exists := seen[key]; exists {
					return errInvalidAdminRequest
				}
				seen[key] = struct{}{}
				if err := scanJSONValue(decoder); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := scanJSONValue(decoder); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		}
	}
	return nil
}

func isAdminBodyTooLarge(err error) bool {
	var maxBytesError *http.MaxBytesError
	return errors.As(err, &maxBytesError)
}

func parseAdminExpiration(value *string) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, *value)
	if err != nil {
		return nil, errInvalidAdminRequest
	}
	parsed = parsed.UTC().Truncate(time.Second)
	return &parsed, nil
}

func writeAdminJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeAdminError(response http.ResponseWriter, status int, code, message string) {
	// Do not expose parser/storage detail supplied by callers. Keep the custom
	// status for the oversized-body case, but use the shared safe envelope.
	if code == "unauthorized" {
		code = gatewayErrorInvalidAPIKey
	} else if code == "invalid_request" {
		code = gatewayErrorInvalidRequest
	} else if _, ok := gatewayErrorDefinitions[code]; !ok {
		code = gatewayErrorInternal
	}
	definition := gatewayErrorDefinitions[code]
	writeGatewayErrorStatus(response, status, code, definition, "")
}
