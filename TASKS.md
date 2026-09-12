# Active Tasks

This file contains the next twenty atomic tasks. An implementation agent reads
only `AGENTS.md`, `CURRENT.md`, its assigned task, and the architecture documents
linked by that task. Tasks are ordered by dependency and must not be implemented
out of order. Do not load `PLAN.md`, `docs/tasks/backlog-full.md`, or
`docs/archive/ARCHITECTURE-full.md` during routine implementation.

Every implementation task must finish with `go fmt ./...`, `go test ./...`, and
`go build ./...`. Add behavioral tests in the same task as changed HTTP or
streaming behavior. Update `CURRENT.md` and commit after completing one task.

For observability, accounting, limiter, pricing, budget, and body-inspection
tasks, first inspect the equivalent implementation in the optional local
`.references/bifrost` checkout when it is available. Record the inspected
Bifrost commit and source paths in the task commit message or an adjacent source
comment/notice. Prefer a maintained permissive dependency, then a small isolated
adaptation, then a clean minimal implementation. Do not import Bifrost
architecture or make the ignored checkout a build/runtime dependency. Before
adapting code or data, verify file-level provenance and the dependency license
chain, preserve required notices, and mark adapted files.

## Request Trace Foundation

### T121 - Define the canonical request trace record

Goal: define one immutable, storage-independent completion record that every
later logging and request-history path can consume without reconstructing HTTP
lifecycle semantics.

Scope:

- Replace or extend the narrow `CompletionRecord` with typed fields for request
  ID, stable key ID/name when authenticated, method, bounded route class, model,
  requested mode, actual upstream mode, delivered mode, statuses, terminal
  outcome, safe error code, byte counts, canonical usage, exact cost, and timing.
- Preserve unknown separately from known zero for stream mode, status, token
  counts, money, timestamps, and durations. Use closed enums for route/modes and
  existing `accounting.Usage`, `accounting.Money`, and `TerminalMetadata` values
  rather than parallel numeric representations.
- Define byte and time units explicitly. Persistable durations must be checked,
  non-negative integer microseconds; wall timestamps use canonical UTC Unix
  microseconds while in-process elapsed time may retain monotonic clock behavior.
- Construction and accessors must defensively copy mutable input. The record may
  not retain `*http.Request`, contexts, headers, bodies, principals, policies,
  pricing rules, leases, tickets, loggers, repositories, or arbitrary errors.

Acceptance and tests:

- Table tests cover complete, rejected, pre-upstream, cancelled, JSON, opaque,
  transparent SSE, and converted SSE records plus every known/unknown-zero case.
- Validation rejects invalid enum values, negative/overflowing counts or times,
  finish-before-start, impossible mode combinations, and malformed request IDs.
- Tests prove caller-owned values cannot mutate a built record and formatting or
  validation never exposes credentials, body fragments, pricing rates/rules, or
  SQL details; a known final cost remains an allowed typed accounting value.

Reference: `docs/architecture/observability.md#request-trace`,
`docs/architecture/storage.md#requests-and-bodies`.

Dependencies and out of scope: depends on T120. Do not instrument HTTP, add SQL,
body capture, configuration, metrics, retention, or admin endpoints.

### T122 - Add request-local trace state

Goal: collect T121 fields through one request lifecycle and freeze them exactly
once without introducing shared mutable telemetry state.

Scope:

- Add a request-local trace state at the existing request-ID middleware boundary.
  Provide narrow one-shot setters for authentication, request metadata, upstream
  start/headers, response mode, first downstream byte, usage/cost, error, terminal
  outcome, and completion; later duplicate writes must not corrupt earlier facts.
- Use an injectable clock exposing wall and monotonic time so latency tests use no
  sleeps. Define unset behavior for milestones never reached and clamp/fail safely
  if a test clock moves backwards.
- Keep trace mutation concurrency-safe for cancellation, transport, observation,
  and deferred completion races. Freezing is idempotent and returns independent
  immutable snapshots; no setter may block on logging, parsing, SQL, or a queue.
- Freeze a base completion at handler end. Fields available only from deferred
  response observation are represented by a separate one-shot immutable
  enrichment merged into one final T121 record off the response path; neither the
  base snapshot nor final record may be mutated after construction.
- Preserve the existing request ID response header and terminal metadata contract.
  Do not move the `client.Do` boundary or change lease finalization ordering.

Acceptance and tests:

- Deterministic tests cover every setter order, duplicate/concurrent setters,
  freeze versus cancellation, missing milestones, backwards clocks, and overflow.
- `go test -race` proves one trace cannot affect another and concurrent finalizers
  cannot produce partial records, data races, deadlocks, or post-freeze mutation.
- Trace state retains no request body, header map, raw key, policy, lease, ticket,
  response writer, or unbounded model/error string.

