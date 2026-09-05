package httpserver

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/pestit/9gateway/internal/auth"
)

// principalContextKey deliberately has no exported value. Callers use
// PrincipalFromContext so a principal cannot be replaced with an unrelated
// value by guessing a context key.
type principalContextKey struct{}

// PrincipalFromContext returns the authenticated, safe identity attached to a
// request. The returned principal contains no raw gateway credential.
func PrincipalFromContext(ctx context.Context) (auth.Principal, bool) {
	if ctx == nil {
		return auth.Principal{}, false
	}
	principal, ok := ctx.Value(principalContextKey{}).(auth.Principal)
	if !ok {
		return auth.Principal{}, false
	}
	// Do not hand out the context-owned byte slice. This keeps the principal
	// immutable even when a later middleware inspects and modifies its local
	// copy while the request continues through the chain.
	principal.PolicyJSON = append([]byte(nil), principal.PolicyJSON...)
	return principal, true
}

func withGatewayAuthentication(authenticator *auth.Authenticator, next http.Handler) http.Handler {
	if authenticator == nil {
		return next
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		values := request.Header.Values("Authorization")
		if len(values) != 1 {
			writeGatewayError(response, gatewayErrorInvalidAPIKey, "")
			return
		}
		rawKey, ok := gatewayBearerCredential(values[0])
		if !ok {
			writeGatewayError(response, gatewayErrorInvalidAPIKey, "")
			return
		}
		principal, err := authenticator.Authenticate(rawKey)
		if err != nil {
			writeAuthenticationError(response, err)
			return
		}

		// Authenticate returns copies of principal-owned byte fields. Store a
		// value, rather than a pointer, and do not retain the request or headers.
		request = request.WithContext(context.WithValue(request.Context(), principalContextKey{}, principal))
		next.ServeHTTP(response, request)
	})
}

func gatewayBearerCredential(value string) (string, bool) {
	// Keep the grammar intentionally narrow: one exact ASCII space and one
	// non-empty credential. ParseGatewayKey performs the credential syntax
	// validation after this scheme-level check.
	const scheme = "Bearer "
	if len(value) <= len(scheme) || !strings.EqualFold(value[:len(scheme)-1], scheme[:len(scheme)-1]) || value[len(scheme)-1] != ' ' || strings.ContainsAny(value[len(scheme):], " \t\r\n") {
		return "", false
	}
	rawKey := value[len(scheme):]
	if rawKey == "" {
		return "", false
	}
	return rawKey, true
}

func writeAuthenticationError(response http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrDisabledCredential):
		writeGatewayError(response, gatewayErrorKeyDisabled, "")
	case errors.Is(err, auth.ErrExpiredCredential):
		writeGatewayError(response, gatewayErrorKeyExpired, "")
	default:
		writeGatewayError(response, gatewayErrorInvalidAPIKey, "")
	}
}
