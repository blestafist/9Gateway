# Repository Architecture

## Principles

Transport and protocol semantics are separate. HTTP transport owns bytes,
headers, cancellation, timeouts, and connection lifetime. Protocol parsers may
observe known OpenAI fields but do not control transparent transport. Unknown
API is valid API and passes through.

Single-instance operation comes first: runtime coordination in memory,
persistence in SQLite, and deployment configuration in YAML/environment
variables.

## Initial Structure

- `cmd/gateway`: server entry point.
- `cmd/gwctl`: later admin CLI.
- `internal/config`: configuration loading and validation.
- `internal/httpserver`: routing, middleware, health and admin endpoints.
- `internal/proxy`: reverse-proxy orchestration.
- `internal/transport`: upstream HTTP client, headers, cancellation and timeouts.
- `internal/protocol/openai`: minimal OpenAI request/response understanding.
- `internal/streaming`: generic SSE parsing and OpenAI-specific aggregation.
- `internal/auth`, `policy`, `limiter`: identity and preflight enforcement.
- `internal/accounting`, `tokenizer`: usage, pricing and reconciliation.
- `internal/storage`: SQLite and migrations.
- `internal/observability`: trace, metrics, logging and bounded body capture.
- `internal/admin`: admin service/API.

Create packages only when code belongs in them. Do not add empty package files,
speculative interfaces, or generic `utils`/`common` dumping grounds.

## Dependencies

Orchestration depends on narrow domain capabilities. Storage, accounting and
limiting never depend on proxy transport. Generic SSE framing does not depend on
OpenAI. SQLite types do not leak into domain behavior. Introduce an interface
only at a real architectural boundary or where tests require substitution.

## Request Orchestration

The eventual lifecycle is request ID, bounded metadata inspection,
authentication, effective policy, preflight checks and reservations, upstream
transport, response transport, reconciliation, and trace completion. Acquired
resources belong to one idempotent request lease so normal completion, errors,
and cancellation cannot leak slots or reservations.
