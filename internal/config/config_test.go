package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigValidate(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "gateway.db")
	tests := []struct {
		name    string
		config  Config
		wantErr string
	}{
		{
			name: "valid configuration",
			config: Config{
				ListenAddr:      ":8080",
				UpstreamBaseURL: "http://router.example.test/v1",
				UpstreamAPIKey:  "secret",
				SQLitePath:      databasePath,
				AuthPepper:      "pepper",
				AdminCredential: "admin",
			},
		},
		{
			name: "missing listen address",
			config: Config{
				UpstreamBaseURL: "http://router.example.test",
				UpstreamAPIKey:  "secret",
				SQLitePath:      databasePath,
				AuthPepper:      "pepper",
				AdminCredential: "admin",
			},
			wantErr: "listen address is required",
		},
		{
			name: "missing upstream URL",
			config: Config{
				ListenAddr:      ":8080",
				UpstreamAPIKey:  "secret",
				SQLitePath:      databasePath,
				AuthPepper:      "pepper",
				AdminCredential: "admin",
			},
			wantErr: "upstream base URL is required",
		},
		{
			name: "invalid upstream URL",
			config: Config{
				ListenAddr:      ":8080",
				UpstreamBaseURL: "not a URL",
				UpstreamAPIKey:  "secret",
				SQLitePath:      databasePath,
				AuthPepper:      "pepper",
				AdminCredential: "admin",
			},
			wantErr: "invalid upstream base URL",
		},
		{
			name: "unsupported upstream URL scheme",
			config: Config{
				ListenAddr:      ":8080",
				UpstreamBaseURL: "ftp://router.example.test",
				UpstreamAPIKey:  "secret",
				SQLitePath:      databasePath,
				AuthPepper:      "pepper",
				AdminCredential: "admin",
			},
			wantErr: "scheme must be http or https",
		},
		{
			name: "missing upstream URL host",
			config: Config{
				ListenAddr:      ":8080",
				UpstreamBaseURL: "http://",
				UpstreamAPIKey:  "secret",
				SQLitePath:      databasePath,
				AuthPepper:      "pepper",
				AdminCredential: "admin",
			},
			wantErr: "host is required",
		},
		{
			name: "missing upstream API key",
			config: Config{
				ListenAddr:      ":8080",
				UpstreamBaseURL: "http://router.example.test",
				SQLitePath:      databasePath,
				AuthPepper:      "pepper",
				AdminCredential: "admin",
			},
			wantErr: "upstream API key is required",
		},
		{
			name:    "missing sqlite path",
			config:  Config{ListenAddr: ":8080", UpstreamBaseURL: "http://router.example.test", UpstreamAPIKey: "secret", AuthPepper: "pepper", AdminCredential: "admin"},
			wantErr: "sqlite path is required",
		},
		{
			name:    "missing auth pepper",
			config:  Config{ListenAddr: ":8080", UpstreamBaseURL: "http://router.example.test", UpstreamAPIKey: "secret", SQLitePath: databasePath, AdminCredential: "admin"},
			wantErr: "auth pepper is required",
		},
		{
			name:    "missing admin credential",
			config:  Config{ListenAddr: ":8080", UpstreamBaseURL: "http://router.example.test", UpstreamAPIKey: "secret", SQLitePath: databasePath, AuthPepper: "pepper"},
			wantErr: "admin credential is required",
		},
		{
			name:    "admin credential must be distinct",
			config:  Config{ListenAddr: ":8080", UpstreamBaseURL: "http://router.example.test", UpstreamAPIKey: "same", SQLitePath: databasePath, AuthPepper: "pepper", AdminCredential: "same"},
			wantErr: "admin credential must differ from upstream API key",
		},
		{
			name:    "directory is not a database path",
			config:  Config{ListenAddr: ":8080", UpstreamBaseURL: "http://router.example.test", UpstreamAPIKey: "secret", SQLitePath: t.TempDir(), AuthPepper: "pepper", AdminCredential: "admin"},
			wantErr: "sqlite path: is a directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}

			if err == nil || !contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateSQLitePath(t *testing.T) {
	validFile := filepath.Join(t.TempDir(), "nested", "gateway.db")
	if err := os.Mkdir(filepath.Dir(validFile), 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "memory", path: ":memory:"},
		{name: "new file", path: validFile},
		{name: "missing parent", path: filepath.Join(t.TempDir(), "missing", "gateway.db"), want: "parent directory does not exist"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSQLitePath(tt.path)
			if tt.want == "" && err != nil {
				t.Fatalf("validateSQLitePath() error = %v, want nil", err)
			}
			if tt.want != "" && (err == nil || !contains(err.Error(), tt.want)) {
				t.Fatalf("validateSQLitePath() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func contains(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}
