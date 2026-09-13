# Active Tasks (T141-T160)

This file contains the next twenty atomic tasks for the admin read API, CLI, 
readiness/metrics, and packaging milestone. An implementation agent reads only 
`AGENTS.md`, `CURRENT.md`, its assigned task, and the architecture documents 
linked by that task. Tasks are ordered by dependency and must not be implemented 
out of order.

Every implementation task must finish with `go fmt ./...`, `go test ./...`, and 
`go build ./...`. Add behavioral tests in the same task as changed HTTP or 
streaming behavior. Update `CURRENT.md` and commit after completing one task.

## Admin Read API

### T141 - Add admin GET /keys endpoint with pagination

Goal: implement paginated API key listing through a read-only admin endpoint that 
exposes safe key metadata without raw keys or full policy JSON.

Scope:

- Add `GET /admin/v1/keys` accepting optional `?limit=N&cursor=CURSOR` query 
  parameters. Default limit 50, maximum 500. Return JSON with `keys` array and 
  optional `next_cursor` string for pagination.
- Each key object contains: `id`, `name`, `display_prefix`, `enabled`, `created_at`, 
  `updated_at`, `expires_at` (nullable), and `policy_summary` with only 
  `allow_models`, `deny_models`, `log_request_body`, `log_response_body` booleans.
- Use storage layer cursor-based pagination ordered by creation time descending 
  (newest first). Cursor must be opaque, URL-safe, tamper-evident, and not expose 
  internal database offsets or IDs directly.
- Require admin credential authentication. Invalid/missing auth returns 401. 
  Malformed limit/cursor returns 400 with structured error.
- Repository method `ListAPIKeys(ctx, limit, cursor) ([]KeyListRecord, nextCursor, error)` 
  reads from `api_keys` table without loading full `policy_json` or digest into memory.

Acceptance and tests:

- Real HTTP tests cover: no keys, single page, multiple pages, exact limit boundary, 
  invalid cursor, oversized limit, missing/wrong admin auth, concurrent key creation 
  during pagination.
- Cursor values are opaque and URL-safe. Page 2 starts exactly after page 1's last 
  item. Empty result returns empty array with no `next_cursor`.
- Response contains no raw key, digest, pepper, full policy JSON, or SQL details. 
  Keys with null `expires_at` serialize it as `null`, not omitted or zero timestamp.
- `go test -race ./...`, `go fmt ./...`, `go test ./...`, `go build ./...` pass.

Reference: `docs/architecture/storage.md#api-keys`, 
`docs/architecture/operations.md#admin-api`.

Dependencies and out of scope: first task in T141-T160 milestone. Do not add key 
deletion, policy update GET, usage stats in list view, or full-text search yet.

### T142 - Add admin GET /keys/:id endpoint

Goal: retrieve complete details for one API key including full effective policy 
without exposing security-sensitive digest or pepper values.

Scope:

- Add `GET /admin/v1/keys/:id` where `:id` is the stable string key ID. Return 
  404 if key not found, 400 if ID format invalid, 401 without admin auth.
- Response JSON contains: `id`, `name`, `display_prefix`, `enabled`, `created_at`, 
  `updated_at`, `expires_at`, and complete `policy` object with all fields from 
  `auth.EffectivePolicy`: request limits (count, window), concurrency, token limits, 
  budgets (total/daily/monthly), `allow_models`, `deny_models`, `log_request_body`, 
  `log_response_body`.
- Repository method `GetAPIKeyByID(ctx, id) (*KeyDetailRecord, error)` loads from 
  SQLite, parses `policy_json`, and returns typed policy struct. Invalid stored 
  policy JSON returns internal error, not 404.
- Use existing policy JSON parsing logic. Do not duplicate policy schema validation.
- Timestamps serialize as RFC3339. Money values use integer micro-dollars. Durations 
  use integer seconds (convert from stored microseconds).

Acceptance and tests:

- HTTP tests cover: existing key with complex policy, non-existent ID, malformed ID, 
  disabled key, expired key, key with null expires_at, no admin auth, wrong auth.
- Response contains complete parseable policy matching what's effective for requests. 
  No digest, pepper, raw key, SQL, or internal limiter generation values exposed.
- Concurrent policy update doesn't race with GET. Policy in response matches what 
  was committed to SQLite at read time.
- `go test ./...` and `go build ./...` pass.

Reference: `docs/architecture/policy.md#effective-policy`, 
`docs/architecture/storage.md#api-keys`.

Dependencies and out of scope: depends on T141. Do not add usage statistics, 
request history links, policy PATCH, key deletion, or policy validation endpoint.

### T143 - Add admin GET /requests endpoint with pagination

