# Testing

## Transport Integration

The T159 acceptance suite (`go test ./internal/integration -v`) uses a real TCP
gateway listener, an HTTP upstream, a temporary SQLite database, and the same
worker wiring as production. Failure assertions correlate each persisted
request ID with terminal outcome, upstream boundary, statuses, and usage/cost.
The live budget scenario asserts reservation, pre-upstream rejection, actual
cost, reconciliation, and the SQLite history/bucket projections.

Telemetry backpressure is tested only through live HTTP requests. A real
`slog.Handler` blocks after the first completion; channels synchronize entry
and release while subsequent responses and critical accounting must finish and
the bounded detailed queue may drop.

Use a real HTTP mock upstream and a real gateway HTTP server. Interface-only
mocks cannot verify chunk boundaries, flushing, EOF, cancellation, content type,
slow bodies, hanging connections, or parallel requests.

Current transport scenarios include ordinary JSON, SSE with and without
`[DONE]`, split and coalesced SSE reads, client cancellation, concurrent
requests, upstream error statuses, unknown endpoints, binary bodies, bounded
large requests, and client `stream:false` with upstream SSE, including
fragmented tool calls.

## Regressions

- Terminal content followed by EOF closes downstream without seconds of delay.
- `TestProxySSECloseDelayRegression` guards EOF-to-close below 250 ms in CI; this
  is a regression guardrail, not a production latency SLA.
- EOF does not depend on `[DONE]`.
- Requests run concurrently unless a configured limit forbids it.
- Client cancellation promptly reaches upstream.
- Slow parsing/telemetry does not block streaming.

SSE aggregation regressions cover a non-stream client and fragmented tool calls;
the conversion is bounded and is only selected after actual upstream response
classification.

Timing assertions use deterministic local delays and multiple paired samples,
not flaky one-millisecond targets. T159's performance test measures each
request from immediately before `client.Do` through the first successfully read
byte (TTFB), then separately from that byte through EOF and `Body.Close`
(post-first-byte stream-close time). Direct-upstream and gateway means are
computed over 20 sequential pairs; every `Do`, first-byte read, full-body read,
and close error fails the test. Gateway-minus-direct overhead must be below
10ms independently for both TTFB and post-first-byte stream-close time; these
are separate limits and cannot be satisfied by a compound total-lifetime
allowance. The concurrent phase uses 100 requests, requires every total
duration (immediately before `Do` through read and close) below 10 seconds, and
requires mean total stream-close duration below 50ms.

Split and coalesced SSE tests compare the complete raw body. They must not assume
that an upstream write, HTTP read, or TCP read corresponds to one downstream
event or read.

Cancellation coverage includes both cancellation before upstream response headers
and cancellation after a response has begun streaming. The active-stream case
must read a flushed fragment first, then verify that the upstream request context
is canceled.

The unrestricted parallel-stream regression uses two clients and shared barriers:
both upstream handlers must arrive and both clients must receive their first
fragment before either completion barrier is released.

Shared streaming setup lives in `internal/httpserver/stream_test.go`. Its
test-only script controls real upstream fragments, flushes, first-fragment and
release barriers, EOF timestamps, arrivals, and cancellation observation. Cleanup
always releases a blocked stream before closing the server. The harness reduces
setup duplication only; all transport assertions continue to use real HTTP
servers, including JSON, `[DONE]` and no-`[DONE]` SSE, split/coalesced writes,
hanging requests, active cancellation, and parallel streams.

## Limit Tests

Rate, token, and budget windows use an injectable clock; tests never sleep for a
real minute/day. Concurrent reservation tests prove that individually valid
requests cannot collectively oversubscribe token or budget limits. Every exit
path tests lease and concurrency-slot release.

## Security And Performance

Test credential redaction from response headers and completion logs, fixed
upstream host, and safe path joining with base prefixes, duplicate slashes,
dot-like segments, and encoded slashes. Also test body size limits and separation
of admin and gateway credentials. Compare direct mock or 9router against gateway
TTFT, stream close, total duration, and parallelism before optimizing. The
release blocker is observable coding-agent latency, not an arbitrary
requests-per-second target.

The broad internal coverage smoke check is:

```text
go test -coverpkg=./internal/... ./internal/...
```

It reports package-level coverage for every internal package and is useful for
spotting unexercised subsystems. It is not an acceptance percentage: it
includes CLI, provider-side support, and other packages outside the T159 HTTP
happy path.

The live integration coverage metric is measured only by the eight real
cross-subsystem tests:

```text
go test ./internal/integration -coverpkg=./internal/accounting,./internal/auth,./internal/httpserver,./internal/limiter,./internal/observability,./internal/storage -coverprofile=/tmp/9gateway-live.cover
go tool cover -func=/tmp/9gateway-live.cover
```

This is a diagnostic metric for code reached by live HTTP, SQLite, and worker
wiring. It is intentionally not the T159 80% gate: transport integration does
not exercise every branch of the storage compatibility and limiter APIs. The
aggregate T159 happy-path verification runs all internal behavior tests while
instrumenting the same explicit, meaningful package set:

```text
go test ./internal/... -coverpkg=./internal/accounting,./internal/auth,./internal/httpserver,./internal/limiter,./internal/observability,./internal/storage -coverprofile=/tmp/9gateway-aggregate.cover && go run ./CI/scripts/coveragecheck -profile=/tmp/9gateway-aggregate.cover -min=80.0
go tool cover -func=/tmp/9gateway-aggregate.cover
```

The checker reports the same one-decimal rounding as `go tool cover`; the
unrounded profile is retained in the count shown beside the percentage. The
package set contains the accounting, authentication, gateway HTTP,
limiter, body observability, and SQLite implementations; CLI and provider test
doubles are excluded. The checker sums covered statements from the profile and
fails below 80.0%, so a lower result cannot be relabeled as the live metric or
hidden by changing the package set. Keep the profile paths distinct: using
`./internal/integration` for the live command measured 47.2% before the
additional admin-read scenario and now measures 48.9% (the exact result can
vary slightly with source changes), while `./internal/...` includes the
existing unit and behavior tests and is the aggregate milestone metric.
The `CI/scripts/coveragecheck` command itself is not in `-coverpkg`; adding the
checker cannot inflate the aggregate core-package result.
Integration `TestMain`
runs goleak with no broad ignores; every test closes its listeners, transports,
workers, and SQLite handle before leak verification.
