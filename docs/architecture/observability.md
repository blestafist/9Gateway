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

## Body Capture

Body logging is opt-in because prompts can contain sensitive data. A bounded
recorder retains only the first configured bytes while counting original size
and marking truncation. Client and upstream bodies remain distinguishable.
Streaming capture cannot alter flush behavior, and arbitrary prompt redaction is
not claimed to be reliable.

## Telemetry

Create an immutable completion record and send it to a bounded writer queue.
Slow or failed detailed telemetry cannot stall proxying. Track dropped records.
Usage needed for enforcement is reconciled independently before best-effort
history persistence.

## Metrics

Core metrics cover request counts and activity, duration, TTFT, stream-close
delay, token/cost totals, rejections, upstream errors, cancellations, and dropped
telemetry. Never use request ID as a label; avoid unbounded key/model label
cardinality.
