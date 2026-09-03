# Active Tasks

This file contains the next twenty atomic tasks. An implementation agent reads
only `AGENTS.md`, `CURRENT.md`, its assigned task, and the architecture documents
linked by that task. Tasks are ordered by dependency and must not be implemented
out of order. Do not load `docs/tasks/backlog-full.md` during routine work.

Every implementation task must finish with `go fmt ./...`, `go test ./...`, and
`go build ./...`. Add behavioral tests in the same task as changed HTTP or
streaming behavior. Update `CURRENT.md` and commit after completing one task.

## Transport Compatibility Hardening

### T061 - Sanitize transformed response headers

Goal: ensure the explicit SSE-to-JSON compatibility path does not forward
representation metadata that describes the discarded upstream SSE body.

Scope:

- Define the narrow set of representation-specific headers invalidated when an
  upstream SSE representation is replaced with generated JSON.
- Remove at least `Content-Encoding`, `Content-Length`, `Content-Range`,
  `Accept-Ranges`, `ETag`, `Content-MD5`, `Digest`, and `Content-Digest` from the
  transformed response while retaining safe end-to-end headers.
- Set the generated JSON content type and exact content length only after
  successful aggregation.
- Leave transparent JSON, opaque, and SSE responses byte- and header-preserving.

Acceptance and tests:

- A real transformed response cannot retain an upstream validator, digest,
  range, encoding, or length header.
- Safe headers such as request correlation and rate-limit metadata survive the
  conversion.
- Transparent response tests prove the same headers remain untouched when no
  transformation occurs.

Reference: `docs/architecture/transport.md#headers`,
`docs/architecture/streaming.md#sse-to-json`.

Dependencies and out of scope: depends on T060. Do not generalize response
rewriting, change upstream statuses, parse cache policy, or alter transparent
transport.

### T062 - Do not wait for EOF after unencoded DONE

Goal: prevent a non-stream client from hanging when an identity-encoded upstream
SSE response sends exact `[DONE]` but keeps the HTTP body open.

Scope:

- For the explicit `stream:false` plus upstream-SSE conversion path, return the
  rendered JSON immediately after exact `[DONE]` when no content decoding needs
  trailer validation.
- Close the upstream body through the existing request lifecycle so an upstream
  handler blocked after `[DONE]` is canceled and its connection is not reused.
- Retain clean-EOF completion when `[DONE]` is absent.
- Retain bounded draining where a supported content coding requires trailer
  validation; do not weaken gzip corruption detection.
- Do not introduce grace periods, idle timers, or finish-reason completion.

Acceptance and tests:

- A real identity-encoded upstream that flushes `[DONE]` and then blocks produces
  downstream JSON promptly without waiting for upstream EOF.
- The blocked upstream observes cancellation after the generated response is
  complete.
- No-`[DONE]` EOF, valid gzip, corrupt gzip, bytes after `[DONE]`, and client
  cancellation regressions remain deterministic and bounded.

Reference: `docs/architecture/streaming.md#termination`,
`docs/architecture/testing.md#regressions`.

Dependencies and out of scope: depends on T061. Do not change transparent SSE,
synthesize `[DONE]`, add a streaming timeout, or accept incomplete SSE framing.

### T063 - Decouple completion logging from response close

Goal: ensure a slow structured-log sink cannot delay downstream EOF after the
proxy has finished delivering a response.

Scope:

- Introduce the smallest bounded, non-blocking completion-record handoff between
  HTTP middleware and structured logging.
- Enqueue one immutable completion record after request handling; when the bound
  is full, drop the record explicitly rather than blocking transport.
- Keep request ID, method, path, status, and duration while excluding
  Authorization and credentials.
- Give the process entry point ownership of logger-worker shutdown; shutdown may
  drain only for a bounded period.
- Do not put per-chunk or per-token events on this path.

Acceptance and tests:

- A deliberately blocked log sink cannot delay client-observed SSE EOF or an
  ordinary response completion.
- Queue saturation is deterministic, non-blocking, and exposes a dropped-record
  count without unbounded goroutines.
- Normal completion records preserve current safe fields, and shutdown does not
  leak a worker.

Reference: `docs/architecture/observability.md#logging`,
`docs/architecture/observability.md#telemetry`,
`docs/architecture/operations.md#server-lifecycle`.

