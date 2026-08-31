package httpserver

import "net/http"

// NewHandler returns the gateway's HTTP handler.
func NewHandler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.NotFound(response, request)
	})
}
