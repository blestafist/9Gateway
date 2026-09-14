package httpserver

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/pestit/9gateway/internal/version"
)

var metricBuckets = [...]float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

// gatewayMetrics is a deliberately small process-local Prometheus registry.
// Series are created only from bounded vocabularies and updates are atomic;
// metric collection never takes a request-path lock or touches transport data.
type gatewayMetrics struct {
	requests      fixedRequestCounter
	requestErrors metricCounterSeries20
	bodyTooLarge  atomic.Uint64
	upstream      metricCounterSeries601
	telemetry     metricCounterSeries3
	active        atomic.Int64
	queueSources  map[uint64]func() int
	queueNextID   uint64
	durations     fixedRequestHistogram
	upstreamDur   fixedHistogramSeries
	ttfb          fixedHistogramSeries
	queueMu       sync.RWMutex
}

type metricHistogram struct {
	buckets [len(metricBuckets)]atomic.Uint64
	count   atomic.Uint64
	sum     atomic.Uint64 // math.Float64bits
}

type metricCounterSeries20 struct{ values [20]atomic.Uint64 }
type metricCounterSeries601 struct{ values [601]atomic.Uint64 }
type metricCounterSeries3 struct{ values [3]atomic.Uint64 }
type fixedHistogramSeries struct {
	values [1]metricHistogram
}
type fixedRequestCounter struct{ values [7][8][600][7]atomic.Uint64 }
type fixedRequestHistogram struct {
	values [7][8][600][7]metricHistogram
}

func (hist *metricHistogram) observe(value float64) {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return
	}
	// Publish count before buckets. A scrape that sees a bucket increment then
	// also sees this observation in the count snapshot below.
	hist.count.Add(1)
	for index, bucket := range metricBuckets {
		if value <= bucket {
			hist.buckets[index].Add(1)
		}
	}
	for {
		old := hist.sum.Load()
		updated := math.Float64bits(math.Float64frombits(old) + value)
		if hist.sum.CompareAndSwap(old, updated) {
			return
		}
	}
}

func newGatewayMetrics() *gatewayMetrics {
	return &gatewayMetrics{queueSources: make(map[uint64]func() int)}
}

func (metrics *gatewayMetrics) registerQueue(source func() int) uint64 {
	if metrics == nil || source == nil {
		return 0
	}
	metrics.queueMu.Lock()
	metrics.queueNextID++
	metrics.queueSources[metrics.queueNextID] = source
	id := metrics.queueNextID
	metrics.queueMu.Unlock()
	return id
}

func (metrics *gatewayMetrics) unregisterQueue(id uint64) {
	if metrics == nil || id == 0 {
		return
	}
	metrics.queueMu.Lock()
	delete(metrics.queueSources, id)
	metrics.queueMu.Unlock()
}

func (metrics *gatewayMetrics) registerWorkers(workers ...interface{ metricsQueueSource() func() int }) {
	for _, worker := range workers {
		if worker != nil {
			metrics.registerQueue(worker.metricsQueueSource())
		}
	}
}

func (metrics *gatewayMetrics) begin() { metrics.active.Add(1) }
func (metrics *gatewayMetrics) end()   { metrics.active.Add(-1) }

func (metrics *gatewayMetrics) observe(record CompletionRecord) {
	if metrics == nil {
		return
	}
	method := metricMethodIndex(record.Method)
	route := metricRouteIndex(record.Route)
	status := metricStatusIndex(record.DownstreamStatus)
	outcome := metricOutcomeIndex(record.Terminal.Outcome)
	metrics.requests.values[route][method][status][outcome].Add(1)
	if record.ErrorCode != ErrorCodeUnknown && validErrorCode(record.ErrorCode) {
		metrics.requestErrors.values[metricErrorIndex(record.ErrorCode)].Add(1)
	}
	if record.Terminal.UpstreamStarted {
		upstreamStatus := metricStatusIndex(record.UpstreamStatus)
		if upstreamStatus == 0 {
			upstreamStatus = 500
		}
		metrics.upstream.values[upstreamStatus].Add(1)
	}
	if value, known := record.Timing.Total.Value(); known {
		metrics.durations.values[route][method][status][outcome].observe(float64(value) / 1e6)
	}
	if value, known := record.Timing.TimeToUpstreamHeaders.Value(); known {
		metrics.upstreamDur.values[0].observe(float64(value) / 1e6)
	}
	if value, known := record.Timing.TimeToFirstByte.Value(); known {
		metrics.ttfb.values[0].observe(float64(value) / 1e6)
	}
}