Dependencies and out of scope: depends on T062. Do not add SQLite telemetry,
body capture, Prometheus, usage accounting, or a general event bus.

## SQLite And Key Foundation

### T064 - Add storage and credential configuration

Goal: define the deployment configuration needed for SQLite-backed gateway keys
without placing runtime key records or raw gateway keys in YAML.

Scope:

- Add a required SQLite path, an authentication pepper environment reference,
  and a distinct admin credential environment reference to system config.
- Generalize the existing strict environment-reference resolver so secrets are
  resolved consistently without embedding secret names in field-specific code.
- Reject empty resolved secrets, identical admin/upstream credentials, malformed
  references, and unusable storage paths before starting the listener.
- Keep strict unknown-YAML-field handling and preserve existing upstream config.

Acceptance and tests:

- Table tests cover valid file paths, `:memory:` for tests, missing environment
  variables, empty values, malformed references, and credential separation.
- Validation errors identify the configuration field but never print a resolved
  secret.
- No raw gateway API key or per-key policy field is added to YAML.

Reference: `docs/architecture/operations.md#configuration`,
`docs/architecture/storage.md#boundaries`,
`docs/architecture/policy.md#authentication`.

Dependencies and out of scope: depends on T063. Do not open SQLite, define key
tables, add hot reload, or add admin HTTP routes.

### T065 - Open and configure SQLite

Goal: add the minimal SQLite storage lifecycle with explicit operational
settings suitable for a single gateway instance.

Scope:

- Choose a maintained Go SQLite driver compatible with the project's single
  binary deployment and document any build implications in the task commit.
- Add `internal/storage` ownership of opening, pinging, and closing the database.
- Enable WAL mode for file databases, foreign keys, a bounded busy timeout, and
  conservative connection-pool settings.
- Return contextual startup errors without leaking credentials or silently
  creating parent directories.
- Keep SQL entirely off streaming and limiter hot paths.

Acceptance and tests:

- Temporary-file integration tests verify WAL, foreign keys, reopen behavior,
  and clean close; in-memory tests use a configuration that cannot split state
  across pooled connections.
- An invalid/unwritable path fails before HTTP serving.
- `Close` and startup-failure cleanup are safe and deterministic.

Reference: `docs/architecture/storage.md#boundaries`,
`docs/architecture/operations.md#server-lifecycle`.

Dependencies and out of scope: depends on T064. Do not create application
tables, repositories, telemetry writers, runtime counters, or distributed locks.

### T066 - Add embedded schema migrations

Goal: create a deterministic migration runner and the first persistent API-key
schema without requiring an external migration binary.

Scope:

- Embed ordered SQL migrations in `internal/storage` and apply them
  transactionally during startup.
- Track an explicit schema version and reject a database newer than this binary.
- Create `api_keys` with ID, display name, display prefix, keyed hash, enabled
  state, optional expiration, timestamps, and policy JSON.
- Add constraints and indexes needed for unique identity/prefix lookup and active
  key loading.
- Never add a column for a raw gateway key or authentication pepper.

Acceptance and tests:

- A fresh database reaches the expected version and exposes the expected table,
  columns, constraints, and indexes.
- Reopening is idempotent; a failed migration rolls back; an unknown future
  version fails safely.
- Schema inspection proves raw keys and pepper are absent.

Reference: `docs/architecture/storage.md#boundaries`,
`docs/architecture/storage.md#api-keys`.

Dependencies and out of scope: depends on T065. Do not add request history,
usage aggregates, policy semantics, downgrade migrations, or admin routes.

### T067 - Define API key records and repository

Goal: persist and retrieve gateway-key records through narrow domain types that
do not expose SQLite details to authentication or policy code.

Scope:

- Define validated records for ID, name, display prefix, fixed-size HMAC digest,
  enabled state, optional expiration, timestamps, and opaque policy JSON.
- Implement insert, lookup by display prefix, get by ID, list in deterministic
  order, and enabled-state update against the T066 schema.
- Copy byte slices at the boundary so callers cannot mutate stored digest state.
- Distinguish not-found and uniqueness conflicts with recognizable safe errors.
- Never accept or return the raw key or pepper.

Acceptance and tests:

- Integration tests cover round trips, expiration nullability, duplicate
  constraints, deterministic listing, enable/disable, not-found, and reopen.
