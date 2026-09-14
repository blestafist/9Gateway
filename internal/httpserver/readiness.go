package httpserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/pestit/9gateway/internal/storage"
)

const readinessTimeout = 2 * time.Second

// ReadinessState is the process lifecycle state used by readiness probes. It
// is intentionally small: the process owner marks it before beginning HTTP
// shutdown, so probes fail while the listener may still be draining requests.
type ReadinessState struct {
	draining atomic.Bool
}

// MarkDraining makes all subsequent readiness responses fail. It is safe to
// call more than once and from a goroutine concurrent with a probe.
func (state *ReadinessState) MarkDraining() {
	if state != nil {
		state.draining.Store(true)
	}
}

// SetDraining is useful to embedders and deterministic HTTP tests.
func (state *ReadinessState) SetDraining(draining bool) {
	if state != nil {
		state.draining.Store(draining)
	}
}

// Draining reports whether graceful shutdown has begun.
func (state *ReadinessState) Draining() bool {
	return state != nil && state.draining.Load()
}

// ReadinessConfig supplies the critical process-owned dependencies checked by
// /ready. A nil usage worker means detailed telemetry is disabled, which is a
// valid configuration and therefore passes its check. A non-nil worker must
// still be accepting jobs.
type ReadinessConfig struct {
	Database               *storage.DB
	UpstreamBaseURL        string
	UsageObservationWorker *UsageObservationWorker
	State                  *ReadinessState
}

// Readiness is a bounded, unauthenticated readiness probe.
type Readiness struct {
	database        *storage.DB
	upstreamBaseURL string
	usageWorker     *UsageObservationWorker
	state           *ReadinessState
}

// NewReadiness creates a readiness checker. It does not contact the upstream
// service; only its configured URL is validated.
func NewReadiness(configuration ReadinessConfig) *Readiness {
	return &Readiness{
		database:        configuration.Database,
		upstreamBaseURL: configuration.UpstreamBaseURL,
		usageWorker:     configuration.UsageObservationWorker,
		state:           configuration.State,
	}
}

// WithReadiness adds GET /ready before the supplied authenticated router. The
// wrapper is deliberately outside authentication so probes work before any
// gateway keys or admin credentials exist.
func WithReadiness(next http.Handler, readiness *Readiness) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && request.URL.Path == "/ready" {
			if readiness == nil {
				writeReadiness(response, readinessResult{ready: false, checks: readinessUnavailableChecks()})
				return
			}
			readiness.ServeHTTP(response, request)
			return
		}
		if next != nil {
			next.ServeHTTP(response, request)
			return
		}
		http.NotFound(response, request)
	})
}

type readinessCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

type readinessResult struct {
	ready  bool
	checks map[string]readinessCheck
}

func (readiness *Readiness) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), readinessTimeout)
	defer cancel()

	checks := make(map[string]readinessCheck, 5)
	checkNames := []string{"sqlite", "schema", "telemetry", "upstream", "lifecycle"}
	ready := true
	for _, name := range checkNames {
		var check readinessCheck
		if ctx.Err() != nil {
			check = failedReadinessCheck(name, "readiness deadline exceeded")
		} else if readiness.state != nil && readiness.state.Draining() {
			check = failedReadinessCheck(name, "gateway is shutting down")
		} else {
			check = readiness.runCheck(ctx, name)
		}
		checks[name] = check
		if check.Status != "pass" {
			ready = false
		}
	}
	// A drain can begin after the lifecycle check but before the response is
	// written. Re-read the atomic state so an in-flight probe cannot report
	// ready after shutdown has started.
	if readiness.state != nil && readiness.state.Draining() {
		ready = false
		checks["lifecycle"] = failedReadinessCheck("lifecycle", "gateway is shutting down")
	}
	writeReadiness(response, readinessResult{ready: ready, checks: checks})
}

func (readiness *Readiness) runCheck(ctx context.Context, name string) readinessCheck {
	switch name {
	case "sqlite":
		if readiness.database == nil {
			return failedReadinessCheck(name, "SQLite is unavailable")
		}
		var value int
		if err := readiness.database.QueryRowContext(ctx, "SELECT 1").Scan(&value); err != nil || value != 1 {
			return failedReadinessCheck(name, readinessDatabaseMessage(ctx, err))
		}
		return passedReadinessCheck(name)
	case "schema":
		if readiness.database == nil {
			return failedReadinessCheck(name, "SQLite schema is unavailable")
		}
		var version int
		if err := readiness.database.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
			return failedReadinessCheck(name, readinessDatabaseMessage(ctx, err))
		}
		if version != storage.CurrentSchemaVersion {
			return failedReadinessCheck(name, "SQLite schema version mismatch")
		}
		return passedReadinessCheck(name)
	case "telemetry":
		if readiness.usageWorker == nil {
			// Detailed telemetry is optional; absence is not a failed critical
			// subsystem. Configured workers, however, must accept submissions.
			return passedReadinessCheck(name)
		}
		if !readiness.usageWorker.AcceptingJobs() {
			return failedReadinessCheck(name, "telemetry worker is not accepting jobs")
		}
		return passedReadinessCheck(name)
	case "upstream":
		parsed, err := url.Parse(strings.TrimSpace(readiness.upstreamBaseURL))
		if err != nil || parsed == nil || !parsed.IsAbs() || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return failedReadinessCheck(name, "upstream URL is invalid or not configured")
		}
		return passedReadinessCheck(name)
	case "lifecycle":
		if readiness.state != nil && readiness.state.Draining() {
			return failedReadinessCheck(name, "gateway is shutting down")
		}
		return passedReadinessCheck(name)
	default:
		return failedReadinessCheck(name, "readiness check unavailable")
	}
}

func passedReadinessCheck(name string) readinessCheck {
	return readinessCheck{Name: name, Status: "pass"}
}

func failedReadinessCheck(name, message string) readinessCheck {
	if len(message) > 128 {
		message = message[:128]
	}
	return readinessCheck{Name: name, Status: "fail", Message: message}
}

func readinessDatabaseMessage(ctx context.Context, err error) string {
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return "SQLite check timed out"
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "SQLite check returned no result"
	}
	return "SQLite is unavailable"
}

func readinessUnavailableChecks() map[string]readinessCheck {
	checks := make(map[string]readinessCheck, 5)
	for _, name := range []string{"sqlite", "schema", "telemetry", "upstream", "lifecycle"} {
		checks[name] = failedReadinessCheck(name, "readiness checker is unavailable")
	}
	return checks
}

func writeReadiness(response http.ResponseWriter, result readinessResult) {
	response.Header().Set("Content-Type", "application/json")
	status := http.StatusServiceUnavailable
	if result.ready {
		status = http.StatusOK
	}
	response.WriteHeader(status)
	// All values are static/bounded and encoding errors cannot meaningfully be
	// recovered after the status has been sent.
	_ = json.NewEncoder(response).Encode(struct {
		Ready  bool                      `json:"ready"`
		Checks map[string]readinessCheck `json:"checks"`
	}{Ready: result.ready, Checks: result.checks})
}
