package httpserver

import (
	"encoding/json"
	"net/http"
)

const (
	gatewayErrorInvalidAPIKey      = "invalid_api_key"
	gatewayErrorKeyDisabled        = "key_disabled"
	gatewayErrorKeyExpired         = "key_expired"
	gatewayErrorModelNotAllowed    = "model_not_allowed"
	gatewayErrorRequestLimit       = "request_limit_exceeded"
	gatewayErrorConcurrencyLimit   = "concurrency_limit_exceeded"
	gatewayErrorInvalidRequest     = "invalid_request"
	gatewayErrorNotFound           = "not_found"
	gatewayErrorUpstreamConnection = "upstream_connection_error"
	gatewayErrorInternal           = "gateway_internal_error"
)

type gatewayErrorBody struct {
	Error gatewayErrorDetail `json:"error"`
}

type gatewayErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Param   string `json:"param,omitempty"`
	Code    string `json:"code"`
}

type gatewayErrorDefinition struct {
	message  string
	typeName string
	status   int
}

var gatewayErrorDefinitions = map[string]gatewayErrorDefinition{
	gatewayErrorInvalidAPIKey:      {"Incorrect API key provided.", "authentication_error", http.StatusUnauthorized},
	gatewayErrorKeyDisabled:        {"API key is disabled.", "authentication_error", http.StatusUnauthorized},
	gatewayErrorKeyExpired:         {"API key has expired.", "authentication_error", http.StatusUnauthorized},
	gatewayErrorModelNotAllowed:    {"The requested model is not allowed.", "permission_error", http.StatusForbidden},
	gatewayErrorRequestLimit:       {"Request limit exceeded.", "rate_limit_error", http.StatusTooManyRequests},
	gatewayErrorConcurrencyLimit:   {"Concurrency limit exceeded.", "rate_limit_error", http.StatusTooManyRequests},
	gatewayErrorInvalidRequest:     {"Invalid request.", "invalid_request_error", http.StatusBadRequest},
	gatewayErrorNotFound:           {"The requested resource was not found.", "invalid_request_error", http.StatusNotFound},
	gatewayErrorUpstreamConnection: {"Unable to connect to the upstream service.", "upstream_error", http.StatusBadGateway},
	gatewayErrorInternal:           {"An internal gateway error occurred.", "server_error", http.StatusInternalServerError},
}

// writeGatewayError emits only a known, constant definition. In particular,
// no cause, URL, credential, or parser detail is ever copied into the body.
func writeGatewayError(response http.ResponseWriter, code, param string) {
	definition, ok := gatewayErrorDefinitions[code]
	if !ok {
		code = gatewayErrorInternal
		definition = gatewayErrorDefinitions[code]
		param = ""
	}
	if param != "model" {
		param = ""
	}
	writeGatewayErrorStatus(response, definition.status, code, definition, param)
}

// writeGatewayErrorStatus retains an endpoint-specific status such as 413 for
// an oversized admin body while using the same safe error envelope.
func writeGatewayErrorStatus(response http.ResponseWriter, status int, code string, definition gatewayErrorDefinition, param string) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(gatewayErrorBody{Error: gatewayErrorDetail{
		Message: definition.message,
		Type:    definition.typeName,
		Param:   param,
		Code:    code,
	}})
}