Goal: list request history with pagination, time filters, and per-key filtering 
without loading body content or exposing secrets.

Scope:

- Add `GET /admin/v1/requests` accepting query parameters: `?limit=N`, `?cursor=C`, 
  `?key_id=KID`, `?after=RFC3339`, `?before=RFC3339`. Default limit 50, max 500.
- Return JSON with `requests` array and optional `next_cursor`. Each request object 
  contains all T136 schema fields except bodies: `request_id`, `api_key_id` (nullable), 
  `api_key_name` (nullable, bounded), `method`, `path`, `route`, `model` (nullable), 
  modes (requested/upstream/delivered), statuses, terminal outcome, error code, 
  byte counts, token counts (nullable), cost (nullable integer micros), timestamps, 
  latencies (nullable integer micros).
- Repository method `ListRequests(ctx, ListRequestsFilter, limit, cursor)` queries 
  `requests` table with indexed time range and optional key filter. Order by 
  `completed_at DESC` (newest first). Cursor encodes timestamp+request_id bookmark.
- Time filters use `completed_at`. Invalid RFC3339 or after>before returns 400. 
  Unknown `key_id` returns empty results, not 404.
- Require admin auth. Preserve NULL vs 0 distinction for tokens/cost/latencies.

Acceptance and tests:

- HTTP tests: empty history, single page, multi-page, key filter, time range, 
  combined filters, no matches, invalid params, pagination across 1000+ records.
- Cursor-based pagination is stable during concurrent new requests. Page boundaries 
  are exact with no duplicates or gaps under normal operation.
- Response never includes body bytes, raw keys, Authorization headers, digest, 
  pepper, policy JSON, pricing rules, or SQL errors.
- Null fields serialize as JSON `null`. Unknown vs zero: absent token count is 
  `null`, known zero tokens is `0`.
- Tests with T140 fixture data covering all request types: JSON, SSE, converted, 
  rejected, cancelled, various error codes.

Reference: `docs/architecture/storage.md#requests-and-bodies`, 
`docs/architecture/observability.md#request-trace`.

Dependencies and out of scope: depends on T141-T142. Do not add full-text search, 
arbitrary SQL filters, CSV export, real-time streaming, aggregations, or body access.

### T144 - Add admin GET /requests/:id endpoint

Goal: retrieve complete metadata for one historical request without body content.

Scope:

- Add `GET /admin/v1/requests/:id` where `:id` is the request ID string from T121. 
  Return 404 if not found, 400 if malformed ID, 401 without admin auth.
- Response JSON contains the same fields as T143 list items but for a single request: 
  all scalar metadata from the T136 `requests` table row.
- Repository method `GetRequestByID(ctx, requestID) (*RequestDetailRecord, error)` 
  performs indexed lookup. Do not JOIN or load body rows in this endpoint.
- Preserve all NULL vs 0 semantics. Timestamps as RFC3339, durations and cost as 
  integer micros, enums as strings matching T121 definitions.
- Response includes `has_bodies` boolean array indicating which body kinds exist 
  (client_request, upstream_request, response) without loading actual bytes.

Acceptance and tests:

- HTTP tests: successful request with all fields, rejected pre-upstream, cancelled, 
  converted SSE, unauthenticated request (null key_id), non-existent ID, malformed ID.
- `has_bodies` correctly reflects T137 `request_bodies` rows. Empty array if no 
  bodies captured, up to 3 kinds if all captured.
- Response identical to what T143 would return for same request. No raw keys, 
  headers, body bytes, digest, policy, SQL, or pricing rules exposed.
- Concurrent body deletion doesn't cause 500; `has_bodies` reflects current state.

Reference: `docs/architecture/storage.md#requests-and-bodies`.

Dependencies and out of scope: depends on T143. Do not add body content retrieval, 
request replay, related requests, aggregated stats, or DELETE operation yet.

### T145 - Add admin GET /requests/:id/bodies/:kind endpoint

Goal: retrieve one captured body by request ID and kind with proper content type 
and size bounds enforcement.

Scope:

- Add `GET /admin/v1/requests/:id/bodies/:kind` where `:kind` is one of 
  `client_request`, `upstream_request`, or `response`. Return 404 if request or 
  body kind not found, 400 if invalid kind, 401 without admin auth.
- Response headers: `Content-Type: application/octet-stream`, 
  `X-Original-Size: <bytes>`, `X-Truncated: true|false`. Stream body bytes directly.
- Repository method `GetRequestBody(ctx, requestID, kind) (*BodyContent, error)` 
  loads BLOB from T137 `request_bodies` table. Validate original size and truncation 
  flag consistency with captured bytes length.
