# Operations

## Configuration

Configuration covers listen address, upstream URL/credential, SQLite path,
timeouts, logging, tokenizer defaults, and pricing. Resolve secrets from the
environment and validate before opening the listener. Prefer strict handling of
unknown YAML fields. Hot reload is not MVP.

## Server Lifecycle

Startup loads and validates configuration, opens SQLite, applies migrations,
loads key/policy caches, initializes limiter state, transport and telemetry, then
starts HTTP and marks readiness. Graceful shutdown stops new work, waits a
bounded period, cancels remaining requests, flushes critical accounting and
bounded telemetry, closes SQLite, and exits.

## Health

`GET /health` proves only that the process and HTTP server are alive. `GET
/ready` eventually verifies configuration, storage, migrations, required
secrets, and initialized services. It does not perform an LLM generation or make
temporary upstream failure define gateway process health.

## Administration

Admin routes use `/admin/v1/*` and a credential distinct from gateway keys.
They manage keys/policies and inspect paginated request/usage history. Raw keys
are returned only on creation. The CLI is a thin admin API client once that API
exists; avoid duplicating business logic through direct SQLite access.

## Deployment

Ship one small Go binary/container, one YAML config, and one SQLite database.
Use a multi-stage image, non-root user, persistent `/data`, `/health` healthcheck,
and correct SIGTERM handling. No mandatory Redis, PostgreSQL, or companion
service. Web UI begins only after transport, policy, accounting, and persistence
are stable.
