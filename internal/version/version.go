// Package version contains build metadata exposed by the gateway binaries.
package version

import (
	"fmt"
	"io"
	"regexp"
	"runtime"
	"strings"
	"time"
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
		Version:   publicVersion(Version),
		Commit:    ShortCommit(CommitSHA),
		BuildDate: publicBuildDate(BuildDate),
		GoVersion: publicGoVersion(GoVersion),
		OS:        publicOS(runtime.GOOS),
		Arch:      publicArch(runtime.GOARCH),
	}
}

// MetricLabels returns bounded, exposition-safe build metadata. Build fields
// are supplied by release ldflags, so do not place their raw values in a
// Prometheus label: malformed values cannot break a scrape, create unbounded
// label values, or carry control characters into the exposition.
func MetricLabels() Metadata {
	return Current()
}

const metricValueLimit = 64

var (
	versionPattern   = regexp.MustCompile(`^(?:dev|v?(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-(?:alpha|beta|rc)\.?(?:0|[1-9][0-9]*))?)$`)
	commitPattern    = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)
	goVersionPattern = regexp.MustCompile(`^go[1-9][0-9]*\.(?:0|[1-9][0-9]*)(?:\.(?:0|[1-9][0-9]*)|(?:beta|rc)(?:0|[1-9][0-9]*))$`)
)

func publicVersion(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= metricValueLimit && versionPattern.MatchString(value) {
		return value
	}
	return defaultVersion
}

func publicBuildDate(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= metricValueLimit {
		if _, err := time.Parse(time.RFC3339, value); err == nil {
			return value
		}
	}
	return defaultBuild
}

func publicGoVersion(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= metricValueLimit && goVersionPattern.MatchString(value) {
		return value
	}
	return "unknown"
}

func publicOS(value string) string {
	if value == runtime.GOOS {
		return value
	}
	return "unknown"
}

func publicArch(value string) string {
	if value == runtime.GOARCH {
		return value
	}
	return "unknown"
}

// ShortCommit returns a conventional seven-character commit identifier.
func ShortCommit(commit string) string {
	commit = strings.TrimSpace(commit)
	if commit == "" || commit == defaultCommit {
		return defaultCommit
	}
	if !commitPattern.MatchString(commit) {
		return defaultCommit
	}
	if len(commit) <= 7 {
		return strings.ToLower(commit)
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
