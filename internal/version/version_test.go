package version

import (
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
