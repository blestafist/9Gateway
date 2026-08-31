package config

import (
	"fmt"
	"net/url"
	"strings"
)

// Config contains the settings required to connect the gateway to 9router.
type Config struct {
	ListenAddr      string `yaml:"listen_addr"`
	UpstreamBaseURL string `yaml:"upstream_base_url"`
	UpstreamAPIKey  string `yaml:"upstream_api_key"`
}

// Validate checks the configuration needed before the gateway can start.
func (c Config) Validate() error {
	if strings.TrimSpace(c.ListenAddr) == "" {
		return fmt.Errorf("listen address is required")
	}
	if strings.TrimSpace(c.UpstreamBaseURL) == "" {
		return fmt.Errorf("upstream base URL is required")
	}

	upstreamURL, err := url.Parse(c.UpstreamBaseURL)
	if err != nil {
		return fmt.Errorf("invalid upstream base URL: %w", err)
	}
	if upstreamURL.Scheme != "http" && upstreamURL.Scheme != "https" {
		return fmt.Errorf("invalid upstream base URL: scheme must be http or https")
	}
	if upstreamURL.Host == "" {
		return fmt.Errorf("invalid upstream base URL: host is required")
	}
	if strings.TrimSpace(c.UpstreamAPIKey) == "" {
		return fmt.Errorf("upstream API key is required")
	}

	return nil
}
