package config

import (
	"bytes"
	"fmt"
	"io"
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
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Config{}, fmt.Errorf("decode config YAML: multiple documents are not supported")
		}
		return Config{}, fmt.Errorf("decode config YAML: %w", err)
	}
	if err := rejectExplicitTokenizerDefaults(data, config); err != nil {
		return Config{}, fmt.Errorf("validate config: %w", err)
	}
	config.ApplyDefaults()
	secretFields := []*struct {
		name              string
		value             *string
		requiredReference bool
	}{
		{name: "upstream_api_key", value: &config.UpstreamAPIKey},
		{name: "auth_pepper", value: &config.AuthPepper, requiredReference: true},
		{name: "admin_credential", value: &config.AdminCredential, requiredReference: true},
	}
	for _, field := range secretFields {
		if field.requiredReference && !isEnvironmentReference(*field.value) {
			return Config{}, fmt.Errorf("configuration field %q must be an environment reference", field.name)
		}
		*field.value, err = resolveEnvironmentReference(field.name, *field.value)
		if err != nil {
			return Config{}, err
		}
	}
	if err := config.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate config: %w", err)
	}

	return config, nil
}

// rejectExplicitTokenizerDefaults distinguishes an omitted scalar (which is
// defaulted) from an explicitly supplied zero value. yaml.v3 does not retain
// that distinction in an ordinary value struct, so inspect only the small
// tokenizer section before applying defaults. Unknown fields are still
// rejected by the KnownFields decoder above.
func rejectExplicitTokenizerDefaults(data []byte, config Config) error {
	var raw struct {
		Tokenizer map[string]yaml.Node `yaml:"tokenizer"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("decode tokenizer YAML: %w", err)
	}
	if _, ok := raw.Tokenizer["mode"]; ok && config.Tokenizer.Mode == "" {
		return fmt.Errorf("tokenizer mode must be usage_only or estimate")
	}
	if _, ok := raw.Tokenizer["max_inspected_request_bytes"]; ok && config.Tokenizer.MaxInspectedRequestBytes == 0 {
		return fmt.Errorf("tokenizer max inspected request bytes must be positive")
	}
	if _, ok := raw.Tokenizer["fallback_unknown_input_tokens"]; ok && config.Tokenizer.FallbackUnknownInputTokens == 0 {
		return fmt.Errorf("tokenizer fallback unknown input tokens must be positive")
	}
	if _, ok := raw.Tokenizer["fallback_max_output_tokens"]; ok && config.Tokenizer.FallbackMaxOutputTokens == 0 {
		return fmt.Errorf("tokenizer fallback max output tokens must be positive")
	}
	return nil
}

func isEnvironmentReference(value string) bool {
	return strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}")
}

func resolveEnvironmentReference(field, value string) (string, error) {
	if !strings.Contains(value, "${") {
		return value, nil
	}
	if !strings.HasPrefix(value, "${") || !strings.HasSuffix(value, "}") {
		return "", fmt.Errorf("configuration field %q has an invalid environment reference", field)
	}

	name := strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")
	if !validEnvironmentName(name) {
		return "", fmt.Errorf("configuration field %q has an invalid environment reference", field)
	}

	resolved, ok := os.LookupEnv(name)
	if !ok {
		return "", fmt.Errorf("configuration field %q environment variable %q is not set", field, name)
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
