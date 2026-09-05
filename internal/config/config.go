package config

import (
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// TokenizerMode selects how preflight input-token information is obtained.
// Exact model-specific tokenizers are intentionally not a configuration mode
// yet; they can be added behind the estimator contract in a later task.
type TokenizerMode string

const (
	TokenizerModeUsageOnly TokenizerMode = "usage_only"
	TokenizerModeEstimate  TokenizerMode = "estimate"
)

const (
	// These defaults keep preflight inspection small and make conservative
	// fallback reservations finite for minimal deployments.
	DefaultMaxInspectedRequestBytes   int64 = 64 * 1024
	DefaultFallbackUnknownInputTokens int64 = 4 * 1024
	DefaultFallbackMaxOutputTokens    int64 = 4 * 1024

	// Bounds prevent configuration from turning preflight into an unbounded
	// body buffer or allowing fallback arithmetic to approach int64 overflow.
	MaxMaxInspectedRequestBytes   int64 = 16 * 1024 * 1024
	MaxFallbackUnknownInputTokens int64 = 1_000_000_000
	MaxFallbackMaxOutputTokens    int64 = 1_000_000_000
)

// TokenizerConfig contains deployment-wide preflight estimation settings.
// It deliberately contains no credentials or per-key policy values.
type TokenizerConfig struct {
	Mode                       TokenizerMode `yaml:"mode"`
	MaxInspectedRequestBytes   int64         `yaml:"max_inspected_request_bytes"`
	FallbackUnknownInputTokens int64         `yaml:"fallback_unknown_input_tokens"`
	FallbackMaxOutputTokens    int64         `yaml:"fallback_max_output_tokens"`
}

func (c *TokenizerConfig) applyDefaults() {
	if c.Mode == "" {
		c.Mode = TokenizerModeEstimate
	}
	if c.MaxInspectedRequestBytes == 0 {
		c.MaxInspectedRequestBytes = DefaultMaxInspectedRequestBytes
	}
	if c.FallbackUnknownInputTokens == 0 {
		c.FallbackUnknownInputTokens = DefaultFallbackUnknownInputTokens
	}
	if c.FallbackMaxOutputTokens == 0 {
		c.FallbackMaxOutputTokens = DefaultFallbackMaxOutputTokens
	}
}

func (c TokenizerConfig) validate() error {
	if c.Mode != TokenizerModeUsageOnly && c.Mode != TokenizerModeEstimate {
		return fmt.Errorf("tokenizer mode %q is unsupported; want %q or %q", c.Mode, TokenizerModeUsageOnly, TokenizerModeEstimate)
	}
	if c.MaxInspectedRequestBytes <= 0 {
		return fmt.Errorf("tokenizer max inspected request bytes must be positive")
	}
	if c.MaxInspectedRequestBytes > MaxMaxInspectedRequestBytes {
		return fmt.Errorf("tokenizer max inspected request bytes exceeds maximum %d", MaxMaxInspectedRequestBytes)
	}
	if c.FallbackUnknownInputTokens <= 0 {
		return fmt.Errorf("tokenizer fallback unknown input tokens must be positive")
	}
	if c.FallbackUnknownInputTokens > MaxFallbackUnknownInputTokens {
		return fmt.Errorf("tokenizer fallback unknown input tokens exceeds maximum %d", MaxFallbackUnknownInputTokens)
	}
	if c.FallbackMaxOutputTokens <= 0 {
		return fmt.Errorf("tokenizer fallback max output tokens must be positive")
	}
	if c.FallbackMaxOutputTokens > MaxFallbackMaxOutputTokens {
		return fmt.Errorf("tokenizer fallback max output tokens exceeds maximum %d", MaxFallbackMaxOutputTokens)
	}
	// Keep the documented finite limits safe even if they are changed later.
	if c.FallbackUnknownInputTokens > math.MaxInt64-c.FallbackMaxOutputTokens {
		return fmt.Errorf("tokenizer fallback token total overflows int64")
	}
	return nil
}

// Config contains the settings required to connect the gateway to 9router.
type Config struct {
	ListenAddr      string          `yaml:"listen_addr"`
	UpstreamBaseURL string          `yaml:"upstream_base_url"`
	UpstreamAPIKey  string          `yaml:"upstream_api_key"`
	SQLitePath      string          `yaml:"sqlite_path"`
	AuthPepper      string          `yaml:"auth_pepper"`
	AdminCredential string          `yaml:"admin_credential"`
	Tokenizer       TokenizerConfig `yaml:"tokenizer"`
}

// ApplyDefaults fills omitted optional deployment settings. It is called by
// Load after strict YAML decoding and is also available to startup code that
// constructs Config values directly.
func (c *Config) ApplyDefaults() {
	c.Tokenizer.applyDefaults()
}

// Validate checks the configuration needed before the gateway can start.
func (c Config) Validate() error {
	tokenizer := c.Tokenizer
	tokenizer.applyDefaults()
	if err := tokenizer.validate(); err != nil {
		return fmt.Errorf("tokenizer: %w", err)
	}
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
	if strings.TrimSpace(c.SQLitePath) == "" {
		return fmt.Errorf("sqlite path is required")
	}
	if err := validateSQLitePath(c.SQLitePath); err != nil {
		return fmt.Errorf("sqlite path: %w", err)
	}
	if strings.TrimSpace(c.AuthPepper) == "" {
		return fmt.Errorf("auth pepper is required")
	}
	if strings.TrimSpace(c.AdminCredential) == "" {
		return fmt.Errorf("admin credential is required")
	}
	if c.AdminCredential == c.UpstreamAPIKey {
		return fmt.Errorf("admin credential must differ from upstream API key")
	}

	return nil
}

func validateSQLitePath(path string) error {
	if path == ":memory:" {
		return nil
	}
	if strings.IndexByte(path, 0) >= 0 {
		return fmt.Errorf("contains NUL byte")
	}

	info, err := os.Stat(path)
	switch {
	case err == nil:
		if info.IsDir() {
			return fmt.Errorf("is a directory")
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("is not a regular file")
		}
		if info.Mode().Perm()&0o222 == 0 {
			return fmt.Errorf("is not writable")
		}
		return nil
	case !os.IsNotExist(err):
		return fmt.Errorf("cannot inspect path: %w", err)
	}

	parent := filepath.Dir(path)
	parentInfo, err := os.Stat(parent)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("parent directory does not exist")
		}
		return fmt.Errorf("cannot inspect parent directory: %w", err)
	}
	if !parentInfo.IsDir() {
		return fmt.Errorf("parent path is not a directory")
	}
	if parentInfo.Mode().Perm()&0o222 == 0 || parentInfo.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("parent directory is not writable")
	}
	return nil
}
