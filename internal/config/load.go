package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load reads, decodes, and validates configuration from a YAML file.
func Load(path string) (Config, error) {
	if path == "" {
		return Config{}, ValidationError("config", "path is required")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, ValidationError("config", fmt.Sprintf("read config: %v", err))
	}

	var config Config
	if err := validateRawObservability(data); err != nil {
		return Config{}, ValidationError("observability", err.Error())
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return Config{}, ValidationError("config", fmt.Sprintf("decode config YAML: %v", err))
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Config{}, ValidationError("config", "decode config YAML: multiple documents are not supported")
		}
		return Config{}, ValidationError("config", fmt.Sprintf("decode config YAML: %v", err))
	}
	if err := rejectExplicitTokenizerDefaults(data, config); err != nil {
		return Config{}, ValidationError("tokenizer", err.Error())
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
			return Config{}, ValidationError(field.name, "must be an environment reference")
		}
		*field.value, err = resolveEnvironmentReference(field.name, *field.value)
		if err != nil {
			return Config{}, err
		}
	}
	if err := config.Validate(); err != nil {
		return Config{}, err
	}

	return config, nil
}

// ValidationError is the stable, secret-safe startup error shape. Callers may
// include the returned error directly in logs without exposing the decoded
// configuration or any resolved credential.
func ValidationError(field, reason string) error {
	return fmt.Errorf("config validation failed: field '%s': %s", field, reason)
}

// validateRawObservability checks the scalar representation before yaml.v3
// decodes it into zero-valued Go fields. This is necessary to distinguish an
// omitted field from null and from an explicitly supplied zero (only the body
// byte limit permits zero), while keeping errors limited to field names.
func validateRawObservability(data []byte) error {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("decode config YAML: %w", err)
	}
	if len(document.Content) == 0 {
		return nil
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil // The strict decoder reports the useful structural error.
	}
	for index := 0; index+1 < len(root.Content); index += 2 {
		if root.Content[index].Value != "observability" {
			continue
		}
		observability := root.Content[index+1]
		if isYAMLNull(observability) {
			return fmt.Errorf("observability must be a mapping, not null")
		}
		if observability.Kind != yaml.MappingNode {
			return fmt.Errorf("observability must be a mapping")
		}
		for fieldIndex := 0; fieldIndex+1 < len(observability.Content); fieldIndex += 2 {
			field := observability.Content[fieldIndex].Value
			value := observability.Content[fieldIndex+1]
			switch field {
			case "telemetry_queue_capacity", "max_captured_body_bytes", "request_retention_seconds", "body_retention_seconds":
				if isYAMLNull(value) {
					return fmt.Errorf("observability.%s must not be null", field)
				}
				if value.Kind != yaml.ScalarNode || value.Tag != "!!int" {
					return fmt.Errorf("observability.%s must be an integer number of seconds or bytes", field)
				}
				parsed, err := strconv.ParseInt(value.Value, 10, 64)
				if err != nil {
					return fmt.Errorf("observability.%s is outside the supported integer range", field)
				}
				if field != "max_captured_body_bytes" && parsed == 0 {
					return fmt.Errorf("observability.%s must be positive", field)
				}
			case "":
				return fmt.Errorf("observability field name must not be empty")
			default:
				return fmt.Errorf("observability.%s is unknown", field)
			}
		}
		return nil
	}
	return nil
}

func isYAMLNull(node *yaml.Node) bool {
	return node.Kind == yaml.ScalarNode && node.Tag == "!!null"
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
		return "", ValidationError(field, "has an invalid environment reference")
	}

	name := strings.TrimSuffix(strings.TrimPrefix(value, "${"), "}")
	if !validEnvironmentName(name) {
		return "", ValidationError(field, "has an invalid environment reference")
	}

	resolved, ok := os.LookupEnv(name)
	if !ok {
		return "", ValidationError(field, fmt.Sprintf("environment variable '%s' not set", name))
	}
	if strings.TrimSpace(resolved) == "" {
		return "", ValidationError(field, fmt.Sprintf("environment variable '%s' is empty", name))
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
