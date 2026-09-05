package httpserver

import (
	"net/http"
)

// newTransportHandler is deliberately test-only. It exercises transparent
// transport without pretending to be the exported, authenticated gateway.
func newTransportHandler(client *http.Client, baseURL, apiKey string) http.Handler {
	return newHandlerWithCompletionLogger(nil, route(newProxyHandler(client, baseURL, apiKey)))
}

func newTransportHandlerWithCompletionLogger(client *http.Client, baseURL, apiKey string, logger *CompletionLogger) http.Handler {
	return newHandlerWithCompletionLogger(logger, route(newProxyHandler(client, baseURL, apiKey)))
}
