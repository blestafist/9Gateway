// Package version contains build metadata exposed by the gateway binaries.
package version

import (
	"fmt"
	"io"
	"runtime"
	"strings"
	"unicode"
)

const (
	defaultVersion = "dev"
	defaultCommit  = "unknown"
	defaultBuild   = "unknown"
)

// String metadata is replaced by release builds with -ldflags -X. GoVersion
// is runtime-derived so it reliably describes the Go toolchain in use.
var (
	Version   = defaultVersion
	CommitSHA = defaultCommit
	BuildDate = defaultBuild
	GoVersion = runtime.Version()
)

// Metadata is normalized, presentation-ready build information.
type Metadata struct {
	Version, Commit, BuildDate, GoVersion, OS, Arch string
}

// Current returns safe metadata for this process.
func Current() Metadata {
	return Metadata{
		Version:   valueOrDefault(Version, defaultVersion),
		Commit:    ShortCommit(CommitSHA),
		BuildDate: valueOrDefault(BuildDate, defaultBuild),
		GoVersion: valueOrDefault(GoVersion, runtime.Version()),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
	}
}

// MetricLabels returns bounded, exposition-safe build metadata. Build fields
// are supplied by release ldflags, so do not place their raw values in a
// Prometheus label: malformed values cannot break a scrape, create unbounded
// label values, or carry control characters into the exposition.
func MetricLabels() Metadata {
	metadata := Current()
	return Metadata{
		Version:   metricValue(metadata.Version),
		Commit:    metricValue(metadata.Commit),
		BuildDate: metricValue(metadata.BuildDate),
		GoVersion: metricValue(metadata.GoVersion),
		OS:        metricValue(metadata.OS),
		Arch:      metricValue(metadata.Arch),
	}
}

const metricValueLimit = 64

func metricValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	lower := strings.ToLower(value)
	for _, sensitive := range []string{"secret", "password", "token", "bearer", "authorization", "api_key", "apikey", "credential"} {
		if strings.Contains(lower, sensitive) {
			return "unknown"
		}
	}
	var builder strings.Builder
	for _, character := range value {
		if builder.Len() >= metricValueLimit {
			break
		}
		if character < unicode.MaxASCII && (character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("._+-", character)) {
			builder.WriteRune(character)
		} else {
			builder.WriteByte('_')
		}
	}
	if builder.Len() == 0 {
		return "unknown"
	}
	return builder.String()
}

// ShortCommit returns a conventional seven-character commit identifier.
func ShortCommit(commit string) string {
	commit = strings.TrimSpace(commit)
	if commit == "" {
		return defaultCommit
	}
	if commit == defaultCommit || len(commit) <= 7 {
		return commit
	}
	return commit[:7]
}

// Format writes the common, secret-free version report used by both binaries.
func Format(writer io.Writer, program string) {
	metadata := Current()
	if strings.TrimSpace(program) == "" {
		program = "gateway"
	}
	fmt.Fprintf(writer, "%s version %s\ncommit: %s\nbuild date: %s\ngo version: %s\nos/arch: %s/%s\n",
		program, metadata.Version, metadata.Commit, metadata.BuildDate,
		metadata.GoVersion, metadata.OS, metadata.Arch)
}

func valueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value == "" {
		return fallback
	}
	return value
}
