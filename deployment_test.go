package deployment_test

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func readDeploymentFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestComposeDeploymentContract(t *testing.T) {
	var compose struct {
		Services map[string]struct {
			Build     any                 `yaml:"build"`
			Image     string              `yaml:"image"`
			Ports     []string            `yaml:"ports"`
			Profiles  []string            `yaml:"profiles"`
			Volumes   []string            `yaml:"volumes"`
			DependsOn map[string]struct{} `yaml:"depends_on"`
		} `yaml:"services"`
		Volumes map[string]any `yaml:"volumes"`
	}
	if err := yaml.Unmarshal(readDeploymentFile(t, "docker-compose.yml"), &compose); err != nil {
		t.Fatalf("docker-compose.yml: %v", err)
	}
	for _, service := range []string{"gateway", "mock-upstream", "prometheus"} {
		if _, ok := compose.Services[service]; !ok {
			t.Fatalf("compose service %q is missing", service)
		}
	}
	if compose.Services["gateway"].Image != "9gateway:local" || len(compose.Services["gateway"].Ports) != 1 || compose.Services["gateway"].Ports[0] != "8080:8080" {
		t.Fatalf("gateway image/port contract is incorrect: %+v", compose.Services["gateway"])
	}
	if len(compose.Services["prometheus"].Profiles) != 1 || compose.Services["prometheus"].Profiles[0] != "observability" {
		t.Fatalf("prometheus must be optional observability profile")
	}
	if _, ok := compose.Volumes["gateway-data"]; !ok {
		t.Fatal("gateway-data named volume is missing")
	}
	for _, required := range []string{"UPSTREAM_API_KEY", "ADMIN_CREDENTIAL", "AUTH_PEPPER", "gateway-data", "mock-upstream", "8081"} {
		if !strings.Contains(string(readDeploymentFile(t, "docker-compose.yml")), required) {
			t.Errorf("compose missing %q", required)
		}
	}
}

func TestExampleConfigurationContract(t *testing.T) {
	var config map[string]any
	if err := yaml.Unmarshal(readDeploymentFile(t, "config.example.yaml"), &config); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"listen_addr", "upstream_base_url", "upstream_api_key", "sqlite_path", "auth_pepper", "admin_credential"} {
		if _, ok := config[key]; !ok {
			t.Errorf("config.example.yaml missing %q", key)
		}
	}
	if config["upstream_base_url"] != "http://mock-upstream:8081" {
		t.Errorf("upstream URL = %v", config["upstream_base_url"])
	}
	for _, secret := range []string{"upstream_api_key", "auth_pepper", "admin_credential"} {
		value, ok := config[secret].(string)
		if !ok || !strings.HasPrefix(value, "${") || !strings.HasSuffix(value, "}") {
			t.Errorf("%s must be an environment reference, got %v", secret, config[secret])
		}
	}
}

func TestPrometheusConfigurationContract(t *testing.T) {
	var config struct {
		Global struct {
			ScrapeInterval string `yaml:"scrape_interval"`
		} `yaml:"global"`
		ScrapeConfigs []struct {
			MetricsPath   string `yaml:"metrics_path"`
			StaticConfigs []struct {
				Targets []string `yaml:"targets"`
			} `yaml:"static_configs"`
		} `yaml:"scrape_configs"`
	}
	if err := yaml.Unmarshal(readDeploymentFile(t, "prometheus.yml"), &config); err != nil {
		t.Fatal(err)
	}
	if config.Global.ScrapeInterval != "15s" || len(config.ScrapeConfigs) != 1 || config.ScrapeConfigs[0].MetricsPath != "/metrics" || config.ScrapeConfigs[0].StaticConfigs[0].Targets[0] != "gateway:8080" {
		t.Fatalf("unexpected Prometheus configuration: %+v", config)
	}
}
