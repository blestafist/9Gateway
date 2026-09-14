package httpserver

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

var metricBuckets = [...]float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10}

// gatewayMetrics is a deliberately small process-local Prometheus registry.
// Series are created only from bounded vocabularies and updates are atomic;
// metric collection never takes a request-path lock or touches transport data.
type gatewayMetrics struct {
	requests      metricCounterVec
	requestErrors metricCounterVec
	upstream      metricCounterVec
	telemetry     metricCounterVec
	active        atomic.Int64
	queueSources  []func() int
	durations     metricHistogramVec
	upstreamDur   metricHistogramVec
	ttfb          metricHistogramVec
	queueMu       sync.RWMutex
}

type metricCounter struct{ value atomic.Uint64 }
type metricCounterVec struct{ series sync.Map }

func (vec *metricCounterVec) add(key string, value uint64) {
	entry, _ := vec.series.LoadOrStore(key, new(metricCounter))
	entry.(*metricCounter).value.Add(value)
}

type metricHistogram struct {
	buckets [len(metricBuckets)]atomic.Uint64
	count   atomic.Uint64
	sum     atomic.Uint64 // math.Float64bits
}
type metricHistogramVec struct{ series sync.Map }

func (vec *metricHistogramVec) observe(key string, value float64) {
	if value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return
	}
	entry, _ := vec.series.LoadOrStore(key, new(metricHistogram))
	hist := entry.(*metricHistogram)
	for index, bucket := range metricBuckets {
		if value <= bucket {
			hist.buckets[index].Add(1)
		}
	}
	hist.count.Add(1)
	for {
		old := hist.sum.Load()
		updated := math.Float64bits(math.Float64frombits(old) + value)
		if hist.sum.CompareAndSwap(old, updated) {
			return
		}
	}
}

func newGatewayMetrics() *gatewayMetrics { return &gatewayMetrics{} }

func (metrics *gatewayMetrics) registerQueue(source func() int) {
	if metrics == nil || source == nil {
		return
	}
	metrics.queueMu.Lock()
	metrics.queueSources = append(metrics.queueSources, source)
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
	method := normalizeMetricMethod(record.Method)
	route := record.Route.String()
	status := metricStatus(record.DownstreamStatus)
	outcome := normalizeMetricOutcome(record.Terminal.Outcome)
	key := strings.Join([]string{route, method, status, outcome}, "\x00")
	metrics.requests.add(key, 1)
	if record.ErrorCode != ErrorCodeUnknown && validErrorCode(record.ErrorCode) {
		metrics.requestErrors.add(record.ErrorCode.String(), 1)
	}
	if record.Terminal.UpstreamStarted {
		upstreamStatus := metricStatus(record.UpstreamStatus)
		if upstreamStatus == "unknown" {
			upstreamStatus = "error"
		}
		metrics.upstream.add(upstreamStatus, 1)
	}
	if value, known := record.Timing.Total.Value(); known {
		metrics.durations.observe(key, float64(value)/1e6)
	}
	if value, known := record.Timing.TimeToUpstreamHeaders.Value(); known {
		metrics.upstreamDur.observe("", float64(value)/1e6)
	}
	if value, known := record.Timing.TimeToFirstByte.Value(); known {
		metrics.ttfb.observe("", float64(value)/1e6)
	}
}

