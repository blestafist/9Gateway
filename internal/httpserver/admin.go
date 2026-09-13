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
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/limiter"
	"github.com/pestit/9gateway/internal/storage"
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
	ID        string
	Name      string
	Prefix    string
	Enabled   bool
	ExpiresAt *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
	Policy    json.RawMessage
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
	if value == "" || len(value) > 256 {
		return false
	}
	for index, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' {
			if index == 0 && (character == '-' || character == '_') {
				return false
			}
			continue
		}
		return false
	}
	return true
}

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
	if !adminBearerMatches(request, handler.credential) {
		writeAdminError(response, http.StatusUnauthorized, "unauthorized", "invalid admin credentials")
		return
	}

	body, err := decodeAdminKeyRequest(response, request)
	if err != nil {
		if errors.Is(err, errAdminBodyTooLarge) {
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
	if !adminBearerMatches(request, handler.credential) {
		writeAdminError(response, http.StatusUnauthorized, "unauthorized", "invalid admin credentials")
		return
	}
	lister, ok := handler.service.repository.(requestPageLister)
	if !ok {
		writeAdminError(response, http.StatusInternalServerError, "internal_error", "request listing failed")
		return
	}
	query := request.URL.Query()
	limit, err := parseAdminListLimit(query)
	if err != nil {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid request parameters")
		return
	}
	cursor, err := singleAdminQueryValue(query, "cursor", false)
	if err != nil || len(cursor) > 512 || strings.ContainsAny(cursor, "\r\n") {
		writeAdminError(response, http.StatusBadRequest, "invalid_request", "invalid request parameters")
		return
	}
	keyID, err := singleAdminQueryValue(query, "key_id", false)
	if err != nil || (keyID != "" && !validAPIKeyID(keyID)) {
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
	if !adminBearerMatches(request, handler.credential) {
		writeAdminError(response, http.StatusUnauthorized, "unauthorized", "invalid admin credentials")
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
	if !adminBearerMatches(request, handler.credential) {
		writeAdminError(response, http.StatusUnauthorized, "unauthorized", "invalid admin credentials")
		return
	}
	lister, ok := handler.service.repository.(apiKeyPageLister)
	if !ok {
		writeAdminError(response, http.StatusInternalServerError, "internal_error", "key listing failed")
		return
	}
	limit := 50
	values, present := request.URL.Query()["limit"]
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
	cursorValues, cursorPresent := request.URL.Query()["cursor"]
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
	if len(cursor) > 512 || strings.ContainsAny(cursor, "\r\n") {
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

func (handler *adminHandler) updatePolicy(response http.ResponseWriter, request *http.Request) {
	const prefix = "/admin/v1/keys/"
	id := strings.TrimSuffix(strings.TrimPrefix(request.URL.Path, prefix), "/policy")
	if id == "" || strings.Contains(id, "/") {
		writeAdminError(response, http.StatusNotFound, "not_found", "")
		return
	}
	if !adminBearerMatches(request, handler.credential) {
		writeAdminError(response, http.StatusUnauthorized, "unauthorized", "invalid admin credentials")
		return
	}
	body, err := decodeAdminPolicyRequest(response, request)
	if err != nil {
		if errors.Is(err, errAdminBodyTooLarge) {
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
	credential string
	service    *adminKeyService
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
