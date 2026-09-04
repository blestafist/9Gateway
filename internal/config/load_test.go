package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadValidYAML(t *testing.T) {
	t.Setenv("TEST_AUTH_PEPPER", "pepper-from-environment")
	t.Setenv("TEST_ADMIN_CREDENTIAL", "admin-from-environment")
	path := writeConfig(t, "listen_addr: :8080\nupstream_base_url: https://router.example.test/v1\nupstream_api_key: secret\nsqlite_path: ':memory:'\nauth_pepper: ${TEST_AUTH_PEPPER}\nadmin_credential: ${TEST_ADMIN_CREDENTIAL}\n")

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	want := Config{
		ListenAddr:      ":8080",
		UpstreamBaseURL: "https://router.example.test/v1",
		UpstreamAPIKey:  "secret",
		SQLitePath:      ":memory:",
		AuthPepper:      "pepper-from-environment",
		AdminCredential: "admin-from-environment",
	}
	if got != want {
		t.Fatalf("Load() = %+v, want %+v", got, want)
	}
}

func TestLoadMalformedYAML(t *testing.T) {
	path := writeConfig(t, "listen_addr: [8080\nupstream_base_url: http://router.example.test\nupstream_api_key: secret\n")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "decode config YAML") {
		t.Fatalf("Load() error = %v, want a YAML decoding error", err)
	}
}

func TestLoadMissingUpstreamURL(t *testing.T) {
	t.Setenv("TEST_AUTH_PEPPER", "pepper")
	t.Setenv("TEST_ADMIN_CREDENTIAL", "admin")
	path := writeConfig(t, "listen_addr: :8080\nupstream_api_key: secret\nsqlite_path: ':memory:'\nauth_pepper: ${TEST_AUTH_PEPPER}\nadmin_credential: ${TEST_ADMIN_CREDENTIAL}\n")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "upstream base URL is required") {
		t.Fatalf("Load() error = %v, want missing upstream URL error", err)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	t.Setenv("TEST_AUTH_PEPPER", "pepper")
	t.Setenv("TEST_ADMIN_CREDENTIAL", "admin")
	path := writeConfig(t, "listen_addr: :8080\nupstream_base_url: http://router.example.test\nupstream_api_key: secret\nsqlite_path: ':memory:'\nauth_pepper: ${TEST_AUTH_PEPPER}\nadmin_credential: ${TEST_ADMIN_CREDENTIAL}\nunknown: value\n")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "decode config YAML") {
		t.Fatalf("Load() error = %v, want unknown field error", err)
	}
}

func TestLoadResolvesUpstreamAPIKeyFromEnvironment(t *testing.T) {
	t.Setenv("TEST_UPSTREAM_API_KEY", "secret-from-environment")
	t.Setenv("TEST_AUTH_PEPPER", "pepper")
	t.Setenv("TEST_ADMIN_CREDENTIAL", "admin")
	path := writeConfig(t, "listen_addr: :8080\nupstream_base_url: http://router.example.test\nupstream_api_key: ${TEST_UPSTREAM_API_KEY}\nsqlite_path: ':memory:'\nauth_pepper: ${TEST_AUTH_PEPPER}\nadmin_credential: ${TEST_ADMIN_CREDENTIAL}\n")

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if got.UpstreamAPIKey != "secret-from-environment" {
		t.Fatalf("Load() upstream API key = %q, want resolved secret", got.UpstreamAPIKey)
	}
}

