package httpserver

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteGatewayErrorMappings(t *testing.T) {
	for code, want := range map[string]struct {
		status   int
		typeName string
		message  string
	}{
		gatewayErrorInvalidAPIKey:      {401, "authentication_error", "Incorrect API key provided."},
		gatewayErrorKeyDisabled:        {401, "authentication_error", "API key is disabled."},
		gatewayErrorKeyExpired:         {401, "authentication_error", "API key has expired."},
		gatewayErrorModelNotAllowed:    {403, "permission_error", "The requested model is not allowed."},
		gatewayErrorRequestLimit:       {429, "rate_limit_error", "Request limit exceeded."},
		gatewayErrorConcurrencyLimit:   {429, "rate_limit_error", "Concurrency limit exceeded."},
		gatewayErrorInvalidRequest:     {400, "invalid_request_error", "Invalid request."},
		gatewayErrorUpstreamConnection: {502, "upstream_error", "Unable to connect to the upstream service."},
		gatewayErrorInternal:           {500, "server_error", "An internal gateway error occurred."},
	} {
		t.Run(code, func(t *testing.T) {
			response := httptest.NewRecorder()
			writeGatewayError(response, code, "")
			if response.Code != want.status {
				t.Fatalf("status = %d, want %d", response.Code, want.status)
			}
			if response.Header().Get("Content-Type") != "application/json" {
				t.Fatalf("Content-Type = %q", response.Header().Get("Content-Type"))
			}
			var body gatewayErrorBody
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Error.Code != code || body.Error.Type != want.typeName || body.Error.Message != want.message || body.Error.Param != "" {
				t.Fatalf("error = %#v", body.Error)
			}
		})
	}
}

func TestWriteGatewayErrorSupportsSafeOptionalParam(t *testing.T) {
	response := httptest.NewRecorder()
	writeGatewayError(response, gatewayErrorModelNotAllowed, "model")

	var body gatewayErrorBody
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Param != "model" {
		t.Fatalf("param = %q, want model", body.Error.Param)
	}
}

func TestWriteGatewayErrorDoesNotEchoUnsafeParam(t *testing.T) {
	response := httptest.NewRecorder()
	writeGatewayError(response, gatewayErrorModelNotAllowed, "sk-gw-presented-secret")
	if strings.Contains(response.Body.String(), "sk-gw-presented-secret") {
		t.Fatal("gateway error echoed an unsafe param")
	}
}

func TestGatewayErrorBodiesDoNotEchoSensitiveOrInternalDetails(t *testing.T) {
	response := httptest.NewRecorder()
	writeGatewayError(response, gatewayErrorInternal, "model")
	body := response.Body.String()
	for _, forbidden := range []string{
		"sk-gw-presented-secret",
		"pepper-secret",
		"upstream-credential",
		"https://user:password@example.test/path",
		"SELECT * FROM api_keys",
		"json: cannot unmarshal",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("body contains forbidden detail %q: %s", forbidden, body)
		}
	}
}