- Enforce safety: refuse to serve bodies larger than 10MB even if stored. Refuse if 
  stored bytes exceed schema constant `MaxCapturedBodyBytes` (1MB from T130).
- Do not decode, parse, redact, or transform bytes. Serve exactly what was captured.

Acceptance and tests:

- HTTP tests: all three kinds for one request, empty body (0 bytes), truncated 
  large body, binary/NUL/invalid UTF-8 content, non-existent kind, wrong kind name, 
  missing request, no admin auth.
- Response bytes exactly match what T133/T134/T135 captured. Headers correctly 
  reflect truncation state and original size.
- Oversized or schema-inconsistent stored body returns 500, not corrupted download.
- Concurrent retention cleanup causing body deletion during GET returns 404, not 
  partial content or 500.
- Tests use T140 captured fixtures covering JSON, SSE fragments, gzip responses.

Reference: `docs/architecture/storage.md#requests-and-bodies`, 
`docs/architecture/observability.md#body-capture`.

Dependencies and out of scope: depends on T144. Do not add body search, diff, 
pretty-printing, automatic JSON formatting, redaction, or streaming decompression.

## CLI Tool

### T146 - Create gwctl CLI foundation

Goal: build a minimal CLI binary that authenticates with the admin API and provides 
a foundation for key and request management commands.

Scope:

- Add `cmd/gwctl/main.go` as a separate binary. Support `--gateway-url` (default 
  `http://localhost:8080`) and `--admin-credential` (or env `GWCTL_ADMIN_CREDENTIAL`) 
  flags globally.
- Implement `gwctl version` showing CLI version and `gwctl ping` hitting 
  `/admin/v1/keys?limit=1` to verify connectivity and auth.
- Use a simple CLI framework (e.g., `spf13/cobra` or stdlib `flag` with subcommands). 
  Do not add heavy dependencies like full TUI frameworks.
- Store no credentials on disk in this task. Require explicit flag or env var each 
  invocation. Validate URL format and non-empty credential before API calls.
- Return exit code 0 on success, 1 on usage errors, 2 on API errors. Print errors 
  to stderr, output to stdout.

Acceptance and tests:

- `gwctl version` prints version without requiring auth or URL.
- `gwctl ping` succeeds against real running gateway with valid admin credential, 
  fails with 401 for invalid credential, fails with connection error for wrong URL.
- `gwctl --help` shows usage. Unknown command returns exit 1 with error message.
- Integration test: start gateway, run gwctl commands, verify exit codes and output.
- Build produces standalone `gwctl` binary: `go build ./cmd/gwctl` succeeds.

Reference: `docs/architecture/operations.md#admin-api`.

Dependencies and out of scope: depends on T141-T145. Do not add key create/list/get, 
request list/get, config file, credential storage, interactive mode, or TUI yet.

### T147 - Add gwctl keys list and get commands

Goal: implement CLI commands for listing and inspecting API keys using T141-T142 
admin endpoints.

Scope:

- Add `gwctl keys list [--limit N]` calling `GET /admin/v1/keys`. Display table with 
  columns: ID (first 12 chars), Name, Prefix, Enabled, Created. Support `--json` 
  flag for raw JSON output.
- Add `gwctl keys get <id>` calling `GET /admin/v1/keys/:id`. Display human-readable 
  formatted output: key metadata, policy section with limits/budgets/models/logging.
- Handle pagination transparently in `list`: fetch all pages automatically unless 
  `--limit` specified. Show progress to stderr if fetching multiple pages.
- Format timestamps as local time in human output, preserve RFC3339 in JSON mode.
- Error handling: 401→"Authentication failed", 404→"Key not found", network errors 
  with helpful message, malformed JSON→"Invalid API response".

Acceptance and tests:

- Integration tests against real gateway with multiple keys: list all, list with 
  limit, get existing, get non-existent, invalid auth, both human and JSON output.
- Human output is readable and aligned. JSON output is valid and parseable.
- `keys list` with 150 keys fetches 3 pages automatically without manual pagination.
- Empty list shows "No keys found", not an error.

Reference: `docs/architecture/operations.md#admin-api`.

Dependencies and out of scope: depends on T146. Do not add key creation, deletion, 
policy update, filtering, sorting, or usage statistics display yet.

### T148 - Add gwctl requests list and get commands

Goal: implement CLI commands for viewing request history using T143-T144 endpoints.

Scope:

- Add `gwctl requests list [--limit N] [--key-id ID] [--after TIME] [--before TIME]` 
  calling `GET /admin/v1/requests`. Display table: Request ID (first 12), Key Name, 
  Method, Route, Model, Status, Tokens, Cost, Duration, Completed.