- Malformed digests, empty identity fields, and invalid timestamps are rejected.
- Repository errors contain no policy contents, digest bytes, or credentials.

Reference: `docs/architecture/storage.md#api-keys`,
`docs/architecture/repository.md#dependencies`.

Dependencies and out of scope: depends on T066. Do not generate keys, interpret
policy JSON, add HTTP, cache records, or update last-used timestamps.

### T068 - Generate and fingerprint gateway keys

Goal: issue recognizable random gateway API keys while persisting only a safe
display prefix and `HMAC-SHA256(pepper, raw_key)`.

Scope:

- Add a cryptographically secure key generator with the `sk-gw-` namespace and
  enough random entropy for online credentials.
- Derive an unambiguous display prefix suitable for indexed candidate lookup.
- Compute the keyed hash with HMAC-SHA256 and compare hashes with constant-time
  primitives.
- Return the raw key only in the creation result; the repository record contains
  only its display prefix and digest.
- Make randomness injectable only where deterministic tests require it.

Acceptance and tests:

- Generated keys have the documented format, unique random material, and stable
  digest for the same pepper/key.
- Different peppers or keys produce different digests; malformed key formats are
  rejected before lookup.
- Tests prove records, JSON/debug formatting, and errors do not contain the raw
  key or pepper.

Reference: `docs/architecture/policy.md#authentication`,
`docs/architecture/storage.md#api-keys`.

Dependencies and out of scope: depends on T067. Do not add password hashing,
key import, rotation, HTTP authentication, or admin authorization.

### T069 - Add admin-authenticated key creation

Goal: provide the minimal administrative bootstrap operation that creates a
persistent gateway key and reveals its raw value exactly once.

Scope:

- Add `POST /admin/v1/keys` protected by the distinct configured admin bearer
  credential using constant-time comparison.
- Accept a bounded JSON body containing display name and optional expiration;
  reject unknown fields, malformed types, trailing JSON, and oversized input.
- Generate a key through T068, insert its record through T067 with an empty
  initial policy, and return HTTP 201 with safe metadata plus the one-time raw key.
- Keep `/health` public and ensure gateway keys cannot authorize admin routes.
- Use a package-private service boundary so later CLI and policy operations reuse
  behavior rather than SQL.

Acceptance and tests:

- Real HTTP tests cover success, persistence after reopen, missing/wrong/admin
  credentials, gateway-key rejection, invalid body, duplicate-safe generation
  failure, and no upstream call.
- The raw key appears only in the successful creation response and never in logs
  or subsequent repository reads.
- Admin responses receive a gateway request ID and use safe JSON errors.

Reference: `docs/architecture/operations.md#administration`,
`docs/architecture/policy.md#authentication`,
`docs/architecture/testing.md#security-and-performance`.

Dependencies and out of scope: depends on T064 and T067-T068. Do not add list,
revoke, CLI, policy editing, body logging, or ordinary `/v1/*` authentication.

### T070 - Load an immutable authentication snapshot

Goal: keep request-time authentication independent from SQLite by loading active
key candidates into a concurrency-safe immutable in-memory snapshot.

Scope:

- Define a storage-independent auth record and snapshot keyed by display prefix,
  allowing collision candidates without storing raw keys or pepper.
- Load all key records at startup and atomically replace the whole snapshot after
  a successful administrative mutation.
- Authenticate by format/prefix lookup, HMAC-SHA256 with the process pepper, and
  constant-time digest comparison.
- Return an immutable principal containing safe key identity and policy bytes.
- Distinguish internal invalid, disabled, and expired outcomes using an
  injectable clock while exposing no credential detail in errors.

Acceptance and tests:

- Tests cover valid, malformed, unknown, colliding-prefix, wrong-digest,
  disabled, expired, and no-expiration keys.
- Concurrent readers observe either the old or new complete snapshot, never a
  partial mutation; run relevant tests with the race detector.
- Request-time authentication performs no SQL, and returned state cannot mutate
  the snapshot.

Reference: `docs/architecture/policy.md#authentication`,
`docs/architecture/storage.md#aggregates-and-caches`.

Dependencies and out of scope: depends on T067-T069. Do not add HTTP middleware,
rate limits, last-used writes, periodic refresh, or multi-instance invalidation.

## Authentication And Model Policy

### T071 - Render OpenAI-style gateway errors

