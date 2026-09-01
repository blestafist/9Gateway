# Testing

## Transport Integration

Use a real HTTP mock upstream and a real gateway HTTP server. Interface-only
mocks cannot verify chunk boundaries, flushing, EOF, cancellation, content type,
slow bodies, hanging connections, or parallel requests.

Required transport scenarios include ordinary JSON, SSE with and without
`[DONE]`, client `stream:false` with upstream SSE, split and coalesced SSE reads,
fragmented tool calls, client cancellation, concurrent requests, upstream error
statuses, unknown endpoints, binary bodies, and bounded large requests.

## Regressions

- Upstream SSE for a non-stream client aggregates to valid JSON instead of a JSON
  unmarshal failure.
- Terminal content followed by EOF closes downstream without seconds of delay.
- EOF does not depend on `[DONE]`.
- Requests run concurrently unless a configured limit forbids it.
- Client cancellation promptly reaches upstream.
- Slow parsing/telemetry does not block streaming.

Timing assertions use generous CI thresholds, such as EOF-to-close below 250 ms,
not flaky one-millisecond targets.

## Limit Tests

Rate, token, and budget windows use an injectable clock; tests never sleep for a
real minute/day. Concurrent reservation tests prove that individually valid
requests cannot collectively oversubscribe token or budget limits. Every exit
path tests lease and concurrency-slot release.

## Security And Performance

Test credential redaction, fixed upstream host, and safe path joining with base
prefixes, duplicate slashes, dot-like segments, and encoded slashes. Also test
body size limits and separation of admin and gateway credentials. Compare direct
mock or 9router against gateway TTFT, stream close, total duration, and
parallelism before optimizing. The release blocker is observable coding-agent
latency, not an arbitrary requests-per-second target.