- Add `gwctl requests get <request-id>` calling `GET /admin/v1/requests/:id`. 
  Display formatted sections: identity, request metadata, response metadata, 
  usage/cost, timing, error (if any).
- Add `gwctl requests get <request-id> --body <kind>` calling body endpoint T145. 
  Write body bytes to stdout or `--output FILE`. Show truncation warning to stderr 
  if `X-Truncated: true`.
- Format: tokens with thousand separators, cost as dollars (from micros), durations 
  as human readable (e.g., "1.234s", "456ms"), timestamps as local time.
- Support `--json` for raw API output. Handle null fields gracefully: show "-" or 
  "unknown" for null tokens/cost/latency in human mode.

Acceptance and tests:

- Integration tests: list recent requests, filter by key, time range, get complete 
  request, get with all body kinds, get truncated body, non-existent request.
- Body output writes exact bytes to stdout/file. Binary content doesn't corrupt 
  terminal when piped or redirected.
- Empty list shows "No requests found". Pagination handled like T147.
- Cost display: 1500000 micros → "$1.50", null → "-".

Reference: `docs/architecture/storage.md#requests-and-bodies`.

Dependencies and out of scope: depends on T147. Do not add request replay, 
aggregation, export, filtering by model/status/error, or real-time tail mode yet.

## Health and Metrics

### T149 - Implement /ready endpoint with deep checks

Goal: add a readiness endpoint that validates critical subsystem health without 
requiring admin authentication, suitable for Kubernetes/Docker health checks.

Scope:

- Add `GET /ready` (no auth required) returning 200 if ready, 503 if not ready. 
  Response JSON: `{"ready": true/false, "checks": {...}}` with individual check results.
- Perform checks: SQLite connectivity (execute `SELECT 1`), SQLite schema version 
  matches expected, telemetry worker accepting jobs (check queue not in shutdown), 
  upstream URL configured and parseable.
- Each check result includes: `name`, `status` ("pass"/"fail"), optional `message`. 
  Overall ready=true only if all checks pass. Use 2-second timeout for all checks.
- Do not ping upstream 9router on every readiness check. Only validate configuration, 
  not external service availability.
- Readiness fails during graceful shutdown after HTTP listener stops accepting.

Acceptance and tests:

- HTTP tests: healthy gateway returns 200 with all checks passing, SQLite closed 
  returns 503, wrong schema version returns 503, during shutdown returns 503.
- Timeout test: slow SQLite query doesn't hang readiness check beyond 2 seconds.
- No authentication required. Endpoint works before any keys are created.
- Output valid JSON. Failed check includes helpful message, not stack traces or SQL.
- Kubernetes liveness/readiness probe examples work in docker-compose.

Reference: `docs/architecture/operations.md#health-checks`.

Dependencies and out of scope: depends on T141-T148. Do not add /health deprecation, 
upstream connectivity check, limiter state checks, or detailed metrics yet.

### T150 - Add Prometheus /metrics endpoint

Goal: expose operational metrics in Prometheus format for observability without 
adding heavy metric dependencies or changing hot paths.

Scope:

- Add `GET /metrics` (no auth required) returning Prometheus text format. Use 
  `prometheus/client_golang` or minimal compatible implementation.
- Expose counters from existing atomic counters and telemetry worker: 
  `gateway_requests_total{route,method,status,outcome}`, 
  `gateway_request_errors_total{error_code}`, 
  `gateway_upstream_requests_total{status}`, 
  `gateway_telemetry_jobs_total{result}` (persisted/dropped/failed).
- Expose gauges: `gateway_active_requests`, `gateway_telemetry_queue_depth`.
- Add histograms for latency (use existing trace timing): 
  `gateway_request_duration_seconds`, `gateway_upstream_duration_seconds`, 
  `gateway_ttfb_seconds`. Use reasonable buckets: [.001,.005,.01,.025,.05,.1,.25,.5,1,2.5,5,10].
- Instrument in `httpserver` at existing trace/completion boundaries. No per-request 
  allocation or lock contention on hot path. Use lock-free atomics where possible.

Acceptance and tests:

- HTTP test: `/metrics` returns valid Prometheus format parseable by prometheus 
  parser library. Counter/gauge/histogram syntax correct.
- Integration test: perform requests (success, error, rejection), verify counters 
  increment correctly, histogram buckets populated, labels accurate.
- Metrics collection doesn't delay response headers, first byte, flush, or EOF.
- No credentials, API keys, model names with PII, or body content in metric labels.
- Concurrent requests under race detector don't cause metric races.

Reference: `docs/architecture/operations.md#metrics`.

Dependencies and out of scope: depends on T149. Do not add custom metric 
registration API, metric push, exemplars, /metrics admin auth, per-key metrics, 
or OpenTelemetry yet.

