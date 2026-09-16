package transport

import (
	"net"
	"net/http"
	"time"
)

const UpstreamRequestTimeout = time.Hour

// NewClient creates the long-lived HTTP client used for upstream requests.
func NewClient() *http.Client {
	return &http.Client{
		// The gateway is a transparent proxy: an upstream redirect is a response
		// for the client to handle, not an instruction for the gateway to follow.
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DisableCompression:    true,
			DialContext:           (&net.Dialer{Timeout: UpstreamRequestTimeout, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   UpstreamRequestTimeout,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}