func (metrics *gatewayMetrics) telemetryResult(result string) {
	if metrics != nil {
		if index, ok := telemetryIndex(result); ok {
			metrics.telemetry.values[index].Add(1)
		}
	}
}

func (metrics *gatewayMetrics) configureWorkerQueue(source func() int) {
	metrics.registerQueue(source)
}

func (metrics *gatewayMetrics) queueDepth() int {
	if metrics == nil {
		return 0
	}
	depth := 0
	metrics.queueMu.RLock()
	sources := make([]func() int, 0, len(metrics.queueSources))
	for _, source := range metrics.queueSources {
		sources = append(sources, source)
	}
	metrics.queueMu.RUnlock()
	for _, source := range sources {
		depth += source()
	}
	return depth
}

type gatewayMetricsContextKey struct{}

func gatewayMetricsFromContext(ctx context.Context) *gatewayMetrics {
	if ctx == nil {
		return nil
	}
	metrics, _ := ctx.Value(gatewayMetricsContextKey{}).(*gatewayMetrics)
	return metrics
}

func metricsFromRequest(request *http.Request) *gatewayMetrics {
	if request == nil {
		return nil
	}
	return gatewayMetricsFromContext(request.Context())
}

func withGatewayMetricsContext(ctx context.Context, metrics *gatewayMetrics) context.Context {
	return context.WithValue(ctx, gatewayMetricsContextKey{}, metrics)
}

// withMetrics is outside authentication and readiness so every ordinary HTTP
// request (including rejected requests) contributes to the active gauge. The
// scrape endpoint itself is intentionally excluded from request totals.
func withMetrics(metrics *gatewayMetrics, next http.Handler) http.Handler {
	if metrics == nil {
		metrics = newGatewayMetrics()
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && request.URL.Path == "/metrics" {
			serveMetrics(response, metrics)
			return
		}
		metrics.begin()
		defer metrics.end()
		request = request.WithContext(withGatewayMetricsContext(request.Context(), metrics))
		defer func() {
			if trace := TraceFromContext(request.Context()); trace != nil && completionOwnershipFromContext(request.Context()) == nil {
				trace.observeMetrics(metrics)
			}
		}()
		if next != nil {
			next.ServeHTTP(response, request)
			return
		}
		http.NotFound(response, request)
	})
}

func normalizeMetricMethod(method string) string {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions:
		return method
	default:
		return "other"
	}
}

func metricStatus(status OptionalStatus) string {
	if value, known := status.Value(); known && value >= 100 && value <= 599 {
		return strconv.Itoa(value)
	}
	return "unknown"
}

func normalizeMetricOutcome(outcome TerminalOutcome) string {
	if validTerminalOutcome(outcome) && outcome != TerminalOutcomeUnknown {
		return string(outcome)
	}
	return "unknown"
}

const metricsContentType = "text/plain; version=0.0.4; charset=utf-8"

func serveMetrics(response http.ResponseWriter, metrics *gatewayMetrics) {
	response.Header().Set("Content-Type", metricsContentType)
	response.WriteHeader(http.StatusOK)
	var builder strings.Builder
	fmt.Fprintf(&builder, "# gateway version %s\n", version.Current().Version)
	if metrics == nil {
		_, _ = response.Write([]byte(builder.String()))
		return
	}
	writeFixedCounters(&builder, "gateway_requests_total", "Total HTTP requests handled by the gateway.", &metrics.requests)
	writeSimpleCounters(&builder, "gateway_request_errors_total", "Total gateway-owned request errors.", metrics.requestErrors.values[:], errorMetricNames[:])
	builder.WriteString("# HELP gateway_rejected_requests_total Total client requests rejected by the gateway.\n# TYPE gateway_rejected_requests_total counter\n")
	fmt.Fprintf(&builder, "gateway_rejected_requests_total{reason=\"body_too_large\"} %d\n", metrics.bodyTooLarge.Load())
	writeStatusCounters(&builder, "gateway_upstream_requests_total", "Total requests made to the configured upstream.", metrics.upstream.values[:])
	writeSimpleCounters(&builder, "gateway_telemetry_jobs_total", "Total telemetry jobs by terminal result.", metrics.telemetry.values[:], telemetryMetricNames[:])
	builder.WriteString("# HELP gateway_active_requests Active HTTP requests currently handled by the gateway.\n# TYPE gateway_active_requests gauge\ngateway_active_requests ")
	builder.WriteString(strconv.FormatInt(metrics.active.Load(), 10))
	builder.WriteString("\n")
	builder.WriteString("# HELP gateway_telemetry_queue_depth Number of telemetry jobs currently queued.\n# TYPE gateway_telemetry_queue_depth gauge\ngateway_telemetry_queue_depth ")
	builder.WriteString(strconv.Itoa(metrics.queueDepth()))
	builder.WriteString("\n")
	writeFixedHistograms(&builder, "gateway_request_duration_seconds", "HTTP request duration in seconds.", &metrics.durations)
	writeSimpleHistogram(&builder, "gateway_upstream_duration_seconds", "Upstream response-header duration in seconds.", &metrics.upstreamDur.values[0], "")
	writeSimpleHistogram(&builder, "gateway_ttfb_seconds", "Time to first downstream response byte in seconds.", &metrics.ttfb.values[0], "")
	_, _ = response.Write([]byte(builder.String()))
}

func escapeMetricLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return strings.ReplaceAll(value, "\n", `\n`)
}

var errorMetricNames = [...]string{"unknown", "invalid_api_key", "key_disabled", "key_expired", "invalid_request", "model_not_allowed", "request_limit_exceeded", "concurrency_limit_exceeded", "token_limit_exceeded", "budget_exceeded", "upstream_connection_error", "upstream_timeout", "response_transport_error", "conversion_error", "cancelled", "unsupported_response", "gateway_internal_error", "not_found", "conflict", "other"}
var telemetryMetricNames = [...]string{"persisted", "dropped", "failed"}

func writeSimpleCounters(builder *strings.Builder, name, help string, values []atomic.Uint64, names []string) {
	fmt.Fprintf(builder, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
	label := "error_code"
	if name == "gateway_telemetry_jobs_total" {
		label = "result"
	}
	for i := range values {
		value := &values[i]
		if i < len(names) && value.Load() != 0 {
			fmt.Fprintf(builder, "%s{%s=\"%s\"} %d\n", name, label, names[i], value.Load())
		}
	}
}
func writeStatusCounters(builder *strings.Builder, name, help string, values []atomic.Uint64) {
	fmt.Fprintf(builder, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
	for i := range values {
		value := &values[i]
		if value.Load() != 0 {
			fmt.Fprintf(builder, "%s{status=\"%s\"} %d\n", name, statusMetricName(i), value.Load())
		}
	}
}
func writeFixedCounters(builder *strings.Builder, name, help string, vec *fixedRequestCounter) {
	fmt.Fprintf(builder, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
	for route := range vec.values {
		for method := range vec.values[route] {
			for status := range vec.values[route][method] {
				for outcome := range vec.values[route][method][status] {
					value := vec.values[route][method][status][outcome].Load()
					if value == 0 {
						continue
					}
					fmt.Fprintf(builder, "%s{route=\"%s\",method=\"%s\",status=\"%s\",outcome=\"%s\"} %d\n", name, routeMetricName(route), methodMetricName(method), statusMetricName(status), outcomeMetricName(outcome), value)
				}
			}
		}
	}
}

func writeFixedHistograms(builder *strings.Builder, name, help string, vec *fixedRequestHistogram) {
	fmt.Fprintf(builder, "# HELP %s %s\n# TYPE %s histogram\n", name, help, name)
	for route := range vec.values {
		for method := range vec.values[route] {
			for status := range vec.values[route][method] {
				for outcome := range vec.values[route][method][status] {
					hist := &vec.values[route][method][status][outcome]
					if hist.count.Load() == 0 {
						continue
					}
					labels := fmt.Sprintf(`route="%s",method="%s",status="%s",outcome="%s"`, routeMetricName(route), methodMetricName(method), statusMetricName(status), outcomeMetricName(outcome))
					writeHistogramSamples(builder, name, hist, labels)
				}
			}
		}
	}
}

func writeSimpleHistogram(builder *strings.Builder, name, help string, hist *metricHistogram, labels string) {
	fmt.Fprintf(builder, "# HELP %s %s\n# TYPE %s histogram\n", name, help, name)
	if hist == nil || hist.count.Load() == 0 {
		return
	}
	writeHistogramSamples(builder, name, hist, labels)
}

func metricMethodIndex(method string) int {
	switch normalizeMetricMethod(method) {
	case "GET":
		return 1
	case "POST":
		return 2
	case "PUT":
		return 3
	case "PATCH":
		return 4
	case "DELETE":
		return 5
	case "HEAD":
		return 6
	case "OPTIONS":
		return 7
	}
	return 0
}
func methodMetricName(index int) string {
	if index == 1 {
		return "GET"
	}
	if index == 2 {
		return "POST"
	}
	if index == 3 {
		return "PUT"
	}
	if index == 4 {
		return "PATCH"
	}
	if index == 5 {
		return "DELETE"
	}
	if index == 6 {
		return "HEAD"
	}
	if index == 7 {
		return "OPTIONS"
	}
	return "other"
}
func metricRouteIndex(route RouteClass) int {
	if route <= RouteClassAdmin {
		return int(route)
	}
	return 0
}
func routeMetricName(index int) string { return RouteClass(index).String() }
func metricStatusIndex(status OptionalStatus) int {
	if value, known := status.Value(); known && value >= 100 && value <= 599 {
		return value
	}
	return 0
}
func statusMetricName(index int) string {
	if index == 0 {
		return "unknown"
	}
	return strconv.Itoa(index)
}
func metricOutcomeIndex(outcome TerminalOutcome) int {
	switch outcome {
	case TerminalOutcomePreUpstream:
		return 1
	case TerminalOutcomeUpstreamError:
		return 2
	case TerminalOutcomeResponseError:
		return 3
	case TerminalOutcomeComplete:
		return 4
	case TerminalOutcomeCustomDispatch:
		return 5
	case TerminalOutcomeCancelled:
		return 6
	}
	return 0
}
func outcomeMetricName(index int) string {
	switch index {
	case 1:
		return string(TerminalOutcomePreUpstream)
	case 2:
		return string(TerminalOutcomeUpstreamError)
	case 3:
		return string(TerminalOutcomeResponseError)
	case 4:
		return string(TerminalOutcomeComplete)
	case 5:
		return string(TerminalOutcomeCustomDispatch)
	case 6:
		return string(TerminalOutcomeCancelled)
	}
	return "unknown"
}
func metricErrorIndex(code SafeErrorCode) int {
	index := int(code)
	if index >= 0 && index < 20 {
		return index
	}
	return 19
}
func telemetryIndex(result string) (int, bool) {
	switch result {
	case "persisted":
		return 0, true
	case "dropped":
		return 1, true
	case "failed":
		return 2, true
	}
	return 0, false
}
func errorMetricNamesSlice() []string { return errorMetricNames[:] }
func writeHistogramSamples(builder *strings.Builder, name string, hist *metricHistogram, labels string) {
	separator := ""
	if labels != "" {
		separator = ","
	}
	// Writers update each bucket atomically in sequence. A scrape may observe
	// that sequence between bucket writes, so clamp the read-side snapshot to a
	// cumulative histogram before exposition. Count is published before bucket
	// updates and is loaded after all finite buckets here. Reuse this one count
	// snapshot for +Inf and _count, and cap each finite bucket against it.
	var cumulative uint64
	var buckets [len(metricBuckets)]uint64
	for i := range metricBuckets {
		buckets[i] = hist.buckets[i].Load()
	}
	count := hist.count.Load()
	for i, bucket := range metricBuckets {
		if value := buckets[i]; value > cumulative {
			cumulative = value
		}
		if cumulative > count {
			cumulative = count
		}
		fmt.Fprintf(builder, "%s_bucket{%s%sle=\"%s\"} %d\n", name, labels, separator, strconv.FormatFloat(bucket, 'g', -1, 64), cumulative)
	}
	fmt.Fprintf(builder, "%s_bucket{%s%sle=\"+Inf\"} %d\n%s_count{%s} %d\n%s_sum{%s} %s\n", name, labels, separator, count, name, labels, count, name, labels, strconv.FormatFloat(math.Float64frombits(hist.sum.Load()), 'g', -1, 64))
}

func writeMetricLabels(builder *strings.Builder, names, values []string) {
	if len(names) == 0 {
		return
	}
	builder.WriteString("{")
	for index, name := range names {
		if index > 0 {
			builder.WriteString(",")
		}
		fmt.Fprintf(builder, `%s="%s"`, name, escapeMetricLabel(values[index]))
	}
	builder.WriteString("}")
}

func writeMetricLabelsWithExtra(builder *strings.Builder, names, values []string, name, value string) {
	if len(names) == 0 {
		fmt.Fprintf(builder, `{%s="%s"}`, name, escapeMetricLabel(value))
		return
	}
	builder.WriteString("{")
	for index, label := range names {
		if index > 0 {
			builder.WriteString(",")
		}
		fmt.Fprintf(builder, `%s="%s"`, label, escapeMetricLabel(values[index]))
	}
	fmt.Fprintf(builder, `,%s="%s"}`, name, escapeMetricLabel(value))
}
