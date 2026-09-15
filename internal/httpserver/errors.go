package httpserver

import (
	"encoding/json"
	"net/http"
	"strconv"
)

const (
	gatewayErrorInvalidAPIKey       = "invalid_api_key"
	gatewayErrorKeyDisabled         = "key_disabled"
	gatewayErrorKeyExpired          = "key_expired"
	gatewayErrorModelNotAllowed     = "model_not_allowed"
	gatewayErrorRequestLimit        = "request_limit_exceeded"
	gatewayErrorConcurrencyLimit    = "concurrency_limit_exceeded"
	gatewayErrorTokenLimit          = "token_limit_exceeded"
	gatewayErrorBudgetLimit         = "budget_exceeded"
	gatewayErrorInvalidRequest      = "invalid_request"
	gatewayErrorBodyTooLarge        = "body_too_large"
	gatewayErrorNotFound            = "not_found"
	gatewayErrorUpstreamConnection  = "upstream_connection_error"
	gatewayErrorUpstreamTimeout     = "upstream_timeout"
	gatewayErrorResponseTransport   = "response_transport_error"
	gatewayErrorConversion          = "conversion_error"
	gatewayErrorCancellation        = "cancelled"
	gatewayErrorUnsupportedResponse = "unsupported_response"
	gatewayErrorInternal            = "gateway_internal_error"
	gatewayErrorConflict            = "conflict"
	gatewayErrorCursorExpired       = "cursor_expired"
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
	gatewayErrorInvalidAPIKey:       {"Incorrect API key provided.", "authentication_error", http.StatusUnauthorized},
	gatewayErrorKeyDisabled:         {"API key is disabled.", "authentication_error", http.StatusUnauthorized},
	gatewayErrorKeyExpired:          {"API key has expired.", "authentication_error", http.StatusUnauthorized},
	gatewayErrorModelNotAllowed:     {"The requested model is not allowed.", "permission_error", http.StatusForbidden},
	gatewayErrorRequestLimit:        {"Request limit exceeded.", "rate_limit_error", http.StatusTooManyRequests},
	gatewayErrorConcurrencyLimit:    {"Concurrency limit exceeded.", "rate_limit_error", http.StatusTooManyRequests},
	gatewayErrorTokenLimit:          {"Token limit exceeded.", "rate_limit_error", http.StatusTooManyRequests},
	gatewayErrorBudgetLimit:         {"Budget exceeded.", "rate_limit_error", http.StatusTooManyRequests},
	gatewayErrorInvalidRequest:      {"Invalid request.", "invalid_request_error", http.StatusBadRequest},
	gatewayErrorBodyTooLarge:        {"Request body exceeds the 10 MiB limit.", "invalid_request_error", http.StatusRequestEntityTooLarge},
	gatewayErrorNotFound:            {"The requested resource was not found.", "invalid_request_error", http.StatusNotFound},
	gatewayErrorUpstreamConnection:  {"Unable to connect to the upstream service.", "upstream_error", http.StatusBadGateway},
	gatewayErrorUpstreamTimeout:     {"The upstream service timed out.", "upstream_error", http.StatusGatewayTimeout},
	gatewayErrorResponseTransport:   {"The upstream response could not be delivered.", "upstream_error", http.StatusBadGateway},
	gatewayErrorConversion:          {"The upstream response could not be converted.", "upstream_error", http.StatusBadGateway},
	gatewayErrorCancellation:        {"The request was cancelled.", "request_error", 499},
	gatewayErrorUnsupportedResponse: {"The upstream response is not supported.", "upstream_error", http.StatusBadGateway},
	gatewayErrorInternal:            {"An internal gateway error occurred.", "server_error", http.StatusInternalServerError},
	gatewayErrorConflict:            {"The requested change conflicts with active work.", "conflict_error", http.StatusConflict},
	gatewayErrorCursorExpired:       {"The pagination cursor has expired; restart the traversal.", "invalid_request_error", http.StatusBadRequest},
}

// gatewayErrorTraceRecorder is deliberately tiny so error writing remains
// independent of the trace implementation and can also be used by tests.
type gatewayErrorTraceRecorder interface {
	setGatewayErrorCode(SafeErrorCode)
}

func safeErrorCodeForGatewayCode(code string) SafeErrorCode {
	switch code {
	case gatewayErrorInvalidAPIKey:
		return ErrorCodeInvalidAPIKey
	case gatewayErrorKeyDisabled:
		return ErrorCodeKeyDisabled
	case gatewayErrorKeyExpired:
		return ErrorCodeKeyExpired
	case gatewayErrorInvalidRequest:
		return ErrorCodeInvalidRequest
	case gatewayErrorBodyTooLarge:
		return ErrorCodeInvalidRequest
	case gatewayErrorModelNotAllowed:
		return ErrorCodeModelNotAllowed
	case gatewayErrorRequestLimit:
		return ErrorCodeRequestLimit
	case gatewayErrorConcurrencyLimit:
		return ErrorCodeConcurrencyLimit
	case gatewayErrorTokenLimit:
		return ErrorCodeTokenLimit
	case gatewayErrorBudgetLimit:
		return ErrorCodeBudgetLimit
	case gatewayErrorUpstreamConnection:
		return ErrorCodeUpstreamConnection
	case gatewayErrorUpstreamTimeout:
		return ErrorCodeUpstreamTimeout
	case gatewayErrorResponseTransport:
		return ErrorCodeResponseTransport
	case gatewayErrorConversion:
		return ErrorCodeConversion
	case gatewayErrorCancellation:
		return ErrorCodeCancellation
	case gatewayErrorUnsupportedResponse:
		return ErrorCodeUnsupportedResponse
	case gatewayErrorInternal:
		return ErrorCodeInternal
	case gatewayErrorNotFound:
		return ErrorCodeNotFound
	case gatewayErrorConflict:
		return ErrorCodeConflict
	case gatewayErrorCursorExpired:
		return ErrorCodeInvalidRequest
	default:
		return ErrorCodeUnknown
	}
}

// writeGatewayError emits only a known, constant definition. In particular,
// no cause, URL, credential, or parser detail is ever copied into the body.
func writeGatewayError(response http.ResponseWriter, code, param string) {
	writeGatewayErrorRetryAfter(response, code, param, 0)
}

// writeGatewayErrorRetryAfter emits a gateway error and, when requested, a
// delta-seconds Retry-After header. The value is intentionally supplied by the
// limiter rather than derived from a wall clock in the HTTP layer.
func writeGatewayErrorRetryAfter(response http.ResponseWriter, code, param string, retryAfter int) {
	definition, ok := gatewayErrorDefinitions[code]
	if !ok {
		code = gatewayErrorInternal
		definition = gatewayErrorDefinitions[code]
		param = ""
	}
	if param != "model" {
		param = ""
	}
	if retryAfter > 0 {
		response.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	}
	writeGatewayErrorStatus(response, definition.status, code, definition, param)
}

// writeGatewayErrorStatus retains an endpoint-specific status such as 413 for
// an oversized admin body while using the same safe error envelope.
func writeGatewayErrorStatus(response http.ResponseWriter, status int, code string, definition gatewayErrorDefinition, param string) {
	if recorder, ok := response.(gatewayErrorTraceRecorder); ok {
		safeCode := safeErrorCodeForGatewayCode(code)
		if safeCode == ErrorCodeUnknown {
			safeCode = ErrorCodeInternal
		}
		recorder.setGatewayErrorCode(safeCode)
	}
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(gatewayErrorBody{Error: gatewayErrorDetail{
		Message: definition.message,
		Type:    definition.typeName,
		Param:   param,
		Code:    code,
	}})
}
