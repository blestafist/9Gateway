package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Config contains the settings required to connect the gateway to 9router.
type Config struct {
	ListenAddr      string `yaml:"listen_addr"`
	UpstreamBaseURL string `yaml:"upstream_base_url"`
	UpstreamAPIKey  string `yaml:"upstream_api_key"`
	SQLitePath      string `yaml:"sqlite_path"`
	AuthPepper      string `yaml:"auth_pepper"`
	AdminCredential string `yaml:"admin_credential"`
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