Reference: `docs/architecture/observability.md#request-trace`,
`docs/architecture/repository.md#request-orchestration`.

Dependencies and out of scope: depends on T121. Do not yet populate HTTP fields,
change completion logging, parse usage, persist records, or capture bodies.

### T123 - Record safe gateway error codes

Goal: attach a stable, secret-safe error classification to every gateway-owned
rejection or failure without storing arbitrary error text.

Scope:

- Define a closed error-code type covering current authentication, disabled or
  expired key, malformed request, model, request-window, concurrency, token,
  budget, upstream connection/timeout, response transport, conversion,
  cancellation, unsupported response, and internal failure paths.
- Make `writeGatewayError*`, authentication middleware, policy admission, proxy
  dispatch, and cancellation set the trace code chosen for the client-visible
  response. Preserve transparent upstream 4xx/5xx bodies and classify them without
  replacing them with gateway errors.
- Keep `TerminalOutcome`, HTTP status, and safe error code separate. Upstream
  status alone is not an internal error string, and cancellation must not be
  reported as success merely because headers were already written.
- Never retain or log `error.Error()`, URLs with credentials, parser excerpts,
  Authorization values, SQL errors, request payloads, or response payloads.

Acceptance and tests:

- Real handler tests map every existing rejection and lifecycle failure to one
  stable code, status, terminal outcome, and upstream-start value.
- Upstream error passthrough remains byte/header/status identical; successful and
  ordinary upstream 4xx/5xx records do not invent a gateway error body.
- Secret canary tests cover raw keys, admin/upstream credentials, pepper, digest,
  malformed payload fragments, database errors, and escaped upstream URLs.

Reference: `docs/architecture/observability.md#logging`,
`docs/architecture/transport.md#generic-passthrough`,
`docs/architecture/transport.md#cancellation`.

Dependencies and out of scope: depends on T122. Do not persist free-form errors,
rewrite upstream errors, add retries, metrics, SQL, or new public error responses.

### T124 - Trace downstream status bytes and TTFT

Goal: measure committed response status, successfully delivered bytes, first-byte
latency, and handler completion without changing `ResponseWriter` behavior.

Scope:

- Extend the existing `completionResponseWriter`; do not add a second competing
  outer wrapper. Record explicit or implicit status, bytes reported successfully
  written, first successful non-empty body write, and completion in T122 state.
- Preserve `Unwrap` and `http.ResponseController` behavior, including `FlushError`,
  implicit 200 on successful flush, unsupported-operation errors, short writes,
  and the rule that the first committed status wins.
- Define TTFT as request start to first successfully written non-empty downstream
  body byte. Header-only and empty responses retain unknown TTFT rather than zero.
- Count only bytes accepted by downstream. Failed or short writes must never be
  reported as complete delivery, and instrumentation must not add flushes.

Acceptance and tests:

- Tests cover implicit/explicit status, repeated `WriteHeader`, empty writes,
  short writes, write errors, header-only responses, flush-before-write, hijack or
  unsupported controller behavior, cancellation, and concurrent finalization.
- Existing SSE first-flush and EOF timing regressions pass unchanged; wrapping
  does not remove supported interfaces or alter response bytes and headers.
- Byte/timing bookkeeping performs no parsing, allocation proportional to body
  size, logging, queue operation, SQL, or goroutine creation per write.

Reference: `docs/architecture/observability.md#request-trace`,
`docs/architecture/observability.md#logging`.

Dependencies and out of scope: depends on T122-T123. Do not capture body content,
measure meaningful SSE events, parse usage, or persist telemetry.

### T125 - Trace identity route and request metadata

Goal: populate safe request identity and metadata from facts already available to
authentication and policy inspection without broadening synchronous inspection.

Scope:

- Record stable key ID and bounded key-name snapshot only after authentication.
  Invalid, disabled, expired, health, and admin requests keep nullable key identity
  and must never expose raw key, prefix-as-authenticator, digest, or policy JSON.
- Define bounded route classes for health, admin, known generation, models, and
  generic `/v1/*`; preserve method and escaped path separately where T121 allows.
- Feed model and requested stream mode from existing `RequestMetadata` results.
  Preserve absent, false, true, malformed, oversized, and not-inspected states.
- Do not force unrestricted or generic bodies through `InspectRequestBody` merely
  to improve telemetry. Existing policy-driven inspection and byte-for-byte replay
  remain the only synchronous metadata source in this task.

Acceptance and tests:

- Tests cover authenticated/unauthenticated/admin/health traffic, known and
  generic routes, malformed and oversized JSON, absent/boolean stream, Unicode
  bounded models, and multiple keys without identity crossover.
- Unrestricted generic chunked uploads begin upstream without full pre-read and
  preserve method, escaped path, query, headers, content length, and body bytes.
