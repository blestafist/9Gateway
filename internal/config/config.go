package config

import (
	"fmt"
	"math"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pestit/9gateway/internal/security"
	"github.com/pestit/9gateway/internal/storage/schema"
)

// TokenizerMode selects how preflight input-token information is obtained.
// Exact model-specific tokenizers are intentionally not a configuration mode
// yet; they can be added behind the estimator contract in a later task.
type TokenizerMode string

const (
	TokenizerModeUsageOnly TokenizerMode = "usage_only"
	TokenizerModeEstimate  TokenizerMode = "estimate"
)

// effectiveUID is a variable rather than a direct call in validation so
// callers can exercise the non-root deployment rule without changing the
// process identity. Production code never replaces it.
var effectiveUID = os.Geteuid

const (
	// DefaultSQLitePath keeps the default database on the persistent data
	// volume used by the container image.
	DefaultSQLitePath = "/data/gateway.db"

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

// Observability defaults are intentionally conservative. The queue is large
// enough for normal bursts without making detailed telemetry a meaningful
// memory sink; body capture is disabled until a deployment opts in.
const (
	DefaultTelemetryQueueCapacity  int   = 128
	MaxTelemetryQueueCapacity      int   = 4096
	DefaultMaxCapturedBodyBytes    int64 = 0
	MaxMaxCapturedBodyBytes              = schema.RequestBodySchemaSafetyMaxBytes
	DefaultRequestRetentionSeconds int64 = 30 * 24 * 60 * 60
	DefaultBodyRetentionSeconds    int64 = 7 * 24 * 60 * 60
	MaxRequestRetentionSeconds     int64 = 365 * 24 * 60 * 60
	MaxBodyRetentionSeconds        int64 = 365 * 24 * 60 * 60
	DefaultShutdownTimeoutSeconds  int64 = 30
	MaxShutdownTimeoutSeconds      int64 = 10 * 60
)

// Retention execution is deliberately not deployment configurable. A pass is
// run at writer startup and after each fixed number of processed jobs; each
// table is independently capped so cleanup cannot turn into an unbounded SQL
// operation. These constants are exported for focused tests and future
// persistence workers, but production code must use these values.
const (
	RetentionPassEveryJobs          uint64 = 1024
	RetentionMaxBodyRowsPerPass            = 1000
	RetentionMaxMetadataRowsPerPass        = 1000
)

// TokenizerConfig contains deployment-wide preflight estimation settings.
// It deliberately contains no credentials or per-key policy values.
type TokenizerConfig struct {
	Mode                       TokenizerMode `yaml:"mode"`
	MaxInspectedRequestBytes   int64         `yaml:"max_inspected_request_bytes"`
	FallbackUnknownInputTokens int64         `yaml:"fallback_unknown_input_tokens"`
	FallbackMaxOutputTokens    int64         `yaml:"fallback_max_output_tokens"`
}

// ObservabilityConfig contains deployment-wide bounds for optional detailed
// telemetry. It contains no credentials, body content, or per-key policy.
// Retention durations are strict integer seconds, not Go duration strings.
// max_captured_body_bytes == 0 disables body capture globally.
type ObservabilityConfig struct {
	TelemetryQueueCapacity  int   `yaml:"telemetry_queue_capacity"`
	MaxCapturedBodyBytes    int64 `yaml:"max_captured_body_bytes"`
	RequestRetentionSeconds int64 `yaml:"request_retention_seconds"`
	BodyRetentionSeconds    int64 `yaml:"body_retention_seconds"`
}

func (c *ObservabilityConfig) applyDefaults() {
	if c.TelemetryQueueCapacity == 0 {
		c.TelemetryQueueCapacity = DefaultTelemetryQueueCapacity
	}
	if c.MaxCapturedBodyBytes == 0 {
		c.MaxCapturedBodyBytes = DefaultMaxCapturedBodyBytes
	}
	if c.RequestRetentionSeconds == 0 {
		c.RequestRetentionSeconds = DefaultRequestRetentionSeconds
	}
	if c.BodyRetentionSeconds == 0 {
		c.BodyRetentionSeconds = DefaultBodyRetentionSeconds
	}
}

func (c ObservabilityConfig) validate() error {
	if c.TelemetryQueueCapacity <= 0 {
		return fmt.Errorf("telemetry_queue_capacity must be positive")
	}
	if c.TelemetryQueueCapacity > MaxTelemetryQueueCapacity {
		return fmt.Errorf("telemetry_queue_capacity exceeds maximum %d", MaxTelemetryQueueCapacity)
	}
	if c.MaxCapturedBodyBytes < 0 {
		return fmt.Errorf("max_captured_body_bytes must not be negative")
	}
	if c.MaxCapturedBodyBytes > schema.RequestBodySchemaSafetyMaxBytes {
		return fmt.Errorf("max_captured_body_bytes exceeds maximum %d", schema.RequestBodySchemaSafetyMaxBytes)
	}
	if c.RequestRetentionSeconds <= 0 {
		return fmt.Errorf("request_retention_seconds must be positive")
	}
	if c.RequestRetentionSeconds > MaxRequestRetentionSeconds {
		return fmt.Errorf("request_retention_seconds exceeds maximum %d", MaxRequestRetentionSeconds)
	}
	if c.BodyRetentionSeconds <= 0 {
		return fmt.Errorf("body_retention_seconds must be positive")
	}
	if c.BodyRetentionSeconds > MaxBodyRetentionSeconds {
		return fmt.Errorf("body_retention_seconds exceeds maximum %d", MaxBodyRetentionSeconds)
	}
	if c.BodyRetentionSeconds > c.RequestRetentionSeconds {
		return fmt.Errorf("body_retention_seconds must not exceed request_retention_seconds")
	}
	return nil
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
	ListenAddr             string              `yaml:"listen_addr"`
	UpstreamBaseURL        string              `yaml:"upstream_base_url"`
	UpstreamAPIKey         string              `yaml:"upstream_api_key"`
	SQLitePath             string              `yaml:"sqlite_path"`
	AuthPepper             string              `yaml:"auth_pepper"`
	AdminCredential        string              `yaml:"admin_credential"`
	Tokenizer              TokenizerConfig     `yaml:"tokenizer"`
	Observability          ObservabilityConfig `yaml:"observability"`
	Pricing                PricingConfig       `yaml:"pricing"`
	ShutdownTimeoutSeconds int64               `yaml:"shutdown_timeout_seconds"`
}

// ApplyDefaults fills omitted optional deployment settings. It is called by
// Load after strict YAML decoding and is also available to startup code that
// constructs Config values directly.
func (c *Config) ApplyDefaults() {
	if c.SQLitePath == "" {
		c.SQLitePath = DefaultSQLitePath
	}
	c.Tokenizer.applyDefaults()
	c.Observability.applyDefaults()
	if c.ShutdownTimeoutSeconds == 0 {
		c.ShutdownTimeoutSeconds = DefaultShutdownTimeoutSeconds
	}
}

// Validate checks the configuration needed before the gateway can start.
func (c Config) Validate() error {
	tokenizer := c.Tokenizer
	tokenizer.applyDefaults()
	if err := tokenizer.validate(); err != nil {
		return fieldError("tokenizer", err.Error())
	}
	observability := c.Observability
	observability.applyDefaults()
	if err := observability.validate(); err != nil {
		return fieldError("observability", err.Error())
	}
	if err := c.Pricing.Validate(); err != nil {
		return fieldError("pricing", err.Error())
	}
	if c.ShutdownTimeoutSeconds < 0 {
		return fieldError("shutdown_timeout_seconds", "must not be negative")
	}
	if c.ShutdownTimeoutSeconds > MaxShutdownTimeoutSeconds {
		return fieldError("shutdown_timeout_seconds", fmt.Sprintf("exceeds maximum %d", MaxShutdownTimeoutSeconds))
	}
	if strings.TrimSpace(c.ListenAddr) == "" {
		return fieldError("listen_addr", "listen address is required")
	}
	listenHost, listenPort, err := validateListenAddress(c.ListenAddr)
	if err != nil {
		return fieldError("listen_addr", err.Error())
	}
	if listenPort < 1024 && effectiveUID() != 0 {
		return fieldError("listen_addr", fmt.Sprintf("listen port %d requires root privileges", listenPort))
	}
	if strings.TrimSpace(c.UpstreamBaseURL) == "" {
		return fieldError("upstream_base_url", "upstream base URL is required")
	}

	upstream, err := security.ValidateUpstreamURL(c.UpstreamBaseURL)
	if err != nil {
		return fieldError("upstream_base_url", fmt.Sprintf("invalid upstream base URL: %v", err))
	}
	if upstreamLoopsBack(upstream, listenHost, listenPort) {
		return fieldError("upstream_base_url", "points to the gateway listen address")
	}
	if strings.TrimSpace(c.UpstreamAPIKey) == "" {
		return fieldError("upstream_api_key", "upstream API key is required")
	}
	if strings.TrimSpace(c.SQLitePath) == "" {
		return fieldError("sqlite_path", "sqlite path is required")
	}
	if err := validateSQLitePath(c.SQLitePath); err != nil {
		return fieldError("sqlite_path", fmt.Sprintf("sqlite path: %v", err))
	}
	if strings.TrimSpace(c.AuthPepper) == "" {
		return fieldError("auth_pepper", "auth pepper is required")
	}
	if strings.TrimSpace(c.AdminCredential) == "" {
		return fieldError("admin_credential", "admin credential is required")
	}
	if c.AdminCredential == c.UpstreamAPIKey {
		return fieldError("admin_credential", "must differ from upstream API key")
	}

	return nil
}

func fieldError(field, reason string) error {
	return fmt.Errorf("config validation failed: field '%s': %s", field, reason)
}

func validateListenAddress(address string) (string, int, error) {
	address = strings.TrimSpace(address)
	if address == "" {
		return "", 0, fmt.Errorf("address is required")
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, fmt.Errorf("must be a host:port address")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("port must be between 1 and 65535")
	}
	return host, port, nil
}

func upstreamLoopsBack(upstream *url.URL, listenHost string, listenPort int) bool {
	if upstream == nil {
		return false
	}
	upstreamPort := 0
	if explicit := upstream.Port(); explicit != "" {
		upstreamPort, _ = strconv.Atoi(explicit)
	} else if upstream.Scheme == "https" {
		upstreamPort = 443
	} else {
		upstreamPort = 80
	}
	if upstreamPort != listenPort {
		return false
	}
	upstreamHost := strings.TrimSuffix(strings.ToLower(upstream.Hostname()), ".")
	listenHost = strings.TrimSuffix(strings.ToLower(strings.Trim(listenHost, "[]")), ".")
	if listenHost == "" || listenHost == "0.0.0.0" || listenHost == "::" {
		return isLoopbackHost(upstreamHost) || upstreamHost == "0.0.0.0" || upstreamHost == "::"
	}
	if upstreamHost == listenHost {
		return true
	}
	return isLoopbackHost(upstreamHost) && isLoopbackHost(listenHost)
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
