package transport

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestNewClientUsesPooledTransportWithRequestContextTimeout(t *testing.T) {
	client := NewClient()
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("client transport type = %T, want *http.Transport", client.Transport)
	}
	if client.Timeout != 0 {
		t.Fatalf("client timeout = %s, want no total timeout", client.Timeout)
	}
	if err := client.CheckRedirect(nil, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("CheckRedirect() error = %v, want http.ErrUseLastResponse", err)
	}
	if !transport.DisableCompression {
		t.Fatal("transport automatic compression must be disabled")
	}
	if transport.MaxIdleConnsPerHost < 2 {
		t.Fatalf("max idle connections per host = %d, want concurrent reuse", transport.MaxIdleConnsPerHost)
	}
	if transport.DialContext == nil || transport.TLSHandshakeTimeout != UpstreamRequestTimeout {
		t.Fatal("connection setup must use the one-hour safety ceiling")
	}
	if transport.ResponseHeaderTimeout != 0 {
		t.Fatalf("response header timeout = %s, want request-context timeout only", transport.ResponseHeaderTimeout)
	}
	if transport.IdleConnTimeout == time.Duration(0) {
		t.Fatal("idle connection timeout is not configured")
	}
}