### T151 - Implement ordered graceful shutdown

Goal: coordinate subsystem shutdown in correct dependency order so in-flight 
requests complete, telemetry drains, and no data corruption occurs.

Scope:

- Extend existing `Run()` in `cmd/gateway/main.go` to handle `SIGTERM`/`SIGINT`. 
  Shutdown order: stop accepting new connections → wait for active handlers (with 
  timeout) → stop accounting observation worker → drain telemetry queue → close SQLite.
- Use `http.Server.Shutdown(ctx)` with configurable timeout (default 30s). Active 
  requests have this time to complete before force-close.
- Accounting observation and telemetry workers receive shutdown signal, finish 
  current job, drain bounded queue up to shutdown deadline, then stop. Unprocessed 
  telemetry is dropped with logged count.
- SQLite close is the final step. If critical accounting writes are pending, they 
  must complete before SQLite closes. Telemetry writes are best-effort.
- Log shutdown phases: "shutting down HTTP server", "draining telemetry 
  (N pending)", "closing storage", "shutdown complete". Include dropped counts.

Acceptance and tests:

- Integration test: start gateway, send requests, send SIGTERM during active 
  request, verify request completes successfully, telemetry written, clean exit.
- Test: shutdown with saturated telemetry queue drains up to deadline, logs 
  dropped count, doesn't corrupt SQLite.
- Test: shutdown timeout expires, active requests cancelled, exit without hang.
- Race detector clean. No goroutine leaks (use goleak if available).
- `/ready` returns 503 immediately after shutdown signal received.

Reference: `docs/architecture/operations.md#server-lifecycle`.

Dependencies and out of scope: depends on T150. Do not add hot reload, zero-downtime 
restart, graceful upgrade, or connection draining beyond stdlib `Shutdown()`.

## Security Hardening

### T152 - Harden path traversal and injection risks

Goal: prevent path traversal, header injection, and malformed input attacks in 
admin and proxy paths without breaking legitimate Unicode or encoded content.

Scope:

- Validate admin route parameters `:id` and `:kind`: reject patterns containing 
  `..`, null bytes, control characters, path separators. Allow alphanumeric, hyphen, 
  underscore, and forward slash only where semantically valid.
- Sanitize cursor values: validate base64/URL-safe encoding before decode. Reject 
  cursors with embedded newlines, nulls, or exceeding reasonable length (1KB).
- Validate upstream URL from config at startup: must be valid absolute HTTP/HTTPS 
  URL, reject file://, javascript:, data:, and relative paths. Reject URLs with 
  embedded credentials in production environments.
- Escape/quote values in structured logs. Never interpolate user input directly 
  into log format strings. Use `slog` structured attributes exclusively.
- Add request size limits enforced before reading: max header size 16KB, max URL 
  length 8KB, max query string 4KB. Return 431 (Request Header Fields Too Large).

Acceptance and tests:

- Security tests: path traversal attempts in key ID (`../../../etc/passwd`), null 
  bytes in request ID, oversized cursors, malformed base64, control chars in model name.
- Log injection test: malicious input with newlines/ANSI codes doesn't corrupt log 
  output or create fake log entries.
- Upstream URL validation: reject `file:///etc/passwd`, `javascript:alert()`, 
  relative URLs, credentials in URL (`http://user:pass@host`).
- Legitimate use cases still work: Unicode model names, long but valid request IDs, 
  URL-encoded query parameters in generic passthrough.
- All rejection paths return appropriate 4xx, never 5xx for validation failures.

Reference: `docs/architecture/transport.md#security`.

Dependencies and out of scope: depends on T151. Do not add rate limiting by IP, 
WAF rules, SQL injection prevention (already using parameterized queries), CSRF 
tokens, or content security policy headers yet.

### T153 - Enforce body size limits consistently

Goal: prevent memory exhaustion and abuse by enforcing documented limits on request 
and response body sizes at all ingress points.

Scope:

- Enforce `http.MaxBytesReader` on incoming client request bodies: 10MB hard limit 
  before any policy inspection or body capture. Return 413 (Payload Too Large) with 
  structured error if exceeded.
- Enforce upstream response body size limit: 100MB for non-streaming responses. For 
  streaming SSE, enforce per-chunk size sanity (individual SSE event <1MB) but allow 
  unbounded total stream as long as chunks are consumed.
- Validate `Content-Length` header if present against limits before reading body. 
  Reject oversized declared lengths immediately.
- Body capture respects T130 `max_captured_body_bytes` (max 1MB) but does not 
  reject requests that exceed it—only truncates capture. The 10MB request and 100MB 
  response limits are separate enforcement boundaries.
