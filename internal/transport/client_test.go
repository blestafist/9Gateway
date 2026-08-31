package transport

import (
	"net/http"
	"testing"
	"time"
)

func TestNewClientUsesPooledTransportWithoutTotalTimeout(t *testing.T) {
	client := NewClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("client transport type = %T, want *http.Transport", client.Transport)
	}
	if client.Timeout != 0 {
		t.Fatalf("client timeout = %s, want no total timeout", client.Timeout)
	}
	if transport.MaxIdleConnsPerHost < 2 {
		t.Fatalf("max idle connections per host = %d, want concurrent reuse", transport.MaxIdleConnsPerHost)
	}
	if transport.IdleConnTimeout <= 0 || transport.TLSHandshakeTimeout <= 0 || transport.ResponseHeaderTimeout <= 0 {
		t.Fatal("transport phase timeouts must be configured")
	}
	if transport.IdleConnTimeout == time.Duration(0) {
		t.Fatal("idle connection timeout is not configured")
	}
}
