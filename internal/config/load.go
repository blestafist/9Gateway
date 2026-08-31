package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load reads, decodes, and validates configuration from a YAML file.
func Load(path string) (Config, error) {
	if path == "" {
		return Config{}, fmt.Errorf("config path is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	var config Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode config YAML: %w", err)
	}
	config.UpstreamAPIKey, err = resolveEnvironmentReference(config.UpstreamAPIKey)
	if err != nil {
		return Config{}, err
	}
	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate config: %w", err)
	}

	return config, nil
}

func resolveEnvironmentReference(value string) (string, error) {
	if !strings.HasPrefix(value, "${") {
		return value, nil
	}
	if !strings.HasSuffix(value, "}") {
		return "", fmt.Errorf("invalid upstream API key environment reference")
	}

	name := strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")
	if !validEnvironmentName(name) {
		return "", fmt.Errorf("invalid upstream API key environment reference %q", value)
	}

	resolved, ok := os.LookupEnv(name)
	if !ok {
		return "", fmt.Errorf("upstream API key environment variable %q is not set", name)
	}
	return resolved, nil
}

func validEnvironmentName(name string) bool {
	if name == "" || (name[0] != '_' && (name[0] < 'A' || name[0] > 'Z') && (name[0] < 'a' || name[0] > 'z')) {
		return false
	}
	for _, character := range name[1:] {
		if character != '_' && (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}