- Trace output contains no raw key, Authorization, policy JSON, prompt fragment,
  query credential, or unbounded path/model/name value.

Reference: `docs/architecture/observability.md#request-trace`,
`docs/architecture/transport.md#request-body`,
`docs/architecture/repository.md#request-orchestration`.

Dependencies and out of scope: depends on T122-T124. Do not add optional body
logging, new body parsing, endpoint rejection, storage, or admin history APIs.

### T126 - Trace upstream and response modes

Goal: measure the exact upstream boundary and distinguish requested, actual
upstream, and delivered response modes without changing dispatch decisions.

Scope:

- Record immediately before `client.Do`, immediately after headers return, and at
  dispatch selection. Upstream-header latency starts at the `client.Do` boundary,
  not at request arrival or after response classification.
- Record upstream status independently from downstream status. Classify actual
  response with existing `Content-Type` logic as opaque, JSON, or SSE; record
  delivered mode separately when SSE is converted to JSON.
- Preserve unknown mode for connection failures or malformed/ambiguous content
  types exactly as current transport does. Do not sniff bodies for telemetry.
- Keep cancellation-before-cleanup and all token/budget lease semantics unchanged.
  Instrumentation remains local and cannot delay `client.Do`, headers, dispatch,
  first byte, or concurrency release.

Acceptance and tests:

- Fake-clock and real HTTP tests cover connection failure, JSON, opaque, SSE,
  malformed/repeated content type, upstream errors, conversion, cancellation
  before/after headers, and differing upstream/downstream statuses.
- Actual format always comes from response headers, never requested stream mode;
  converted SSE records requested false, upstream SSE, and delivered JSON.
- Proxy method/path/query/headers/body/status/response headers/bytes and existing
  lifecycle accounting tests remain unchanged.

Reference: `docs/architecture/transport.md#response-classification`,
`docs/architecture/observability.md#request-trace`.

Dependencies and out of scope: depends on T124-T125. Do not sniff response bodies,
change conversion support, add retries/timeouts, parse SSE, or persist records.

### T127 - Attach usage and actual cost to traces

Goal: report canonical usage and exact actual cost without making detailed
telemetry part of token or budget enforcement correctness.

Scope:

- Carry canonical usage from converted SSE directly into trace state. Extend the
  bounded transparent JSON/SSE observation result so the same parsed canonical
  usage can reach telemetry after accounting tickets settle, without reparsing.
- Add one-shot completion ownership beside, but independent from, accounting
  tickets: handler completion freezes the T122 base; immediate paths finalize it
  directly, while observed transparent responses transfer it to the existing
  bounded worker, which merges one immutable usage/cost enrichment and emits one
  final record. Submission drop, invalidation, parse failure, or shutdown must
  emit the final record promptly with those fields unknown rather than lose it or
  wait on parsing.
- Calculate reportable actual cost from immutable pricing and differentiated
  input/output usage using existing checked integer-micros calculation. Resolve
  pricing for telemetry even when a key has no budget, using the startup-built
  local resolver only; unknown model/price remains unknown, not zero.
- Keep known token total independent from differentiated cost. Explicit zero-price
  and zero-token results are known zero; total-only, partial, malformed, overflow,
  unsupported coding, truncation, or dropped observation leaves cost unknown.
- Accounting reconciliation must happen with existing guarantees regardless of
  whether trace enrichment succeeds. A telemetry drop may never lose, delay,
  refund, or duplicate token/budget settlement.

Acceptance and tests:

- Tests cover JSON/SSE/conversion parity, priced no-budget requests, exact/glob
  and zero prices, unknown pricing, partial/total-only usage, gzip, malformed and
  over-bound bodies, arithmetic overflow, upstream errors, and queue shutdown.
- Completion ownership tests cover immediate finalize, worker enrichment,
  submission drop, parser failure, invalidation, concurrent duplicate completion,
  and shutdown; every request emits exactly one final record without waiting.
- Known actual accounting and telemetry values agree; each adjustment ticket is
  consumed once and no lease/ticket is retained solely for request history.
- Blocked or failed telemetry enrichment cannot delay response bytes, flush, EOF,
  cancellation, concurrency reuse, or critical persistence.

Reference: `docs/architecture/accounting.md#usage-and-estimation`,
`docs/architecture/accounting.md#pricing`,
`docs/architecture/observability.md#telemetry`.

Dependencies and out of scope: depends on T121-T126 and T111-T113. Do not parse
upstream monetary metadata, store pricing rules, change limits, or add SQL.

### T128 - Measure SSE stream-close delay safely

Goal: measure last meaningful upstream event and downstream stream-close delay
without parsing before delivery or allowing protocol metadata to control EOF.

Scope:

- For transparent SSE, append bounded checkpoints of successfully flushed wire
  offsets and monotonic times, then let off-path observation map the last
  meaningful event end offset to a checkpoint. Bound both bytes and checkpoint
  count; overflow makes semantic timing unknown.
- Carry the resulting timing in T127's same one-shot immutable enrichment before
  its final record is emitted. A dropped/failed observation finalizes promptly
  with stream-close delay unknown; never mutate an already emitted record and
  never wait at downstream EOF for enrichment.
- Reuse the existing SSE parser's definition of meaningful content/terminal
  events and ignore comments/heartbeats for the last-meaningful timestamp. `[DONE]`
  and `finish_reason` remain metadata and never terminate transparent transport.
- For mandatory SSE-to-JSON aggregation, add the smallest event callback/result
  timing needed to capture the last meaningful event during existing parsing;
  do not add a second parse of rendered JSON or upstream SSE.
- Stream-close delay is downstream completion minus last meaningful event and is
  recorded only when both are known and ordered. Malformed, truncated, compressed
  data without safe offset mapping, and clock anomalies remain unknown.

Acceptance and tests:

- Tests cover split/coalesced events, one-byte reads, comments after content,
  usage-only terminal events, `[DONE]`, no `[DONE]`, clean EOF, malformed tails,
  gzip, checkpoint/capture overflow, downstream failure, and cancellation.
- Timing tests prove each transparent fragment is written and flushed before
  checkpoint work and physical upstream EOF closes downstream immediately.
- No per-chunk goroutine, unbounded allocation, idle wait, timer-based normal
  termination, synchronous telemetry parser, or artificial final flush is added.

Reference: `docs/architecture/streaming.md#transparent-sse`,
`docs/architecture/streaming.md#termination`,
`docs/architecture/observability.md#request-trace`.

Dependencies and out of scope: depends on T126-T127. Do not hard-stop streams,
insert SSE errors/heartbeats, infer missing events, or add metrics/storage.

### T129 - Emit canonical structured completion logs

Goal: make current bounded completion logging a secret-safe projection of the
canonical trace rather than a separate narrow lifecycle model.

Scope:

- Keep the existing process-owned bounded `CompletionLogger` and nonblocking
  handoff. Emit safe scalar fields for route/modes, statuses, terminal/error code,
  byte counts, known usage/cost, and known latency values from the frozen record.
- Consume only T127's final records. Immediate paths log after base finalization;
  deferred paths log when the observation worker emits its enriched-or-unknown
  final record. Logging never waits in the HTTP handler and never emits a second
  correction event for one request.
- Omit unknown optional fields rather than encoding misleading zero. Log known
  zero explicitly where operationally meaningful. Use integer micros for money
  and duration; do not emit floating-point costs or duration strings.
- Bound model/key-name/path values before they reach `slog`. Never log bodies,
  headers, raw keys, prefixes as credentials, digest, pepper, policy JSON, pricing
  rates/rules, reservations, SQL errors, or per-token/per-event data.
- Preserve queue capacity, drop counter, concurrent shutdown safety, and bounded
  shutdown. Logging remains best effort and cannot influence response status.

Acceptance and tests:

- Attribute tests cover complete and every rejection/failure class, known zero
  versus unknown, JSON/SSE/conversion, cancellation, and malformed bounded text.
- Saturated and blocked sinks cannot delay JSON completion, first SSE flush, EOF,
  cancellation, accounting finalization, or concurrency reuse under race tests.
- Canary scans prove all forbidden credentials, payloads, prices, reservations,
  headers, and database details are absent from captured structured logs.

Reference: `docs/architecture/observability.md#logging`,
`docs/architecture/observability.md#telemetry`.

Dependencies and out of scope: depends on T121-T128. Do not write SQLite, log body
content, add metrics exporters, request-ID labels, or change public responses.

## Bounded Body Inspection

### T130 - Validate observability configuration

Goal: define strict deployment bounds for detailed telemetry and sensitive body
retention before any capture or persistence is enabled.

Scope:

- Add an `observability` YAML object with bounded telemetry queue capacity,
  `max_captured_body_bytes`, request-metadata retention, and independently shorter
  body retention. Choose conservative documented defaults; zero body bytes
  disables capture globally even if a key opts in.
- Define retention execution constants rather than more deployment knobs: one
  pass at worker startup and after each 1024 processed jobs, deleting at most 1000
  body rows and then 1000 metadata rows per pass. Tests may inject these constants
  or an equivalent internal policy, but production defaults remain fixed.
- Parse retention as the repository's existing strict duration representation or
  introduce one narrow checked representation. Reject negatives, overflow, nulls,
  unknown fields, ambiguous units, body retention longer than metadata retention,
  and values above explicit memory/storage safety caps.
- Keep this limit independent from request policy inspection bounds and the usage
  observation bound. Configuration is validated once before listener startup.
