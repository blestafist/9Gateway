package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadValidYAML(t *testing.T) {
	path := writeConfig(t, "listen_addr: :8080\nupstream_base_url: https://router.example.test/v1\nupstream_api_key: secret\n")

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}

	want := Config{
		ListenAddr:      ":8080",
		UpstreamBaseURL: "https://router.example.test/v1",
		UpstreamAPIKey:  "secret",
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
	path := writeConfig(t, "listen_addr: :8080\nupstream_api_key: secret\n")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "upstream base URL is required") {
		t.Fatalf("Load() error = %v, want missing upstream URL error", err)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := writeConfig(t, "listen_addr: :8080\nupstream_base_url: http://router.example.test\nupstream_api_key: secret\nunknown: value\n")

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "decode config YAML") {
		t.Fatalf("Load() error = %v, want unknown field error", err)
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
