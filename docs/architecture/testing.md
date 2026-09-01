# Testing

## Transport Integration

Use a real HTTP mock upstream and a real gateway HTTP server. Interface-only
mocks cannot verify chunk boundaries, flushing, EOF, cancellation, content type,
slow bodies, hanging connections, or parallel requests.

Current transport scenarios include ordinary JSON, SSE with and without
`[DONE]`, split and coalesced SSE reads, client cancellation, concurrent
requests, upstream error statuses, unknown endpoints, binary bodies, and bounded
large requests. Future compatibility coverage will include client `stream:false`
with upstream SSE and fragmented tool calls.

## Regressions

- Terminal content followed by EOF closes downstream without seconds of delay.
- `TestProxySSECloseDelayRegression` guards EOF-to-close below 250 ms in CI; this
  is a regression guardrail, not a production latency SLA.
- EOF does not depend on `[DONE]`.
- Requests run concurrently unless a configured limit forbids it.
- Client cancellation promptly reaches upstream.
- Slow parsing/telemetry does not block streaming.

Future compatibility regressions will cover SSE aggregation for a non-stream
client and fragmented tool calls.

Timing assertions use generous CI thresholds, not flaky one-millisecond targets.

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