- Configuration contains no per-key policy and no body content. Error messages
  include field names but never dump credentials or complete configuration.

Acceptance and tests:

- Strict config tests cover omitted object/defaults, explicit disablement, minimum
  and maximum values, unknown/null/wrong-type fields, malformed durations,
  overflow, invalid retention ordering, and environment-secret coexistence.
- Existing minimal configurations remain valid and body capture stays disabled by
  default; no invented retention task runs when detailed telemetry is disabled.
- Tests prove changing body capture limits cannot alter tokenizer, request
  inspection, accounting observation, pricing, or transport timeout limits.

Reference: `docs/architecture/operations.md#configuration`,
`docs/architecture/observability.md#body-capture`,
`docs/architecture/storage.md#boundaries`.

Dependencies and out of scope: depends on T129. Do not add hot reload, key policy,
capture wrappers, SQL, Prometheus, `/ready`, or arbitrary redaction.

### T131 - Add per-key body capture policy

Goal: make sensitive request and response body capture an explicit per-key opt-in
within the existing full-replacement policy transaction.

Scope:

- Add strict boolean `log_request_body` and `log_response_body` policy fields.
  Absence means false; reject null, strings/numbers, duplicates, and unknown
  nested policy shapes under existing strict JSON decoding.
- Compile copied values into immutable `auth.EffectivePolicy`. Request capture
  enables distinct client and upstream request bodies; response capture enables
  only bytes delivered downstream. T130's global maximum always caps both.
- Extend admin create/replacement responses, SQLite `policy_json`, reopen loading,
  and atomic auth snapshot publication. Invalid replacement changes neither
  durable policy nor runtime snapshots/limiter generations.
- Logging policy changes affect only newly authenticated requests. An active
  request keeps its captured immutable policy and is not retroactively enabled.

Acceptance and tests:

- Policy and real admin HTTP tests cover absent/false/true combinations, null and
  wrong types, unknown fields, idempotent replacement, concurrent replacement,
  immediate visibility, active-request snapshot isolation, and reopen.
- Ordinary gateway keys cannot change policy; raw keys, bodies, pepper, digest,
  upstream/admin credentials, and full policy JSON never enter errors or logs.
- Existing model/request/token/concurrency/budget replacement and stale-principal
  tests remain behaviorally unchanged.

Reference: `docs/architecture/policy.md#effective-policy`,
`docs/architecture/observability.md#body-capture`,
`docs/architecture/storage.md#api-keys`.

Dependencies and out of scope: depends on T130. Do not add per-key byte limits,
header capture, content redaction claims, retention overrides, or history routes.

### T132 - Implement bounded binary body recording

Goal: provide one protocol-independent recorder that retains a fixed prefix while
counting the complete observed byte length.

Scope:

- Record at most the configured number of bytes, checked original byte count,
  truncation state, and a strict body-kind identity. Preserve arbitrary binary,
  NUL, invalid UTF-8, compressed, and empty content without text conversion.
- A zero bound performs no allocation and retains no content. Truncation is true
  whenever observed size exceeds retained size; known empty capture remains
  distinguishable from no capture.
- Snapshot returns independent immutable bytes and cannot expose recorder capacity
  for mutation. After snapshot/finalization, later writes fail safely or are
  ignored according to one documented deterministic contract.
- Keep the recorder independent of HTTP, OpenAI/SSE parsing, gzip decoding,
  policy, trace state, accounting, SQLite, logging, and goroutines.

Acceptance and tests:

- Table tests cover disabled, empty, under/exact/over bound, fragmented writes,
  large write, binary/NUL/invalid UTF-8, short accepted counts, overflow, repeated
  snapshot/finalize, and caller mutation attempts.
- Property/fuzz tests prove retained bytes are exactly the observed prefix,
  original size never wraps, and memory remains O(configured bound).
- Recorder errors and debug formatting contain no captured payload bytes.

Reference: `docs/architecture/observability.md#body-capture`,
`docs/architecture/storage.md#requests-and-bodies`.

Dependencies and out of scope: depends on T130-T131. Do not wrap HTTP bodies,
parse/redact payloads, compress content, persist snapshots, or add retention.

### T133 - Capture client and upstream request bodies

Goal: separately observe bytes consumed from the client and bytes read by the
upstream transport without pre-buffering generic uploads.

Scope:

- When the captured effective policy enables request logging and the global bound
  is nonzero, wrap the incoming body at authentication success to record bytes
  actually consumed as `client_request`; never drain rejected/abandoned bodies.
- Wrap the final replayed/original body passed to `client.Do` separately as
  `upstream_request`. Compose with `InspectRequestBody` and `replayedRequestBody`
  so policy inspection occurs once and replay remains byte-for-byte.
