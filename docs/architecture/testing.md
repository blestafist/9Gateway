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

Timing assertions use generous CI thresholds, not flaky one-millisecond targets.
T159's 100-request test measures each request from immediately before
`client.Do` through EOF, fails on every Do/read/close error, requires each total
duration below 10 seconds, and independently requires mean stream-close
overhead against a direct-upstream baseline below 50 ms. TTFB cannot mask the
stream-close threshold.

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
spotting unexercised subsystems, but it includes CLI and provider-side support
packages that are not part of the T159 HTTP happy path. The acceptance contract
for this task uses the scoped aggregate command below and requires at least
80.0% total statement coverage.

The meaningful integration coverage command is:

```text
go test ./internal/... -coverpkg=./internal/accounting,./internal/auth,./internal/httpserver,./internal/limiter,./internal/observability,./internal/storage -coverprofile=/tmp/9gateway-integration.cover
go tool cover -func=/tmp/9gateway-integration.cover
```

The package set contains the accounting, authentication, gateway HTTP,
limiter, body observability, and SQLite implementations exercised by the live
happy path; CLI and provider test doubles are excluded. The acceptance gate is
an aggregate total of at least 80.0%; the reported percentage is expected to
vary with the checked-out source and test selection. Integration `TestMain`
runs goleak with no broad ignores; every test closes its listeners, transports,
workers, and SQLite handle before leak verification.