- Add metrics: `gateway_rejected_requests_total{reason="body_too_large"}`.

Acceptance and tests:

- HTTP tests: 9.9MB request succeeds, 10.1MB returns 413, oversized `Content-Length` 
  rejected before reading body, chunked upload enforced incrementally.
- Streaming SSE with 500MB total succeeds if individual events small. Single 2MB 
  SSE event is rejected or truncated with error.
- Body capture correctly truncates at 1MB even when request is 5MB and allowed.
- Memory usage doesn't spike: oversized request doesn't buffer entire body into RAM.
- Error responses include helpful message and max size, no stack trace or internals.

Reference: `docs/architecture/transport.md#request-body`, 
`docs/architecture/observability.md#body-capture`.

Dependencies and out of scope: depends on T152. Do not add per-key body size limits, 
streaming request upload progress, multipart form limits, or dynamic limit adjustment.

### T154 - Add secret redaction audit

Goal: systematically verify no credentials, keys, or sensitive config values can 
leak through logs, metrics, errors, or admin API responses.

Scope:

- Create `internal/security/redaction_test.go` with comprehensive leak detection 
  tests. Use canary values for: raw API key, admin credential, auth pepper, upstream 
  API key, SQLite password (if applicable), request body with mock secrets.
- Test all error paths: malformed requests, auth failures, storage errors, upstream 
  errors, timeout, cancellation. Capture structured logs, HTTP error bodies, panic 
  recovery messages.
- Test all admin API endpoints with injected sensitive data in edge cases: keys 
  named with credential patterns, models containing secrets, malformed policy JSON 
  with embedded keys.
- Verify cursor values don't encode raw IDs or offsets that could leak DB structure.
- Check metric labels and histogram buckets don't include API keys or user data.
- Add CI test that fails if any canary value appears in captured output.

Acceptance and tests:

- Comprehensive secret canary tests covering 50+ scenarios across all major code paths.
- Test passes: no canary value (full or substring) appears in any log, error, metric, 
  admin response, or panic message.
- Legitimate data still present: cost values, token counts, timing, bounded model 
  names, request IDs, stable key IDs (not raw keys).
- Test documents each checked scenario with comments explaining the leak risk.
- Add to CI: `go test -v ./internal/security -run TestSecretRedaction`.

Reference: `docs/architecture/observability.md#logging`, 
`docs/architecture/operations.md#security`.

Dependencies and out of scope: depends on T153. Do not add runtime secret scanning, 
DLP integration, audit log export, or credential rotation mechanism yet.

## Packaging and Deployment

### T155 - Create minimal Docker image

Goal: build a production-ready Docker image with multi-stage build, minimal attack 
surface, and no unnecessary tooling or files in the final image.

Scope:

- Create `Dockerfile` with multi-stage build: build stage with Go toolchain, runtime 
  stage with minimal base (distroless, alpine, or scratch with CA certificates).
- Build both `gateway` and `gwctl` binaries. Final image contains `/gateway`, 
  `/gwctl`, CA certs for HTTPS upstream, and timezone data.
- Image runs as non-root user (UID 65532). SQLite DB path defaults to `/data/gateway.db`. 
  Config path `/etc/gateway/config.yaml` or overridable via `--config` flag.
- Support build args: `VERSION`, `COMMIT_SHA`, `BUILD_DATE`. Embed these in binary 
  via `-ldflags` for `gwctl version` output.
- Image size target: <50MB compressed. No gcc, shells, package managers, or source 
  code in final image.
- Health check: `HEALTHCHECK --interval=30s --timeout=3s CMD ["/gateway", "healthcheck"]` 
  or HTTP GET to `/ready`.

Acceptance and tests:

- Build: `docker build -t 9gateway:latest .` succeeds in <2min.
- Run: `docker run -v ./data:/data -v ./config.yaml:/etc/gateway/config.yaml -p 8080:8080 9gateway:latest` 
  starts gateway successfully.
- Image inspection: final stage has no shell, minimal layers, runs as non-root, 
  contains only necessary binaries and certs.
- Security scan: `docker scan` or `trivy` shows no high/critical vulnerabilities.
- Multi-arch: document build for linux/amd64 and linux/arm64 (actual multi-arch 
  build optional, can be follow-up).

Reference: `docs/architecture/operations.md#deployment`.

Dependencies and out of scope: depends on T154. Do not add Kubernetes manifests, 
Helm charts, auto-scaling, or multi-arch automated builds yet.

### T156 - Create docker-compose example

Goal: provide a complete working docker-compose setup for local development and 
small production deployments with gateway, mock upstream, and observability.

Scope:

- Create `docker-compose.yml` with services: `gateway` (using T155 image), 
  `mock-upstream` (simple OpenAI-compatible mock from test helpers), optional 
  `prometheus` for metrics scraping.
- Gateway service: volume mounts for `./data` (SQLite), `./config.yaml`, exposes 
  8080, environment variables for secrets, health checks enabled, restart policy.
- Mock upstream: builds from `tests/mock-upstream` (create minimal Go server 
  responding to `/v1/chat/completions`), exposes 8081 internally.
- Prometheus (optional): scrapes `gateway:8080/metrics` every 15s, web UI on 9090.
- Include `.env.example` with: `UPSTREAM_API_KEY`, `ADMIN_CREDENTIAL`, `AUTH_PEPPER`.
- Include `config.example.yaml` with reasonable defaults pointing to `mock-upstream:8081`.
- Document in `README.md`: `docker-compose up` starts everything, create first key 
  with `docker-compose exec gateway gwctl keys create`, test with `curl`.

Acceptance and tests:

- `docker-compose up` starts all services successfully on fresh checkout.
- Gateway healthcheck passes. Prometheus scrapes metrics successfully.
- Create API key via gwctl, send request via curl to gateway, gateway proxies to 
  mock-upstream, response returns correctly, request appears in history.
- `docker-compose down` stops cleanly, `docker-compose down -v` removes volumes.
- README walkthrough completeable by new user in <5 minutes.

Reference: `docs/architecture/operations.md#deployment`.

Dependencies and out of scope: depends on T155. Do not add Traefik/nginx reverse 
proxy, TLS termination, log aggregation, or distributed tracing yet.

### T157 - Add configuration validation and startup checks

Goal: fail fast on startup with clear error messages for invalid configuration, 
missing secrets, or incompatible settings before accepting any traffic.

Scope:

- Validate all config fields at startup before listener opens: required fields 
  present, types correct, ranges valid, durations parseable, URLs absolute HTTP/HTTPS.
- Validate secrets resolved from environment variables exist and non-empty: 
  `upstream_api_key`, `admin_credential`, `auth_pepper`. Never log actual values.
- Validate SQLite path writable, schema version compatible. If DB doesn't exist, 
  create with correct schema. If exists with wrong version, fail with upgrade message.
- Validate pricing patterns compilable, tokenizer mode valid, observability limits 
  within safety bounds (T130).
- Cross-field validation: body retention <= request retention, upstream URL doesn't 
  point to gateway itself (detect simple loops), listen addr not privileged if 
  running as non-root.
- Exit code 1 with clear error message on validation failure. Error format: 
  `"config validation failed: field 'X': reason"`. Never dump entire config.

Acceptance and tests:

- Unit tests: every invalid config variant triggers specific validation error with 
  field name, valid configs pass.
- Integration tests: start with missing secret (env not set) fails, wrong SQLite 
  schema fails, circular upstream URL fails, out-of-range retention fails.
- Error messages helpful: "upstream_api_key environment variable 'UPSTREAM_API_KEY' 
  not set", not "invalid config".
- Valid minimal config starts successfully even with optional fields omitted.

Reference: `docs/architecture/operations.md#configuration`.

Dependencies and out of scope: depends on T156. Do not add config hot reload, 
validation API endpoint, config migration tool, or schema documentation generator.

### T158 - Add version information and build metadata

Goal: embed version, commit, and build info in binaries for operational tracking 
and support diagnostics without requiring external version files.

Scope:

- Add `internal/version/version.go` with variables: `Version`, `CommitSHA`, 
  `BuildDate`, `GoVersion`. Set via `-ldflags` during build.
- Add `gateway --version` and `gwctl version` commands printing: version string, 
  commit SHA (short), build date, Go version, OS/arch.
- Include version in startup logs: `"starting gateway version=v0.1.0 commit=abc123 
  build=2026-09-13T21:00:00Z"`.
- Add version to `/ready` response JSON: `"version": "v0.1.0"`, `"commit": "abc123"`.
- Add version comment to generated metrics: `# gateway version v0.1.0`.
- Default version when not set via ldflags: `"dev"`, commit: `"unknown"`, build: 
  `"unknown"`.

Acceptance and tests:

- Build with ldflags: 
  `go build -ldflags="-X internal/version.Version=v0.1.0 -X internal/version.CommitSHA=$(git rev-parse --short HEAD)"` 
  produces binary with correct version output.
- `gateway --version` and `gwctl version` show version info, exit 0.
- Startup log includes version. `/ready` JSON includes version fields.
- Build without ldflags uses "dev" defaults, no errors.
- Docker image T155 embeds version from build args.

Reference: `docs/architecture/operations.md#versioning`.