func (metrics *gatewayMetrics) telemetryResult(result string) {
	if metrics != nil {
		metrics.telemetry.add(result, 1)
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
	sources := append([]func() int(nil), metrics.queueSources...)
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
		if metrics != nil {
			metrics.begin()
			defer metrics.end()
			request = request.WithContext(withGatewayMetricsContext(request.Context(), metrics))
			defer func() {
				if trace := TraceFromContext(request.Context()); trace != nil && completionOwnershipFromContext(request.Context()) == nil {
					trace.observeMetrics(metrics)
				}
			}()
		}
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
	if metrics == nil {
		return
	}
	var builder strings.Builder
	writeCounterFamily(&builder, "gateway_requests_total", "Total HTTP requests handled by the gateway.", &metrics.requests, []string{"route", "method", "status", "outcome"})
	writeCounterFamily(&builder, "gateway_request_errors_total", "Total gateway-owned request errors.", &metrics.requestErrors, []string{"error_code"})
	writeCounterFamily(&builder, "gateway_upstream_requests_total", "Total requests made to the configured upstream.", &metrics.upstream, []string{"status"})
	writeCounterFamily(&builder, "gateway_telemetry_jobs_total", "Total telemetry jobs by terminal result.", &metrics.telemetry, []string{"result"})
	builder.WriteString("# HELP gateway_active_requests Active HTTP requests currently handled by the gateway.\n# TYPE gateway_active_requests gauge\ngateway_active_requests ")
	builder.WriteString(strconv.FormatInt(metrics.active.Load(), 10))
	builder.WriteString("\n")
	builder.WriteString("# HELP gateway_telemetry_queue_depth Number of telemetry jobs currently queued.\n# TYPE gateway_telemetry_queue_depth gauge\ngateway_telemetry_queue_depth ")
	builder.WriteString(strconv.Itoa(metrics.queueDepth()))
	builder.WriteString("\n")
	writeHistogramFamily(&builder, "gateway_request_duration_seconds", "HTTP request duration in seconds.", &metrics.durations, []string{"route", "method", "status", "outcome"})
	writeHistogramFamily(&builder, "gateway_upstream_duration_seconds", "Upstream response-header duration in seconds.", &metrics.upstreamDur, nil)
	writeHistogramFamily(&builder, "gateway_ttfb_seconds", "Time to first downstream response byte in seconds.", &metrics.ttfb, nil)
	_, _ = response.Write([]byte(builder.String()))
}

func escapeMetricLabel(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return strings.ReplaceAll(value, "\n", `\n`)
}

func writeCounterFamily(builder *strings.Builder, name, help string, vec *metricCounterVec, labels []string) {
	fmt.Fprintf(builder, "# HELP %s %s\n# TYPE %s counter\n", name, help, name)
	var entries []struct {
		key   string
		value uint64
	}
	vec.series.Range(func(key, value any) bool {
		entries = append(entries, struct {
			key   string
			value uint64
		}{key.(string), value.(*metricCounter).value.Load()})
		return true
	})
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	for _, entry := range entries {
		values := strings.Split(entry.key, "\x00")
		if len(values) < len(labels) {
			continue
		}
		builder.WriteString(name)
		writeMetricLabels(builder, labels, values)
		fmt.Fprintf(builder, " %d\n", entry.value)
	}
}

func writeHistogramFamily(builder *strings.Builder, name, help string, vec *metricHistogramVec, labels []string) {
	fmt.Fprintf(builder, "# HELP %s %s\n# TYPE %s histogram\n", name, help, name)
	var entries []struct {
		key  string
		hist *metricHistogram
	}
	vec.series.Range(func(key, value any) bool {
		entries = append(entries, struct {
			key  string
			hist *metricHistogram
		}{key.(string), value.(*metricHistogram)})
		return true
	})
	sort.Slice(entries, func(i, j int) bool { return entries[i].key < entries[j].key })
	for _, entry := range entries {
		values := strings.Split(entry.key, "\x00")
		if len(values) < len(labels) {
			continue
		}
		for index, bucket := range metricBuckets {
			fmt.Fprintf(builder, "%s_bucket", name)
			writeMetricLabelsWithExtra(builder, labels, values, "le", strconv.FormatFloat(bucket, 'g', -1, 64))
			fmt.Fprintf(builder, " %d\n", entry.hist.buckets[index].Load())
		}
		fmt.Fprintf(builder, "%s_bucket", name)
		writeMetricLabelsWithExtra(builder, labels, values, "le", "+Inf")
		fmt.Fprintf(builder, " %d\n", entry.hist.count.Load())
		fmt.Fprintf(builder, "%s_count", name)
		writeMetricLabels(builder, labels, values)
		fmt.Fprintf(builder, " %d\n", entry.hist.count.Load())
		fmt.Fprintf(builder, "%s_sum", name)
		writeMetricLabels(builder, labels, values)
		fmt.Fprintf(builder, " %s\n", strconv.FormatFloat(math.Float64frombits(entry.hist.sum.Load()), 'g', -1, 64))
	}
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
