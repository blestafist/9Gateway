package config

import "testing"

func TestConfigValidate(t *testing.T) {
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
			},
		},
		{
			name: "missing listen address",
			config: Config{
				UpstreamBaseURL: "http://router.example.test",
				UpstreamAPIKey:  "secret",
			},
			wantErr: "listen address is required",
		},
		{
			name: "missing upstream URL",
			config: Config{
				ListenAddr:     ":8080",
				UpstreamAPIKey: "secret",
			},
			wantErr: "upstream base URL is required",
		},
		{
			name: "invalid upstream URL",
			config: Config{
				ListenAddr:      ":8080",
				UpstreamBaseURL: "not a URL",
				UpstreamAPIKey:  "secret",
			},
			wantErr: "invalid upstream base URL",
		},
		{
			name: "unsupported upstream URL scheme",
			config: Config{
				ListenAddr:      ":8080",
				UpstreamBaseURL: "ftp://router.example.test",
				UpstreamAPIKey:  "secret",
			},
			wantErr: "scheme must be http or https",
		},
		{
			name: "missing upstream URL host",
			config: Config{
				ListenAddr:      ":8080",
				UpstreamBaseURL: "http://",
				UpstreamAPIKey:  "secret",
			},
			wantErr: "host is required",
		},
		{
			name: "missing upstream API key",
			config: Config{
				ListenAddr:      ":8080",
				UpstreamBaseURL: "http://router.example.test",
			},
			wantErr: "upstream API key is required",
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

func contains(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}