- Preserve `ContentLength`, `GetBody` behavior where currently supported, chunked
  streaming, close/error propagation, upload cancellation, nil/`NoBody`, and the
  exact upstream-start accounting boundary.
- Finalize immutable snapshots on every success, rejection, read/close error,
  cancellation, and internal exit. Client/upstream sizes may legitimately differ
  after partial reads and must not be synthesized from headers.

Acceptance and tests:

- Real HTTP tests cover inspected and unrestricted known routes, generic chunked
  uploads, empty/nil bodies, exact/over bound, partial upstream upload failure,
  client read error, cancellation, auth/policy rejection, and concurrent keys.
- Admitted upstream receives identical method/path/query/headers/content length
  and body; generic traffic starts upstream before client EOF and is not buffered.
- Disabled capture adds no body-sized allocation; no body bytes appear in trace,
  logs, errors, accounting jobs, or credentials.

Reference: `docs/architecture/transport.md#request-body`,
`docs/architecture/observability.md#body-capture`,
`docs/architecture/repository.md#request-orchestration`.

Dependencies and out of scope: depends on T125 and T131-T132. Do not capture
headers, modify bodies, add content filtering, SQL, retries, or request history.

### T134 - Capture JSON and opaque responses

Goal: retain a bounded copy of only bytes successfully accepted downstream for
transparent JSON and opaque responses.

Scope:

- Integrate T132 at the downstream writer boundary only when response logging is
  enabled. Capture exactly the returned successful write count, including partial
  writes; never capture unread upstream bytes or infer size from `Content-Length`.
- Keep response body capture separate from accounting response observation: each
  has its own enablement and bound, and dropping detailed telemetry cannot affect
  token/budget reconciliation.
- Preserve current `io.Copy` behavior, status, safe headers, content encoding,
  byte identity, implicit status, cancellation, and immediate EOF completion.
- Finalize known empty bodies and partial/error snapshots correctly. Do not decode
  gzip, parse JSON, redact arbitrary content, or place body bytes in T121 fields.

Acceptance and tests:

- Real HTTP tests cover identity/gzip/binary/invalid UTF-8, empty, exact/over
  bound, upstream 4xx/5xx, short downstream writes, upstream read failure,
  cancellation, enabled/disabled keys, and response-policy replacement races.
- Captured bytes equal the delivered prefix and original size equals successfully
  delivered bytes; response output remains byte/header/status identical.
- A blocked/failed recorder path cannot delay completion or alter critical usage
  observation, lease settlement, and concurrency reuse.

Reference: `docs/architecture/observability.md#body-capture`,
`docs/architecture/transport.md#generic-passthrough`.

Dependencies and out of scope: depends on T124 and T131-T133. Do not handle SSE
or conversion yet, persist bodies, inspect MIME content, or capture headers.

### T135 - Capture SSE and converted responses

Goal: capture the downstream representation of streaming and converted responses
without changing flush order or conversion semantics.

Scope:

- For transparent SSE, record only after each downstream write and successful
  flush, matching accounting observation semantics. Capture bookkeeping for a
  fragment completes only after flush and must not wait for event framing.
- For `stream:false` plus upstream SSE, capture generated JSON bytes actually
  accepted downstream, not discarded upstream SSE. Reuse the same writer-level
  mechanism as T134 rather than reparsing rendered JSON.
- Preserve exact `[DONE]`, EOF-without-DONE, fragmented/coalesced events, gzip
  bounds/trailer validation, tool-call aggregation, header sanitation,
  cancellation, and known usage retained before later drain/write failure.
- Bound capture independently from T128 checkpoints, accounting observation, and
  aggregation limits. Overflow only truncates detailed capture.

Acceptance and tests:

- Timing tests cover first flush, every fragment, EOF, blocked recorder consumer,
  exact/over bound, `[DONE]` followed by delayed EOF, no `[DONE]`, malformed SSE,
  gzip, downstream failure, cancellation, and parallel unrestricted streams.
- Transparent captured bytes exactly equal successfully written/flushed wire
  bytes; converted captured bytes exactly equal accepted generated JSON bytes.
- No parser, queue, SQL, per-chunk goroutine, heartbeat, idle wait, or extra flush
  enters transparent transport.

Reference: `docs/architecture/streaming.md#transparent-sse`,
`docs/architecture/streaming.md#sse-to-json`,
`docs/architecture/observability.md#body-capture`.

Dependencies and out of scope: depends on T128 and T131-T134. Do not persist yet,
store upstream conversion input, alter aggregation, or terminate on metadata.

## Persistent Request History

### T136 - Add persistent request history schema

Goal: store canonical request metadata with strict integrity while preserving
unknown values and keeping sensitive body bytes outside the main table.

Scope:

