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
	"strings"
	"sync"
	"time"

	"github.com/pestit/9gateway/internal/auth"
	"github.com/pestit/9gateway/internal/storage"
)

const adminRequestBodyLimit int64 = 16 * 1024

var (
	errInvalidAdminRequest = errors.New("invalid admin request")
	errAdminKeyCreation    = errors.New("admin key creation failed")
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
	repository apiKeyRepository
	pepper     []byte
	generator  gatewayKeyGenerator
	auth       *auth.Authenticator
	refreshMu  sync.Mutex
}

func newAdminKeyService(repository apiKeyRepository, pepper []byte, tokenModes ...auth.TokenMode) (*adminKeyService, error) {
	if repository == nil {
		return nil, errAdminKeyCreation
	}
	authenticator, err := auth.NewAuthenticator(pepper, nil, tokenModes...)
	if err != nil {
		return nil, errAdminKeyCreation
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

// create makes one persistent key. A small retry budget handles an extremely
// unlikely random identity collision without ever returning a colliding raw
// credential. All errors intentionally lose repository and randomness detail.
func (service *adminKeyService) create(ctx context.Context, name string, expiresAt *time.Time) (createdAdminKey, error) {
	if service == nil || service.repository == nil || service.generator == nil || len(service.pepper) == 0 || service.auth == nil {
		return createdAdminKey{}, errAdminKeyCreation
	}
	if strings.TrimSpace(name) == "" || len(name) > 256 {
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
	for index := range records {
		if records[index].ID != id {
			continue
		}
		found = true
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
	if recordUpdater, ok := service.repository.(apiKeyPolicyRecordUpdater); ok {
		// The snapshot has already been prepared and the mutation is now the
		// commit point. Finish that operation independently of the request's
		// cancellation so a database commit cannot succeed without publishing its
		// returned record.
		persisted, err = recordUpdater.UpdatePolicyRecord(context.WithoutCancel(ctx), id, enabled, string(policyJSON))
	} else {
		err = updater.UpdatePolicy(context.WithoutCancel(ctx), id, enabled, string(policyJSON))
	}
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return updatedAdminKey{}, storage.ErrNotFound
		}
		return updatedAdminKey{}, errAdminKeyCreation
	}
	if persisted.ID != "" {
		replacement = persisted
	} else {
		// Compatibility repositories may only expose the legacy update method.
		// The prepared replacement is sufficient for authentication publication;
		// do not perform a cancelable read after a known durable update.
	}
	service.auth.Publish(prepared)
	return updatedAdminKeyFromRecord(replacement, policyJSON), nil
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
	if request.Method == http.MethodPut && strings.HasPrefix(request.URL.Path, "/admin/v1/keys/") && strings.HasSuffix(request.URL.Path, "/policy") {
		handler.updatePolicy(response, request)
		return
	}
	if request.Method != http.MethodPost || request.URL.Path != "/admin/v1/keys" {
		http.NotFound(response, request)
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
		ID        string     `json:"id"`
		Name      string     `json:"name"`
		Prefix    string     `json:"prefix"`
		Enabled   bool       `json:"enabled"`
		ExpiresAt *time.Time `json:"expires_at,omitempty"`
		CreatedAt time.Time  `json:"created_at"`
		Key       string     `json:"key"`
	}{created.ID, created.Name, created.Prefix, created.Enabled, created.ExpiresAt, created.CreatedAt, created.RawKey}
	writeAdminJSON(response, http.StatusCreated, responseBody)
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
