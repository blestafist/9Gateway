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
	goVersion := GoVersion
	if strings.TrimSpace(goVersion) == "" {
		// Keep an unset override aligned with the runtime release core rather
		// than reporting an unknown toolchain for the normal default case.
		goVersion = runtime.Version()
	}
	return Metadata{
		Version:   publicVersion(Version),
		Commit:    ShortCommit(CommitSHA),
		BuildDate: publicBuildDate(BuildDate),
		GoVersion: publicGoVersion(goVersion),
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
	// The public version is deliberately narrower than the full SemVer
	// prerelease grammar. Release tooling may use only the approved alpha,
	// beta, and rc forms; arbitrary prerelease identifiers could contain a
	// credential or other build-system payload.
	versionPattern = regexp.MustCompile(`^(?:dev|v?(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-(?:alpha|beta|rc)\.?(?:0|[1-9][0-9]*))?)$`)
	buildPattern   = regexp.MustCompile(`^[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*$`)
	commitPattern  = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)
	// Go's canonical release forms are go1.M.p, go1.MbetaN, and go1.MrcN.
	// A custom toolchain may append a hyphen suffix. Capture only the
	// canonical prefix so the suffix can never become public metadata.
	goVersionPattern = regexp.MustCompile(`^(go(?:[1-9][0-9]*\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)|[1-9][0-9]*\.(?:0|[1-9][0-9]*)(?:beta|rc)(?:0|[1-9][0-9]*)))(?:-[^\r\n]+)?$`)
)

func publicVersion(value string) string {
	value = strings.TrimSpace(value)
	core, build, hasBuild := strings.Cut(value, "+")
	if hasBuild {
		// Validate build metadata as SemVer before dropping it. This accepts
		// legitimate metadata, while ensuring malformed input is not treated
		// as a version by accident. The metadata itself is never returned.
		if !buildPattern.MatchString(build) {
			return defaultVersion
		}
		value = core
		if value == "dev" {
			return defaultVersion
		}
	}
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
	if match := goVersionPattern.FindStringSubmatch(value); match != nil && len(match[1]) <= metricValueLimit {
		// runtime.Version can contain a custom toolchain suffix (including
		// sensitive text). Normalize it to the canonical release core; the
		// suffix is intentionally not validated or exposed. Invalid values
		// remain unknown rather than allowing a substring to become public.
		return match[1]
	}
	return "unknown"
}

func runtimeGoReleaseCore() string {
	if match := goVersionPattern.FindStringSubmatch(runtime.Version()); match != nil {
		return match[1]
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
