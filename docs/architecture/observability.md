# Observability

## Request Trace

Every request receives a collision-safe ID exposed as
`X-Gateway-Request-ID`. Track route, model, requested and actual response modes,
status, termination reason, bytes, usage, cost-known state, error, and latency.
Useful latency points are upstream headers, TTFT, last meaningful event, total
duration, and stream-close delay. Metrics never control transport.

## Logging

Use structured production logs and readable development logs. Completion and
error events include request ID but never Authorization, gateway keys, upstream
keys, or per-token/chunk data by default. Upstream credentials must be redacted
in tests as well as implementation.

Observability wrappers around `http.ResponseWriter` must remain transparent to
supported controller operations such as flush and must not alter cancellation or
connection behavior. Unsupported operations retain their normal Go errors.

## Body Capture

Body logging is opt-in because prompts can contain sensitive data. A bounded
recorder retains only the first configured bytes while counting original size
and marking truncation. Client and upstream bodies remain distinguishable.
Streaming capture cannot alter flush behavior, and arbitrary prompt redaction is
not claimed to be reliable.

Deployment bounds are configured under `observability`. Body capture is globally
disabled when `max_captured_body_bytes` is zero, regardless of any future
per-key opt-in. Retention uses strict integer seconds: metadata defaults to 30
days and body data to 7 days, with body retention no longer than metadata
retention. The one startup cleanup pass and each subsequent 1024-job pass are
bounded to 1000 body deletions, then 1000 metadata deletions.

## Telemetry

Create one immutable completion record after handler cleanup and hand that same
record, without waiting, to both the bounded structured-log writer and the
bounded history writer. Slow, failed, or saturated detailed telemetry cannot
stall proxying; each sink may drop independently. Usage needed for enforcement
is reconciled independently before best-effort history persistence. During
shutdown, stop request admission and accounting observation first, then stop
telemetry admission and drain both detailed sinks before SQLite closes.

## Metrics

Core metrics cover request counts and activity, duration, TTFT, stream-close
delay, token/cost totals, rejections, upstream errors, cancellations, and dropped
telemetry. `gateway_requests_total{route,method,status,outcome}` is the primary
count: every request handled by the gateway, including URI/query and declared
body-size ingress rejections, contributes exactly once. Ingress rejections may
also appear in `gateway_rejected_requests_total`; that auxiliary counter must
not be added to the primary count. `route`, `method`, `status`, and `outcome`
come from bounded canonical vocabularies and never contain request paths,
queries, credentials, model names, or error text.

`gateway_build_info{version,commit,build_date,go_version,os,arch}` is a gauge
with value `1`. Its values are normalized and length-bounded before exposition;
arbitrary release `-ldflags -X` contents cannot create invalid or unbounded
label values. The exposition also retains the human-readable
`# gateway version ...` comment for compatibility. Never use request ID as a
label; avoid unbounded key/model label cardinality.
