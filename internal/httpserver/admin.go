package httpserver

import (
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

func newAdminKeyService(repository apiKeyRepository, pepper []byte) (*adminKeyService, error) {
	if repository == nil {
		return nil, errAdminKeyCreation
	}
	authenticator, err := auth.NewAuthenticator(pepper, nil)
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

func newAPIKeyID() (string, error) {
	var random [16]byte
	if _, err := io.ReadFull(rand.Reader, random[:]); err != nil {
		return "", err
	}
	return "key-" + hex.EncodeToString(random[:]), nil
}

func (handler *adminHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
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
	writeAdminJSON(response, status, struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    string `json:"code"`
		} `json:"error"`
	}{Error: struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	}{Message: message, Type: "gateway_error", Code: code}})
}