- Add the next embedded migration for `requests`, keyed by collision-safe request
  ID, with nullable stable API key foreign key and bounded historical key name,
  route/method/path/model/modes, statuses, terminal/error codes, byte/token/cost
  values, timestamps, and latency fields required by T121.
- Use `NULL` for unknown and integer zero for known zero. Add checks for enums,
  non-negative counts/micros/durations, valid status ranges, timestamp ordering,
  mode combinations, and bounded text lengths.
- Define intentional key deletion behavior without storing raw keys or digests.
  Add indexes for recent history and per-key/time lookup; do not add speculative
  indexes for future UI filters without a demonstrated query.
- Preserve transactional/idempotent migration, WAL/foreign keys, upgrade from
  schema 6, fresh creation, failed-migration rollback, and future-version reject.

Acceptance and tests:

- Fresh/upgrade/reopen schema tests inspect columns, nullability, checks, foreign
  keys, indexes, version, and rollback; constraints reject every invalid enum,
  negative/overflowing value, impossible time, duplicate ID, and orphan key.
- Round-trip fixtures preserve known zero versus unknown for usage, cost, status,
  and latency and support unauthenticated requests with no key ID.
- Schema contains no body BLOB, raw key, Authorization/header data, digest,
  pepper, policy JSON, pricing rule, reservation, or arbitrary error text.

Reference: `docs/architecture/storage.md#requests-and-bodies`,
`docs/architecture/storage.md#boundaries`,
`docs/architecture/observability.md#request-trace`.

Dependencies and out of scope: depends on T121-T129. Do not add repository code,
body tables, retention, admin queries, full-text search, metrics, or request replay.

### T137 - Add sensitive request body schema

Goal: store optional bounded bodies separately so they can have shorter retention
and cannot accidentally enter ordinary metadata queries.

Scope:

- Add `request_bodies` in the same next migration or the immediately following
  embedded migration, keyed by `(request_id, body_kind)` with strict kinds
  `client_request`, `upstream_request`, and `response`.
- Store body as SQLite BLOB, checked original byte size, and strict truncation
  flag. Enforce captured length at or below configured schema safety maximum,
  captured length at or below original size, and consistent truncation semantics.
- Foreign-key bodies to requests with cascading metadata deletion. Known empty
  capture is a zero-length row; capture not requested is absence of a row.
- Keep media parsing, headers, encodings, body hashes, deduplication, compression,
  encryption/key management, and searchable text out of this schema.

Acceptance and tests:

- Fresh/upgrade/reopen tests cover all body kinds, binary/NUL/invalid UTF-8,
  empty/exact/truncated bodies, duplicate kinds, orphans, cascade, rollback, and
  every malformed size/truncation combination.
- Queries of the main `requests` table never load body content, and body deletion
  can occur independently while retaining request metadata.
- Schema and constraint errors never include body bytes or credentials.

Reference: `docs/architecture/storage.md#requests-and-bodies`,
`docs/architecture/observability.md#body-capture`.

Dependencies and out of scope: depends on T132 and T136. Do not add persistence
workers, encryption, redaction, admin body access, export, or retention scheduling.

### T138 - Persist telemetry transactionally

Goal: insert one validated completion record and its optional body snapshots
atomically through a narrow storage repository.

Scope:

- Add a repository API that maps T121 records and up to one snapshot per T137
  body kind into SQLite in one transaction. Validate before SQL and use explicit
  column lists; no HTTP, policy, parser, logger, or limiter types enter storage.
- Duplicate request ID, invalid key identity, malformed record/body, cancellation,
  and any statement failure roll back every row. Return typed bounded errors that
  omit SQL text, body bytes, model/path payload fragments, and credentials.
- Add bounded deletion operations for body and metadata cutoffs separately, each
  accepting a positive maximum-row count. Body retention runs first; metadata
  deletion cascades remaining bodies. Cutoffs use completion timestamps and
  deterministic inclusive/exclusive semantics.
- Keep write and retention calls synchronous at repository level for testability;
  T139 owns all asynchronous scheduling and retry/drop policy.

Acceptance and tests:

- Integration tests cover full/minimal/unauthenticated records, every unknown and
  known-zero field, all body kinds, binary data, transaction rollback, duplicate
  IDs, key deletion behavior, context cancellation, reopen, and corruption.
- Retention tests use a fake clock/cutoffs and cover exact boundary, independent
  body removal, metadata cascade, empty batches, idempotence, and multiple keys.
- Repository never logs, exposes mutable SQL rows, returns body content in errors,
  or updates token/budget aggregates and runtime limiter state.

Reference: `docs/architecture/storage.md#requests-and-bodies`,
`docs/architecture/storage.md#boundaries`.

Dependencies and out of scope: depends on T136-T137. Do not add HTTP list/get
routes, pagination, telemetry queues, background goroutines, or request replay.