func TestLoadFailsWhenUpstreamAPIKeyEnvironmentVariableIsMissing(t *testing.T) {
	os.Unsetenv("TEST_MISSING_UPSTREAM_API_KEY")
	t.Setenv("TEST_AUTH_PEPPER", "pepper")
	t.Setenv("TEST_ADMIN_CREDENTIAL", "admin")
	path := writeConfig(t, "listen_addr: :8080\nupstream_base_url: http://router.example.test\nupstream_api_key: ${TEST_MISSING_UPSTREAM_API_KEY}\nsqlite_path: ':memory:'\nauth_pepper: ${TEST_AUTH_PEPPER}\nadmin_credential: ${TEST_ADMIN_CREDENTIAL}\n")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "upstream_api_key") || !strings.Contains(err.Error(), "environment variable \"TEST_MISSING_UPSTREAM_API_KEY\" is not set") {
		t.Fatalf("Load() error = %v, want missing environment variable error", err)
	}
}

func TestLoadSecretEnvironmentReferences(t *testing.T) {
	tests := []struct {
		name  string
		yaml  string
		setup func(*testing.T)
		field string
	}{
		{name: "missing pepper", yaml: "auth_pepper: ${MISSING_PEPPER}\nadmin_credential: ${VALID_ADMIN}", setup: func(t *testing.T) { t.Setenv("VALID_ADMIN", "admin") }, field: "auth_pepper"},
		{name: "empty pepper", yaml: "auth_pepper: ${EMPTY_PEPPER}\nadmin_credential: ${VALID_ADMIN}", setup: func(t *testing.T) { t.Setenv("EMPTY_PEPPER", ""); t.Setenv("VALID_ADMIN", "admin") }, field: "auth pepper"},
		{name: "literal pepper rejected", yaml: "auth_pepper: pepper\nadmin_credential: ${VALID_ADMIN}", setup: func(t *testing.T) { t.Setenv("VALID_ADMIN", "admin") }, field: "auth_pepper"},
		{name: "literal admin rejected", yaml: "auth_pepper: ${VALID_PEPPER}\nadmin_credential: admin", setup: func(t *testing.T) { t.Setenv("VALID_PEPPER", "pepper") }, field: "admin_credential"},
		{name: "malformed admin reference", yaml: "auth_pepper: ${VALID_PEPPER}\nadmin_credential: ${ADMIN", setup: func(t *testing.T) { t.Setenv("VALID_PEPPER", "pepper") }, field: "admin_credential"},
		{name: "malformed upstream reference", yaml: "upstream_api_key: prefix${UPSTREAM}", setup: func(t *testing.T) { t.Setenv("VALID_PEPPER", "pepper"); t.Setenv("VALID_ADMIN", "admin") }, field: "upstream_api_key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup(t)
			}
			contents := "listen_addr: :8080\nupstream_base_url: http://router.example.test\nupstream_api_key: upstream\nsqlite_path: ':memory:'\n" + tt.yaml + "\n"
			if tt.name == "malformed upstream reference" {
				contents += "auth_pepper: ${VALID_PEPPER}\nadmin_credential: ${VALID_ADMIN}\n"
			}
			_, err := Load(writeConfig(t, contents))
			if err == nil || !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("Load() error = %v, want field %q", err, tt.field)
			}
			if strings.Contains(err.Error(), "pepper") && strings.Contains(err.Error(), "secret") {
				t.Fatalf("Load() error appears to contain a resolved secret: %v", err)
			}
		})
	}
}

func TestLoadResolvesAllCredentials(t *testing.T) {
	t.Setenv("TEST_PEPPER", "resolved-pepper")
	t.Setenv("TEST_ADMIN", "resolved-admin")
	path := writeConfig(t, "listen_addr: :8080\nupstream_base_url: http://router.example.test\nupstream_api_key: ${TEST_UPSTREAM}\nsqlite_path: ':memory:'\nauth_pepper: ${TEST_PEPPER}\nadmin_credential: ${TEST_ADMIN}\n")
	t.Setenv("TEST_UPSTREAM", "resolved-upstream")
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if got.UpstreamAPIKey != "resolved-upstream" || got.AuthPepper != "resolved-pepper" || got.AdminCredential != "resolved-admin" {
		t.Fatalf("Load() did not resolve credentials: %+v", got)
	}
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
