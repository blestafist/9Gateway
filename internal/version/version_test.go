package version

import (
	"runtime"
	"strings"
	"testing"
)

func TestMetricLabelsNormalizeAndBoundLdflags(t *testing.T) {
	oldVersion, oldCommit, oldBuild, oldGo := Version, CommitSHA, BuildDate, GoVersion
	t.Cleanup(func() { Version, CommitSHA, BuildDate, GoVersion = oldVersion, oldCommit, oldBuild, oldGo })
	Version = " release\n\"secret\" " + strings.Repeat("x", 100)
	CommitSHA = strings.Repeat("c", 100)
	BuildDate = "2026-01-01T00:00:00Z\r\nAuthorization: secret"
	GoVersion = "go1.25.0\x00unsafe"

	labels := MetricLabels()
	for name, value := range map[string]string{
		"version": labels.Version, "commit": labels.Commit, "build date": labels.BuildDate, "go version": labels.GoVersion,
	} {
		if len(value) == 0 || len(value) > metricValueLimit {
			t.Errorf("%s length = %d, want 1..%d", name, len(value), metricValueLimit)
		}
		if strings.ContainsAny(value, "\r\n\\\"") {
			t.Errorf("%s contains exposition-sensitive characters: %q", name, value)
		}
	}
	if strings.Contains(labels.Version, "secret") || strings.Contains(labels.BuildDate, "Authorization") {
		t.Fatalf("sensitive ldflag text survived normalization: %#v", labels)
	}
}

func TestMetricLabelsUseStrictAllowLists(t *testing.T) {
	oldVersion, oldCommit, oldBuild, oldGo := Version, CommitSHA, BuildDate, GoVersion
	t.Cleanup(func() { Version, CommitSHA, BuildDate, GoVersion = oldVersion, oldCommit, oldBuild, oldGo })
	Version = "v1.2.3"
	CommitSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	BuildDate = "2026-01-02T03:04:05Z"
	GoVersion = "go1.25.1"
	labels := MetricLabels()
	if labels.Version != Version || labels.Commit != CommitSHA[:7] || labels.BuildDate != BuildDate || labels.GoVersion != GoVersion || labels.OS != runtime.GOOS || labels.Arch != runtime.GOARCH {
		t.Fatalf("valid production metadata = %#v", labels)
	}

	for _, value := range []string{
		"ghp_0123456789abcdef0123456789abcdef0123456789abcdef",
		"eyJhbGciOiJIUzI1NiJ9.opaque.payload",
		"opaque-build-value-without-a-denylist-word",
	} {
		Version, CommitSHA, BuildDate, GoVersion = value, value, value, value
		labels := MetricLabels()
		if labels.Version != defaultVersion || labels.Commit != defaultCommit || labels.BuildDate != defaultBuild || labels.GoVersion != "unknown" {
			t.Fatalf("arbitrary metadata %q survived: %#v", value, labels)
		}
	}
}

func TestPublicVersionFormats(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "dev", value: "dev", want: "dev"},
		{name: "semver", value: "v1.2.3", want: "v1.2.3"},
		{name: "semver without v", value: "1.2.3", want: "1.2.3"},
		{name: "alpha", value: "v1.2.3-alpha1", want: "v1.2.3-alpha1"},
		{name: "alpha dotted", value: "v1.2.3-alpha.1", want: "v1.2.3-alpha.1"},
		{name: "beta", value: "v1.2.3-beta2", want: "v1.2.3-beta2"},
		{name: "rc", value: "v1.2.3-rc.3", want: "v1.2.3-rc.3"},
		{name: "trimmed", value: " v1.2.3 ", want: "v1.2.3"},
		{name: "access key canary", value: "v1.2.3-AKIA1234567890", want: defaultVersion},
		{name: "arbitrary prerelease", value: "v1.2.3-password-abc", want: defaultVersion},
		{name: "build metadata", value: "v1.2.3+build.1", want: defaultVersion},
		{name: "bare prerelease", value: "v1.2.3-alpha", want: defaultVersion},
		{name: "leading zero prerelease", value: "v1.2.3-rc.01", want: defaultVersion},
		{name: "devel", value: "devel", want: defaultVersion},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := publicVersion(test.value); got != test.want {
				t.Fatalf("publicVersion(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}

func TestPublicGoVersionFormats(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "release", value: "go1.25.1", want: "go1.25.1"},
		{name: "beta", value: "go1.25beta1", want: "go1.25beta1"},
		{name: "rc", value: "go1.25rc1", want: "go1.25rc1"},
		{name: "beta zero", value: "go1.25beta0", want: "go1.25beta0"},
		{name: "minor only", value: "go1.25", want: "unknown"},
		{name: "password suffix canary", value: "go1.25.1-password-abc", want: "unknown"},
		{name: "custom toolchain suffix canary", value: "go1.25.1-X:nodwarf5", want: "unknown"},
		{name: "build metadata", value: "go1.25.1+build.1", want: "unknown"},
		{name: "patch beta is not runtime form", value: "go1.25.1beta1", want: "unknown"},
		{name: "dotted beta is not runtime form", value: "go1.25beta.1", want: "unknown"},
		{name: "leading zero beta", value: "go1.25beta01", want: "unknown"},
		{name: "devel suffix", value: "devel go1.25-abcdef", want: "unknown"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := publicGoVersion(test.value); got != test.want {
				t.Fatalf("publicGoVersion(%q) = %q, want %q", test.value, got, test.want)
			}
		})
	}
}