### T139 - Add bounded history persistence worker

Goal: persist detailed request history best-effort through one bounded worker so
SQLite latency and failures never stall transport or accounting.

Scope:

- Add a process-owned worker accepting one immutable job containing T127's final
  T121 record and optional immutable T132 snapshots. Submission is nonblocking and
  reports accepted/dropped without retaining request/context/header/policy/lease.
- Bound queue capacity from T130. Track accepted, processed, persisted, failed,
  and dropped counts with atomics; queue saturation drops only detailed telemetry
  and never invokes synchronous SQL fallback.
- Run T138 writes and deterministic bounded retention off transport: one pass at
  worker startup and after every 1024 processed jobs, at most 1000 body rows then
  1000 metadata rows per pass, using the injectable worker clock. A failed pass is
  counted and retried only at the next scheduled trigger; shutdown starts no new
  pass. Write retry cannot reorder duplicate IDs indefinitely or grow memory;
  safe failure may drop history but must be counted.
- Shutdown stops admission, drains within the caller's deadline, then drops the
  remainder deterministically. It must not close SQLite itself or outlive storage;
  concurrent submit/shutdown must be race-free and panic-free.

Acceptance and tests:

- Tests cover FIFO insert, bodies, saturation, blocked/failing/recovering SQLite,
  duplicate records, retention trigger, concurrent producers, shutdown drain,
  deadline expiry, post-shutdown submit, and exact counters under `go test -race`.
- Blocked persistence cannot delay headers, JSON body, first/per-fragment SSE
  flush, EOF, cancellation, concurrency release, usage adjustment, or critical
  token/budget accumulator writes.
- Dropped jobs release all captured memory and reveal no body/key/credential in
  logs or returned errors.

Reference: `docs/architecture/observability.md#telemetry`,
`docs/architecture/storage.md#boundaries`,
`docs/architecture/operations.md#server-lifecycle`.

Dependencies and out of scope: depends on T130 and T138. Do not make history
durable for enforcement, add an unbounded retry spool, metrics endpoint, or SQL
on request/response goroutines.

### T140 - Complete the observability milestone

Goal: wire request tracing, structured logs, optional bounded body capture,
asynchronous history persistence, retention, and lifecycle shutdown end to end
without regressing the policy proxy.

Scope:

- Construct trace/worker dependencies at startup, attach trace state at the outer
  request boundary, freeze one base after lifecycle cleanup, and use T127 one-shot
  ownership to emit exactly one immediate or asynchronously enriched final record
  to logs/history without blocking HTTP. Ensure health/admin and pre-auth failures
  receive safe records without body capture or fabricated key identity.
- Order graceful shutdown: stop new HTTP work, finish/cancel handlers, finish
  accounting observation and critical accumulators, stop telemetry submissions,
  bounded-drain completion logs/history, then close SQLite. No worker may submit
  after its sink closes.
- Add one real-HTTP lifecycle scenario across restart with multiple keys and body
  policies. Cover JSON, opaque, transparent SSE, conversion, gzip, upstream 4xx/5xx,
  malformed/oversized traffic, all policy rejections, failures, and cancellation.
- Audit memory/secret boundaries and immediate architecture wording. Detailed
  telemetry remains droppable; token and budget enforcement/persistence remains
  correct when logging/history/capture is disabled, saturated, blocked, or failed.

Acceptance and tests:

- End-to-end records contain correct identity, modes, statuses, terminal/error
  code, delivered bytes, usage/cost-known state, TTFT/header/close timing, and only
  policy-enabled bounded bodies with independent retention after restart.
- Transparent traffic preserves method, escaped path, query, headers, body,
  status, response headers/bytes, first flush, every fragment, physical-EOF close,
  cancellation, and unrestricted concurrency. Every rejection makes zero upstream
  calls and consumes no body solely for telemetry.
- Saturated queues and blocked/failed SQLite/log sinks cannot delay transport or
  leak resources. Canary scans find no raw/admin/upstream keys, pepper, digest,
  Authorization, policy JSON, pricing/reservations, SQL detail, or payload content
  when capture is disabled.
- `go test -race ./...`, `go fmt ./...`, `go test ./...`, and `go build ./...`
  pass. `CURRENT.md` marks T121-T140 done and leaves the next milestone unset.

Reference: `docs/architecture/testing.md#transport-integration`,
`docs/architecture/testing.md#security-and-performance`,
`docs/architecture/observability.md#telemetry`,
`docs/architecture/operations.md#server-lifecycle`.

Dependencies and out of scope: depends on T121-T139. Do not add `/metrics`,
`/ready`, CLI, request-history admin APIs, Web UI, tool-call validation/execution,
provider routing/translation, retries, Redis, PostgreSQL, or distributed telemetry.