Goal: give gateway-owned failures a stable, secret-safe OpenAI-compatible JSON
shape before authentication begins rejecting public requests.

Scope:

- Define a narrow error response with `message`, `type`, optional `param`, and
  stable `code` inside the standard `error` envelope.
- Add mappings for invalid API key, disabled/expired key, model denial, request
  limit, concurrency limit, malformed admin request, upstream connection error,
  and gateway internal error.
- Set JSON content type and exact status before writing a gateway-generated body.
- Preserve the existing request-ID response header.
- Do not rewrite normal upstream error statuses, headers, or bodies.

Acceptance and tests:

- Decoded response tests verify each code/status mapping and optional fields.
- Messages and encoded bodies cannot contain presented keys, pepper, upstream
  credentials, URLs with userinfo, SQL, or parser internals.
- An upstream 4xx/5xx remains byte-transparent when no gateway transformation is
  required.

Reference: `docs/architecture/observability.md#logging`,
`docs/architecture/testing.md#security-and-performance`.

Dependencies and out of scope: depends on T070. Do not introduce a broad error
framework, rewrite every existing plain-text startup error, or add retries.

### T072 - Authenticate all public v1 requests

Goal: require a valid gateway bearer key on `/v1/*` before any upstream work and
make its safe principal available to later policy middleware.

Scope:

- Accept exactly one syntactically valid `Authorization: Bearer <gateway-key>`
  value; reject missing, repeated, malformed, unknown, disabled, and expired
  credentials through T071.
- Authenticate before request inspection, limiting, or upstream client calls and
  place the immutable principal in request context.
- Always replace the client credential with the configured 9router credential on
  admitted upstream requests.
- Keep `/health` public and `/admin/v1/*` exclusively under admin authentication.
- Avoid reading or buffering the request body on rejected requests.

Acceptance and tests:

- Real HTTP tests prove every rejection makes zero upstream calls and receives a
  request ID plus safe OpenAI-style error.
- A valid key preserves method, path, query, body, cancellation, concurrency, and
  existing streaming behavior.
- Gateway credentials never reach upstream or completion logs.

Reference: `docs/architecture/policy.md#authentication`,
`docs/architecture/repository.md#request-orchestration`,
`docs/architecture/testing.md#security-and-performance`.

Dependencies and out of scope: depends on T070-T071. Do not add model checks,
rate/concurrency limits, anonymous mode, alternate auth schemes, or admin changes.

### T073 - Define and validate key policy JSON

Goal: turn stored policy JSON into one immutable effective policy containing only
the model and request/concurrency fields needed by this milestone.

Scope:

- Define policy fields for exact/glob model allow and deny lists, generic request
  windows, and optional maximum concurrency.
- Strictly decode stored JSON, reject unknown fields, invalid glob syntax,
  duplicate rules, non-positive amounts/durations, and invalid concurrency.
- Compile model patterns and normalized durations while loading the auth snapshot,
  never per request.
- Define empty policy as unrestricted and deny precedence over allow.
- Keep the compiled policy storage-independent and immutable to callers.

Acceptance and tests:

- Table tests cover empty, valid combined, unknown-field, malformed-pattern,
  invalid-window, invalid-concurrency, duplicate, and mutation-isolation cases.
- Invalid policy prevents the affected snapshot replacement rather than silently
  granting broader access.
- No regex engine, token limits, budgets, logging policy, or pricing fields are
  introduced.

Reference: `docs/architecture/policy.md#effective-policy`,
`docs/architecture/storage.md#api-keys`.

Dependencies and out of scope: depends on T070. Do not add policy admin routes,
global/model-specific counters, tokenization, or database schema normalization.

### T074 - Enforce model allow and deny rules

Goal: reject a known disallowed model after authentication but before upstream
work without changing request bytes.

Scope:

- Evaluate exact and glob patterns against the parsed model on inspectable
  `POST /v1/chat/completions` requests.
- Make deny override allow; when an allow list exists, require a match; an empty
  allow list permits models not denied.
- Return the T071 model error with `param:"model"` before upstream transport.
- Preserve transparent passthrough when the endpoint or model is unknown because
  the body is malformed, oversized, unsupported, or intentionally uninspected.
- Do not reserialize, normalize, alias, or route model names.

Acceptance and tests:

- Tests cover exact/glob allow, exact/glob deny, deny precedence, allow-only miss,
  unrestricted policy, case sensitivity, and multiple keys with different policy.
- Rejected requests never reach upstream; admitted bodies remain byte-identical.
- Unknown `/v1/*`, malformed JSON, and over-limit bodies retain existing behavior.

Reference: `docs/architecture/policy.md#effective-policy`,
`docs/architecture/transport.md#generic-passthrough`.

Dependencies and out of scope: depends on T072-T073. Do not reject unknown
models, inspect additional endpoints, add aliases, or modify payloads.

### T075 - Add admin key policy updates

Goal: make a persistent key's enabled state and milestone policy administratively
changeable without exposing secret material.

Scope:

- Add admin-authenticated `PUT /admin/v1/keys/{id}/policy` with a bounded strict
  JSON body containing `enabled` and the complete T073 policy document.
- Validate and compile the replacement policy before changing persistent state.
- Persist status/policy together, then rebuild and atomically publish the T070
  snapshot; define a safe error if refresh fails instead of publishing partial
  state.
- Make replacement idempotent and return safe validation and not-found responses.
- Return only deterministic safe key metadata and policy, never digest, pepper,
  admin credential, upstream credential, or raw key.

Acceptance and tests:

- Real HTTP tests cover valid replacement, invalid policy rollback, enable/disable,
  idempotence, not-found, unauthorized access, immediate auth/policy effect, and
  persistence after reopen.
- A disabled key cannot call `/v1/*`, and changed model/limit policy is visible to
  the next authenticated request without process restart.
- Responses and logs contain no raw key or digest material.

Reference: `docs/architecture/operations.md#administration`,
`docs/architecture/storage.md#aggregates-and-caches`.

Dependencies and out of scope: depends on T069-T073. Do not add delete, policy
patch semantics, list endpoints, CLI, usage history, key rotation, or last-used
tracking.

## Request And Concurrency Limits

### T076 - Implement generic per-key request windows

Goal: atomically enforce every configured request-count window for a key using
in-memory fixed windows and deterministic time.

Scope:

- Add a minimal injectable clock and define fixed-window boundaries explicitly.
- Track counters by stable key ID and normalized window, with no shared capacity
  between keys.
- Check all configured windows and consume one request from all of them atomically
  only when every window admits the request.
- Return the earliest useful reset time on rejection and lazily discard expired
  state so inactive keys do not grow memory forever.
- Treat RPM as ordinary `amount / 1m` policy, not a separate algorithm.

Acceptance and tests:

- Unit tests cover exact capacity, one-over-limit, boundary reset, multiple
  simultaneous windows, different keys, clock movement, cleanup, and concurrent
  attempts without sleeps.
- A rejected multi-window attempt consumes no capacity in any window.
- The race detector proves configured capacity cannot be oversubscribed.

Reference: `docs/architecture/policy.md#request-limits`,
`docs/architecture/testing.md#limit-tests`.

Dependencies and out of scope: depends on T073. Do not add sliding windows,
refunds, SQLite counters, distributed limiting, tokens, budgets, or HTTP behavior.

### T077 - Integrate request limits and Retry-After

Goal: consume per-key request capacity before upstream work and return a useful
OpenAI-style 429 when any configured window is exhausted.

Scope:

- Run T076 after authentication/model policy and before opening the upstream
  request or acquiring concurrency.
- On rejection return `request_limit_exceeded` with `Retry-After` delta-seconds
  rounded up from the limiter reset time.
- Consume admitted capacity once and do not refund it for upstream status,
  connection failure, client cancellation, or gateway transformation failure.
- Requests with no configured windows take a minimal unrestricted path.
- Do not read or mutate the body to enforce a request-count limit.

Acceptance and tests:

- Real HTTP tests cover RPM, an arbitrary duration, multiple windows, exact reset
  boundary, positive `Retry-After`, different keys, and zero upstream calls on
  rejection using a fake clock.
- Admitted JSON, opaque, SSE, and SSE-to-JSON requests preserve existing behavior.
- Upstream failures and cancellation still consume exactly one request unit.

Reference: `docs/architecture/policy.md#request-limits`,
`docs/architecture/repository.md#request-orchestration`.

Dependencies and out of scope: depends on T071-T072 and T074-T076. Do not add
capacity refunds, queueing, response-header passthrough changes, tokens, or cost.