Dependencies and out of scope: depends on T157. Do not add auto-versioning from 
git tags, changelog generation, update checking, or semantic version parsing yet.

## Documentation and Testing

### T159 - Add comprehensive integration test suite

Goal: create end-to-end integration tests covering complete gateway lifecycle with 
real HTTP, SQLite, and all subsystems working together.

Scope:

- Add `internal/integration/gateway_test.go` with test harness that starts real 
  gateway server, mock upstream, and runs complete scenarios against live HTTP.
- Test scenarios: key creation → policy update → authenticated requests (JSON, SSE, 
  converted) → token/budget enforcement → request history → body retrieval → 
  graceful shutdown.
- Multi-key scenario: parallel requests from different keys with different policies, 
  verify isolation (no cross-key limit leakage).
- Failure scenario: upstream errors, timeout, cancelled requests, rejected requests, 
  saturated telemetry queue, verify correct accounting and history.
- Performance regression: measure TTFB and stream-close delay, verify <10ms overhead 
  vs direct mock upstream, verify no artificial delays (T128 regression).
- Use real SQLite (tmpfile), real HTTP server, real time (with accelerated retention 
  for testing). No mocks of core gateway components.

Acceptance and tests:

- `go test ./internal/integration -v` runs full suite in <30s, all scenarios pass.
- Tests cover at least 80% of happy path code (check with `go test -cover`).
- Performance test: 100 concurrent SSE requests complete with mean stream-close 
  delay <50ms, no request takes >10s total.
- Tests clean up: no leaked goroutines (use goleak), temp files deleted, ports released.
- Tests pass on CI with race detector: `go test -race ./internal/integration`.
- Documented: each scenario has comment explaining what it validates and why.

Reference: `docs/architecture/testing.md#integration`.

Dependencies and out of scope: depends on T158. Do not add load testing, chaos 
testing, fuzz testing beyond existing unit fuzzing, or benchmark suite yet.

### T160 - Complete milestone documentation and release prep

Goal: finalize documentation, verify all acceptance criteria met, update CURRENT.md, 
and prepare for first tagged release.

Scope:

- Update `README.md` with complete quickstart: prerequisites, installation 
  (Docker/binary), configuration, create first key, send first request, view history.
- Add `docs/operations/` guides: configuration reference (all YAML fields), admin 
  API reference (all endpoints with examples), CLI reference (`gwctl` commands), 
  deployment guide (Docker, docker-compose, binary).
- Update `CURRENT.md`: mark T141-T160 done, document milestone completion: "The 
  gateway now provides complete admin read API, CLI tool, health/metrics endpoints, 
  graceful shutdown, security hardening, and production packaging."
- Audit task completion: verify every T141-T160 acceptance criterion met, all tests 
  pass, no broken functionality, no TODO comments in critical paths.
- Verify end-to-end: follow README quickstart on clean machine/container, ensure 
  every command works as documented.
- Tag release candidate: prepare for `v0.1.0-rc1` with changelog from T141-T160.

Acceptance and tests:

- README walkthrough works on Ubuntu 22.04/24.04 and macOS from clean state.
- All documentation cross-references valid (no broken links to non-existent files).
- `go test ./...` passes all tests. `go build ./...` builds all binaries.
- Docker image builds and runs per README. docker-compose setup works.
- CI green: tests, lints, builds all pass. No race conditions.
- `CURRENT.md` accurately reflects completed work. Next milestone (T161+) clearly 
  delineated as out of scope.
- Git log shows all T141-T160 tasks committed with proper messages.

Reference: All architecture docs in `docs/architecture/`.

Dependencies and out of scope: final task in T141-T160 milestone. Do not implement 
T161+ tasks (Web UI, advanced features, provider routing). Do not add contribution 
guidelines, governance, or public release announcement yet.

---

## Milestone Summary

After completing T141-T160, the gateway provides:

**Admin & Operations:**
- Complete admin read API (keys, requests, bodies)
- `gwctl` CLI for management and inspection
- `/ready` endpoint with deep health checks
- `/metrics` Prometheus endpoint
- Ordered graceful shutdown

**Security & Hardening:**
- Path traversal protection
- Body size enforcement (10MB request, 100MB response)
- Secret redaction audit
- Non-root Docker execution

**Deployment:**
- Production Docker image (<50MB)
- docker-compose example with mock upstream
- Configuration validation with clear errors
- Version embedding and build metadata

**Testing & Docs:**
- Comprehensive integration test suite
- Complete operational documentation
- Verified README quickstart
- Release candidate ready

**Next Milestone (T161+, not in this batch):**
Web UI, advanced request filtering, usage analytics, key deletion, policy templates, 
request replay, export capabilities, and enhanced observability.

