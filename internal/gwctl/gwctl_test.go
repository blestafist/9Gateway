package gwctl

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pestit/9gateway/internal/httpserver"
	"github.com/pestit/9gateway/internal/storage"
	"github.com/pestit/9gateway/internal/transport"
)

func TestRunVersionHelpAndUnknownCommand(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		status     int
		stdoutPart string
		stderrPart string
	}{
		{name: "version ignores gateway and credential", args: []string{"--gateway-url", ":not-a-url", "version"}, status: ExitSuccess, stdoutPart: "gwctl version "},
		{name: "help", args: []string{"--help"}, status: ExitSuccess, stdoutPart: "Usage: gwctl", stderrPart: ""},
		{name: "help after command", args: []string{"ping", "--help"}, status: ExitSuccess, stdoutPart: "Commands:"},
		{name: "unknown", args: []string{"wat"}, status: ExitUsage, stderrPart: `unknown command "wat"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			status := Run(context.Background(), test.args, &stdout, &stderr)
			if status != test.status {
				t.Fatalf("status = %d, want %d (stdout %q, stderr %q)", status, test.status, stdout.String(), stderr.String())
			}
			if !strings.Contains(stdout.String(), test.stdoutPart) {
				t.Fatalf("stdout = %q, want %q", stdout.String(), test.stdoutPart)
			}
			if !strings.Contains(stderr.String(), test.stderrPart) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.stderrPart)
			}
		})
	}
}

func TestRunPingAgainstRealGateway(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	handler, err := newAdminTestGateway(database, "integration-admin")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	status := Run(context.Background(), []string{"--gateway-url", server.URL, "--admin-credential", "integration-admin", "ping"}, &stdout, &stderr)
	if status != ExitSuccess {
		t.Fatalf("status = %d, stdout %q, stderr %q", status, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "authentication succeeded") || stderr.Len() != 0 {
		t.Fatalf("stdout/stderr = %q/%q", stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	status = Run(context.Background(), []string{"--gateway-url", server.URL, "--admin-credential", "wrong-secret", "ping"}, &stdout, &stderr)
	if status != ExitAPI || !strings.Contains(stderr.String(), "authentication failed") || stdout.Len() != 0 {
		t.Fatalf("wrong credential result = %d/%q/%q", status, stdout.String(), stderr.String())
	}
}

func TestRunPingAuthenticationNetworkAndValidationErrors(t *testing.T) {
	t.Setenv("GWCTL_ADMIN_CREDENTIAL", "")
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/admin/v1/keys" || request.URL.RawQuery != "limit=1" {
			t.Errorf("request target = %s?%s", request.URL.Path, request.URL.RawQuery)
		}
		response.WriteHeader(http.StatusUnauthorized)
	}))
	serverURL := server.URL
	defer server.Close()

	for _, test := range []struct {
		name       string
		args       []string
		status     int
		stderrPart string
	}{
		{name: "wrong credential", args: []string{"--gateway-url", serverURL, "--admin-credential", "wrong-secret", "ping"}, status: ExitAPI, stderrPart: "authentication failed"},
		{name: "connection", args: []string{"--gateway-url", "http://127.0.0.1:1", "--admin-credential", "network-secret", "ping"}, status: ExitAPI, stderrPart: "connection failed"},
		{name: "missing credential", args: []string{"--gateway-url", "http://127.0.0.1:1", "ping"}, status: ExitUsage, stderrPart: "admin credential is required"},
		{name: "bad URL", args: []string{"--gateway-url", "file:///tmp/gateway", "--admin-credential", "secret", "ping"}, status: ExitUsage, stderrPart: "HTTP or HTTPS"},
		{name: "URL query", args: []string{"--gateway-url", "http://localhost:8080/base?secret=x", "--admin-credential", "secret", "ping"}, status: ExitUsage, stderrPart: "query"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			status := Run(context.Background(), test.args, &stdout, &stderr)
			if status != test.status {
				t.Fatalf("status = %d, want %d (stdout %q, stderr %q)", status, test.status, stdout.String(), stderr.String())
			}
			if !strings.Contains(stderr.String(), test.stderrPart) || stdout.Len() != 0 {
				t.Fatalf("stdout/stderr = %q/%q", stdout.String(), stderr.String())
			}
			if strings.Contains(stderr.String(), "wrong-secret") || strings.Contains(stderr.String(), "network-secret") {
				t.Fatalf("credential leaked in stderr: %q", stderr.String())
			}
		})
	}
}

func TestRunPingUsesEnvironmentCredentialAndExplicitPrecedence(t *testing.T) {
	database, err := storage.Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	handler, err := newAdminTestGateway(database, "explicit")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	t.Setenv("GWCTL_ADMIN_CREDENTIAL", "explicit")
	var stdout, stderr bytes.Buffer
	if status := Run(context.Background(), []string{"--gateway-url", server.URL, "ping"}, &stdout, &stderr); status != ExitSuccess {
		t.Fatalf("environment credential status = %d, stdout %q, stderr %q", status, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if status := Run(context.Background(), []string{"--gateway-url", server.URL, "--admin-credential", "wrong", "ping"}, &stdout, &stderr); status != ExitAPI || !strings.Contains(stderr.String(), "authentication failed") {
		t.Fatalf("explicit credential result = %d/%q/%q", status, stdout.String(), stderr.String())
	}
}

func TestValidateBaseURLAndJoinURLPreserveBasePath(t *testing.T) {
	for _, raw := range []string{"", "localhost:8080", "ftp://localhost", "http://localhost/path?x=1", "http://user:pass@localhost/path", "http://localhost/path#fragment"} {
		if _, err := validateBaseURL(raw); err == nil {
			t.Errorf("validateBaseURL(%q) succeeded", raw)
		}
	}
	base, err := validateBaseURL("http://localhost:8080/tenant/api/")
	if err != nil {
		t.Fatal(err)
	}
	joined := joinURL(base, adminKeysPath)
	if got, want := joined.String(), "http://localhost:8080/tenant/api/admin/v1/keys?limit=1"; got != want {
		t.Fatalf("joined URL = %q, want %q", got, want)
	}
}

func newAdminTestGateway(database *storage.DB, credential string) (http.Handler, error) {
	return httpserver.NewHandlerWithAdmin(transport.NewClient(), "http://127.0.0.1:1", "upstream", credential, "pepper", storage.NewAPIKeyRepository(database))
}