### T078 - Implement per-key concurrency leases

Goal: reject excess simultaneous work per key and represent each admitted slot
with an idempotent lease suitable for later token and budget reservations.

Scope:

- Track active requests by stable key ID with unlimited behavior when no maximum
  is configured.
- Acquire atomically and reject immediately at the configured maximum; never
  queue hidden waiters.
- Return a lease whose `Release` frees exactly one slot and is safe under repeated
  or concurrent calls.
- Remove idle per-key state after the final lease without racing a new acquire.
- Keep lease API narrow enough to gain reservation fields later, but do not add
  speculative token or budget methods now.

Acceptance and tests:

- Tests cover unlimited, limits one and many, exact saturation, separate keys,
  immediate reuse, double/concurrent release, cleanup, and acquire/release races.
- Active count never exceeds the configured maximum and never underflows.
- Rejected acquisition cannot release another request's slot.

Reference: `docs/architecture/policy.md#concurrency`,
`docs/architecture/policy.md#lease`,
`docs/architecture/testing.md#limit-tests`.

Dependencies and out of scope: depends on T073. Do not add HTTP, queues,
semaphores shared across keys, token/budget reservation, SQLite, or admin kill.

### T079 - Hold concurrency through the full proxy lifecycle

Goal: acquire a per-key concurrency lease before upstream work and release it
exactly once only after the request has fully stopped using gateway/upstream
resources.

Scope:

- Acquire after request-window admission and before creating or sending the
  upstream request; reject saturation with `concurrency_limit_exceeded` HTTP 429.
- Hold the lease through response headers and complete JSON/opaque copy,
  transparent SSE EOF, SSE-to-JSON aggregation, and any required bounded drain.
- Release on normal completion, upstream connection/read error, downstream
  write/flush error, client cancellation, compatibility failure, and internal
  early return.
- Ensure cancellation still propagates upstream before request cleanup finishes.
- Do not make lease lifecycle depend on `[DONE]` or `finish_reason`.

Acceptance and tests:

- Real HTTP tests prove a limit-one key rejects a second request while the first
  is blocked in request upload, before headers, in JSON body, in SSE, and in
  aggregation, then admits immediately after every completion/error/cancel path.
- Different keys remain concurrent and a configured limit greater than one does
  not serialize admitted requests.
- Existing first-fragment flush and EOF-close regression timing remains valid.

Reference: `docs/architecture/policy.md#concurrency`,
`docs/architecture/repository.md#request-orchestration`,
`docs/architecture/testing.md#limit-tests`.

Dependencies and out of scope: depends on T072, T077-T078. Do not add token or
budget reconciliation, response retries, forced stream errors, or persistence of
active leases.

### T080 - Harden the authentication and limits milestone

Goal: verify the complete authentication, model-policy, request-window, and
concurrency lifecycle without regressing transparent transport.

Scope:

- Add a focused real-HTTP scenario using two persistent keys with different model,
  request-window, and concurrency policies against the shared mock upstream.
- Exercise success, invalid/disabled/expired auth, model denial, rate rejection,
  concurrent saturation, upstream error, client cancellation, SSE EOF, and
  process restart with persistent keys.
- Add secret-redaction assertions over gateway responses and captured structured
  logs for gateway, admin, upstream credentials, pepper, and stored digest.
- Run the full suite under the race detector and resolve confirmed races or lease
  leaks within this task.
- Update architecture/testing wording that still labels completed SSE-to-JSON
  coverage as future work.

Acceptance and tests:

- No rejected policy request reaches upstream; every admitted request preserves
  method, path, query, required headers, body bytes, status, and transparent
  response behavior except the explicit compatibility path.
- Concurrent tests cannot oversubscribe windows or leases and leave no key
  permanently blocked after cancellation or failure.
- `go test -race ./...`, `go fmt ./...`, `go test ./...`, and `go build ./...`
  pass, and `CURRENT.md` marks T061-T080 done with the next milestone unset.

Reference: `docs/architecture/testing.md#transport-integration`,
`docs/architecture/testing.md#limit-tests`,
`docs/architecture/testing.md#security-and-performance`.

Dependencies and out of scope: depends on T061-T079. Do not add token accounting,
budget, request-history persistence, body inspector, metrics endpoint, CLI, Web
UI, provider routing, protocol translation, Redis, or PostgreSQL.
