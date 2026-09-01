# Active Tasks

This file contains only the active queue. Read `AGENTS.md`, `CURRENT.md`, and the
single task being implemented. Do not load the whole cold backlog. When fewer
than five tasks remain, promote the next coherent batch from
`docs/tasks/backlog-full.md` and link only relevant architecture documents.

Every implementation task must finish with `go fmt ./...`, `go test ./...`, and
`go build ./...`. Add behavioral tests in the same task as changed HTTP or
streaming behavior. Update `CURRENT.md` after completing and committing each
task.

## Transport Foundation Remediation

### T021 - Preserve transparent response encoding

Goal: prevent the upstream HTTP transport from silently changing response bytes
through automatic compression negotiation or decompression.

Scope:

- Configure the shared upstream `http.Transport` so it does not add gzip
  negotiation and transparently decompress a response on the gateway's behalf.
- Continue forwarding a client-provided `Accept-Encoding` header normally; the
  gateway must return the representation and matching headers received from
  upstream without decoding it.
- Keep one long-lived client and the existing phase-specific timeouts.

Acceptance:

- A gzip-encoded upstream body reaches the client byte-for-byte with its
  `Content-Encoding` intact.
- The gateway does not invent `Accept-Encoding: gzip` when the client omitted
  it.
- Ordinary uncompressed JSON passthrough remains unchanged.

Tests:

- Add a transport configuration unit test for disabled automatic compression.
- Add a real upstream/gateway integration test covering compressed binary bytes,
  `Content-Encoding`, and client-provided versus absent `Accept-Encoding`.

Documentation:

- Add a short representation-transparency rule to
  `docs/architecture/transport.md#upstream-client`, including why Go automatic
  decompression is disabled in a byte-transparent proxy.

Reference: `docs/architecture/transport.md#generic-passthrough`,
`docs/architecture/transport.md#upstream-client`.

Dependencies and out of scope: builds on `T010` and `T017`. Do not add general
content transformations, response parsing, or compression performed by the
gateway.

### T022 - Preserve request length semantics

Goal: preserve a known incoming request body length when constructing the
upstream request.

Scope:

- Copy `request.ContentLength` to the outgoing request when the body is forwarded
  unchanged.
- Preserve the distinct meanings of a known zero length and an unknown length;
  do not calculate length by buffering a generic body.
- Keep generic request bodies streaming and byte-identical.

Acceptance:

- An incoming request with a known body length reaches upstream with the same
  `ContentLength` and without an unexpected chunked transfer encoding.
- A request whose body length is genuinely unknown remains streamable and is not
  buffered to discover its size.
- Requests without a body retain valid Go HTTP semantics.

Tests:

- Add real HTTP integration cases for known non-zero length, empty body, and an
  unknown-length streaming body.
- Assert both received bytes and upstream `ContentLength`/`TransferEncoding`.

Documentation:

- Clarify `docs/architecture/transport.md#request-body` and `#headers`: preserve
  length only while bytes are unchanged; a future body transformation must
  recompute or remove it.

Reference: `docs/architecture/transport.md#request-body`,
`docs/architecture/transport.md#headers`.

Dependencies and out of scope: builds on `T012` and `T014`. Request trailers,
inspection buffers, body-size policy, and body logging are not part of this task.

### T023 - Join the configured upstream base path safely

Goal: retain a path prefix in `upstream_base_url` while ensuring the client can
never change the configured upstream authority.

Scope:

- Join the configured base path with the incoming `/v1/*` path instead of
  replacing it.
- Preserve query strings and escaped path semantics without cleaning away or
  decoding meaningful bytes.
- Ensure unusual paths such as a leading `//`, encoded slashes, and dot-like
  segments cannot be interpreted as a new host or scheme.
- Keep scheme and authority exclusively from validated configuration.

Acceptance:

- Base `https://router.example/gateway` plus `/v1/models` targets
  `/gateway/v1/models`.
- Empty and trailing-slash base paths produce one correct separator.
- Query values and escaped path data are retained.
- Hostile path forms cannot select a different host.

Tests:

- Add table-driven URL construction tests for no prefix, prefix, trailing slash,
  escaped slash, duplicate slash, and dot-like input.
- Add a real integration test proving both the observed path and fixed upstream
  host.

Documentation:

- Specify exact base-path joining and escaped-path behavior in
  `docs/architecture/transport.md#generic-passthrough`.
- Record the security cases in
  `docs/architecture/testing.md#security-and-performance` if the existing line
  is not precise enough to reproduce them.

Reference: `docs/architecture/transport.md#generic-passthrough`,
`docs/architecture/testing.md#security-and-performance`.

Dependencies and out of scope: corrects `T011`. Do not add client-selectable
upstreams, redirects, provider routing, or generic URL rewriting.

### T024 - Prevent upstream credential disclosure

Goal: guarantee that the configured 9router credential cannot be reflected to a
downstream client through response headers.

Scope:

- Apply direction-specific response-header filtering rather than reusing an
  unrestricted request-header copier.
- Drop `Authorization` and any narrowly defined gateway-owned credential header
  from upstream responses while preserving ordinary end-to-end headers.
- Keep response bodies opaque; do not scan or rewrite arbitrary body content.
- Do not log the configured upstream key in the new failure or filter paths.

Acceptance:

- An upstream `Authorization: Bearer upstream-secret` response header never
  reaches the client.
- Hop-by-hop filtering still works and unrelated custom headers still pass.
- Existing request-side Authorization replacement remains unchanged.

Tests:

- Add an integration test with upstream reflection of `Authorization` and a
  custom safe header.
- Assert the secret is absent from response headers and completion logs.

Documentation:

- Expand `docs/architecture/transport.md#headers` with the explicit response-side
  deny rule and its body-inspection boundary.
- Keep the secret-redaction test obligation in
  `docs/architecture/testing.md#security-and-performance` current.

Reference: `docs/architecture/transport.md#headers`,
`docs/architecture/observability.md#logging`,
`docs/architecture/testing.md#security-and-performance`.

Dependencies and out of scope: corrects `T013` and `T016`. Configurable secret
scanners, response-body redaction, API-key authentication, and request history
are future work.

### T025 - Preserve optional ResponseWriter capabilities

Goal: make completion logging transparent to streaming and other optional
`http.ResponseWriter` capabilities.

Scope:

- Update the completion writer so wrapped handlers can use modern
  `http.ResponseController` operations through `Unwrap`, including flush.
- Preserve status capture and exactly one completion record.
- Do not eagerly flush ordinary responses or introduce streaming detection in
  middleware.

Acceptance:

- `http.NewResponseController(writer).Flush()` reaches a flush-capable underlying
  writer through completion logging.
- Status and logging behavior from `T009` remain correct.
- Unsupported optional operations return their normal Go error rather than
  panic.

Tests:

- Add focused unit tests with a flush-recording writer and a writer without
  optional capabilities.
- Retain the completion-log assertions for one log line and correct status.

Documentation:

- Add a middleware transparency rule to
  `docs/architecture/observability.md#logging`: observability wrappers must not
  hide flush, cancellation, or connection behavior.

Reference: `docs/architecture/observability.md#logging`,
`docs/architecture/streaming.md#transparent-sse`.

Dependencies and out of scope: corrects the interaction between `T009` and the
upcoming SSE transport. Hijacking or HTTP/1 upgrade proxying is not required
unless a current handler uses it.

## Transparent Streaming Transport

### T026 - Classify the actual upstream response

Goal: create one response classifier based on the actual upstream
`Content-Type`, never on assumptions from the request payload.

Scope:

- Define a small `ResponseMode` with JSON, SSE, and opaque modes.
- Classify `text/event-stream` as SSE, `application/json` and media types ending
  in `+json` as JSON, and everything else as opaque.
- Parse media-type parameters such as `charset=utf-8` correctly.
- Treat missing or malformed `Content-Type` as opaque for now.

Acceptance:

- Classification occurs only after upstream headers arrive.
- Request field `stream` is not read or required.
- Mixed-case media types, parameters, missing headers, and malformed values have
  deterministic results.

Tests:

- Add table-driven unit tests for SSE, JSON, `+json`, parameters, case variants,
  binary, empty, and malformed values.
- Include a test proving a request-side stream assumption cannot affect the
  result.

Documentation:

- Update `docs/architecture/transport.md#response-classification` to make the
  opaque fallback for missing/malformed media types explicit.

Reference: `docs/architecture/transport.md#response-classification`,
`docs/architecture/repository.md#principles`.

Dependencies and out of scope: follows `T021`-`T025`. Byte sniffing, SSE parsing,
OpenAI request inspection, and SSE-to-JSON aggregation are excluded.

### T027 - Dispatch SSE to a dedicated passthrough path

Goal: make the response classifier the single dispatch point between ordinary
copying and transparent SSE transport.

Scope:

- After copying the upstream status and safe headers, route SSE responses to a
  dedicated streaming function.
- Keep JSON and opaque responses on an ordinary byte-copy path.
- Preserve all status codes and safe end-to-end headers in every mode.
- Keep the streaming function protocol-neutral and unaware of OpenAI chunks.

Acceptance:

- Actual SSE enters the dedicated path regardless of the incoming request body.
- JSON and opaque responses remain byte-identical.
- There is no duplicated `Content-Type` classification in handlers or packages.

Tests:

- Add real gateway/upstream integration cases for JSON, binary opaque data, and
  SSE, including non-200 status and a custom response header.
- Use test instrumentation that proves which copy path executed without exposing
  a production testing API.

Documentation:

- Add a concise dispatch description to
  `docs/architecture/transport.md#response-classification` and keep protocol
  responsibilities in `docs/architecture/repository.md#principles` accurate.

Reference: `docs/architecture/transport.md#generic-passthrough`,
`docs/architecture/transport.md#response-classification`,
`docs/architecture/testing.md#transport-integration`.

Dependencies and out of scope: depends on `T026`. Immediate flush is implemented
in `T028`; no parsing, observation, aggregation, or telemetry is added here.

### T028 - Flush each successful SSE write

Goal: deliver upstream SSE fragments incrementally instead of buffering them
until the handler or server buffer completes.

Scope:

- Implement the hot loop `read upstream -> write downstream -> flush -> repeat`.
- Flush through `http.ResponseController` after each successful downstream write.
- Do not interpret read boundaries as SSE event boundaries and do not
  reconstruct chunks.
- Return promptly on read, write, or flush failure and allow deferred upstream
  body closure/context cancellation to release resources.

Acceptance:

- A client receives a small first fragment while upstream remains blocked before
  its second fragment.
- Upstream bytes remain in order and byte-identical.
- No parser, logger, or other operation runs between write and flush.

Tests:

- Add a real HTTP behavioral test with an upstream that writes, flushes, waits on
  a channel barrier, and later writes again.
- Prove receipt of the first fragment before releasing the barrier; do not rely
  only on `httptest.ResponseRecorder` or timing sleeps.

Documentation:

- Keep `docs/architecture/streaming.md#transparent-sse` synchronized with the
  implemented write/error/flush ordering.

Reference: `docs/architecture/streaming.md#transparent-sse`,
`docs/architecture/testing.md#transport-integration`.

Dependencies and out of scope: depends on `T025`-`T027`. Heartbeats, idle
timeouts, event observers, and aggregation are excluded.

### T029 - Close immediately on upstream EOF

Goal: make physical upstream EOF the normal completion signal for transparent
SSE.

Scope:

- End the streaming handler immediately when the upstream body returns `io.EOF`.
- Treat normal EOF as success, including when no `[DONE]` was sent.
- Do not wait for `finish_reason`, terminal JSON, a grace period, or idle timeout.
- Do not generate a synthetic `[DONE]` event.

Acceptance:

- SSE without `[DONE]` reaches downstream completely and then closes.
- Downstream EOF follows upstream EOF without intentional delay.
- Normal EOF does not produce an upstream failure response or error log.

Tests:

- Add an integration test whose upstream sends a complete event without
  `[DONE]`, flushes, and returns.
- Assert all bytes and downstream EOF using bounded channel synchronization.

Documentation:

- Ensure `docs/architecture/streaming.md#termination` explicitly states that EOF
  is sufficient and synthetic terminal payloads are forbidden.

Reference: `docs/architecture/streaming.md#termination`,
`docs/architecture/testing.md#regressions`.

Dependencies and out of scope: depends on `T028`. A policy for an upstream that
remains physically open after terminal content is a separate, disabled-by-default
future decision.

### T030 - Add the stream-close delay regression

Goal: protect the project from the original multi-second Bifrost end-of-stream
delay.

Scope:

- Add a named regression test measuring downstream completion after the upstream
  physically closes its response body.
- Use a terminal-looking OpenAI chunk only as payload; transport must not parse
  it or depend on `finish_reason`.
- Use a generous CI threshold that catches seconds of artificial delay without
  pretending to be a microbenchmark.

Acceptance:

- Downstream completes within 250 ms of observed upstream EOF under local test
  conditions.
- The test would fail for an implementation that waits several seconds after
  EOF.
- The production code adds no timer solely to satisfy the test.

Tests:

- Implement the regression with a real upstream and gateway server, synchronized
  close timestamp, and full client body read.
- Keep synchronization deterministic; timing is used only for the final upper
  bound.

Documentation:

- Name this regression explicitly in
  `docs/architecture/testing.md#regressions` and distinguish the CI guardrail
  from a production latency SLA.

Reference: `docs/architecture/streaming.md#termination`,
`docs/architecture/testing.md#regressions`.

Dependencies and out of scope: depends on `T029`. Real 9router and OpenCode
latency comparison remains release verification work.

### T031 - Preserve DONE, comments, and raw SSE framing

Goal: prove that transparent SSE treats protocol-looking content as opaque bytes.

Scope:

- Pass `data: [DONE]` through unchanged without using it to close transport.
- Pass comments such as `: heartbeat` unchanged and never generate gateway
  heartbeats.
- Preserve event order, delimiters, CRLF/LF bytes, and any bytes following
  `[DONE]` until actual upstream EOF.

Acceptance:

- `[DONE]`, comments, data events, and post-`[DONE]` bytes are byte-identical
  downstream.
- Upstream EOF, not any payload value, ends the handler.
- At least the first flushed comment or event is delivered incrementally.

Tests:

- Add real integration cases for comments around data events and `[DONE]`
  followed by additional bytes before EOF.
- Compare the complete raw body and use a barrier to verify an early flush.

Documentation:

- Clarify in `docs/architecture/streaming.md#termination` that `[DONE]` is payload
  for transparent transport and that gateway-generated heartbeat is absent.

Reference: `docs/architecture/streaming.md#transparent-sse`,
`docs/architecture/streaming.md#termination`.

Dependencies and out of scope: depends on `T028`-`T030`. Semantic comment or
`[DONE]` handling belongs only to a future passive parser/observer.

### T032 - Preserve split and coalesced SSE bytes

Goal: prove the transparent transport is independent of upstream write, HTTP,
and TCP read boundaries.

Scope:

- Support an SSE line divided across several upstream writes and several events
  emitted in one write without inserting, deleting, or duplicating bytes.
- Keep the copy loop generic; do not add line buffering or event framing to the
  passthrough path.
- Do not expose or document the internal copy-buffer size as API behavior.

Acceptance:

- Split `da` plus `ta: ...` fragments become the same downstream byte sequence
  sent upstream.
- Several coalesced events remain in order and byte-identical.
- No assertion assumes one upstream `Write` maps to one downstream `Read`.

Tests:

- Add separate real integration cases for deliberately split writes and one
  coalesced write containing multiple events.
- Compare the final raw bytes and use barriers only where incremental delivery is
  relevant.

Documentation:

- Keep the read-boundary warning in
  `docs/architecture/streaming.md#transparent-sse` and
  `docs/architecture/testing.md#transport-integration` explicit.

Reference: `docs/architecture/streaming.md#transparent-sse`,
`docs/architecture/testing.md#transport-integration`.

Dependencies and out of scope: depends on `T028`. Semantic reconstruction across
split reads is parser work in `T038`.

### T033 - Cancel an active downstream SSE request

Goal: verify client cancellation still reaches upstream after streaming headers
and response bytes have begun.

Scope:

- Exercise cancellation while the SSE copy loop is active, not only while
  waiting for upstream headers.
- Ensure a downstream disconnect/cancel stops body copying and cancels the
  upstream request context promptly.
- Avoid goroutine or upstream connection leaks on the tested path.

Acceptance:

- After receiving the first SSE fragment, client cancellation is observed by the
  upstream handler without a long timeout.
- The downstream request terminates and the gateway handler does not remain
  blocked reading upstream.
- Existing pre-response cancellation coverage remains valid.

Tests:

- Add a real integration test: upstream flushes one fragment, client reads it,
  client cancels, upstream signals `request.Context().Done()`.
- Use bounded channels for each lifecycle stage and no unbounded goroutines.

Documentation:

- Extend `docs/architecture/testing.md#transport-integration` to distinguish
  cancellation before response headers from cancellation during an active
  stream.

Reference: `docs/architecture/transport.md#cancellation`,
`docs/architecture/testing.md#regressions`.

Dependencies and out of scope: depends on `T019`, `T020`, and `T028`. Concurrency
slots, reservations, and accounting cleanup do not exist yet and must not be
introduced here.

### T034 - Prove parallel streams are not serialized

Goal: close the transparent transport milestone with a behavioral concurrency
regression.

Scope:

- Start two SSE requests and prove both reach upstream and deliver first
  fragments before either response completes.
- Use the existing shared client/transport; change pool settings only if the
  test exposes a real serialization defect.
- Do not add a limiter, queue, semaphore, or request scheduler.

Acceptance:

- Both upstream handlers cross a shared arrival barrier.
- Both clients receive their first fragment before the completion barrier is
  released.
- The second stream never waits for EOF of the first in the unrestricted
  configuration.

Tests:

- Add a real HTTP integration test with two clients and channel/barrier
  synchronization rather than sleep-based overlap guesses.
- Ensure test cleanup releases all barriers even after an assertion failure.

Documentation:

- Add unrestricted stream parallelism to
  `docs/architecture/testing.md#regressions` if the current generic concurrency
  statement is not sufficient for reproducing the scenario.

Reference: `docs/architecture/transport.md#upstream-client`,
`docs/architecture/testing.md#regressions`.

Dependencies and out of scope: depends on `T028` and the reusable client from
`T010`. Per-key concurrency limits are a later policy milestone.

## Generic SSE Parser

### T035 - Define a protocol-neutral SSE parser contract

Goal: introduce the smallest bounded SSE framing API without connecting it to
the transparent transport hot path.

Scope:

- Add `internal/streaming` only now that real code belongs there.
- Define `SSEEvent` containing event name and data plus a parser/reader API that
  emits complete events sequentially.
- Accept an explicit maximum event size in construction; do not add YAML config
  yet.
- Define EOF behavior and ownership/lifetime of returned data.
- Keep the package independent of OpenAI and `internal/httpserver`.

Acceptance:

- Empty input returns EOF without fabricating an event.
- The public internal API can represent unnamed and named events.
- Dependencies follow the repository architecture and transport does not import
  the parser.

Tests:

- Add unit tests for empty input, constructor validation, default/empty event
  name, and sequential EOF behavior.

Documentation:

- Add package and exported-type Go documentation for protocol neutrality, event
  completion, data ownership, and size-limit units.
- Keep `docs/architecture/streaming.md#sse-framing` aligned with the chosen API
  semantics without documenting implementation details.

Reference: `docs/architecture/streaming.md#sse-framing`,
`docs/architecture/repository.md#dependencies`.

Dependencies and out of scope: follows the completed transparent stream
milestone through `T034`. OpenAI chunk schemas, passive observation, transport
integration, and aggregation are excluded.

### T036 - Parse SSE data lines and event names

Goal: parse the core SSE framing needed to produce one complete generic event.

Scope:

- Recognize `data:` and optional `event:` fields.
- End an event on a blank line and support LF and CRLF input.
- Join multiple `data:` lines in one event with a newline according to SSE
  semantics.
- Support an empty data value and ignore unknown fields rather than treating
  them as data.

Acceptance:

- Unnamed and named events parse correctly.
- Multi-line data is joined predictably without JSON interpretation.
- A completed event followed by EOF is returned once, then EOF.

Tests:

- Add table-driven tests for one data line, event plus data, multiple data lines,
  empty data, unknown fields, LF, and CRLF.
- Include multiple sequential events read through repeated parser calls.

Documentation:

- Document data-line joining, blank-line termination, and unknown-field behavior
  in Go docs or `docs/architecture/streaming.md#sse-framing` where it is a stable
  contract.

Reference: `docs/architecture/streaming.md#sse-framing`.

Dependencies and out of scope: depends on `T035`. Comments, arbitrary chunk
boundaries, size-limit enforcement, and OpenAI semantics are separate tasks.

### T037 - Handle SSE comments and field syntax safely

Goal: support heartbeat comments and valid field formatting without assigning
them OpenAI or transport meaning.

Scope:

- Ignore lines beginning with `:` when building parsed events.
- Handle the optional single space after a field colon (`data:x` and `data: x`).
- Keep comments between data lines from terminating or altering the event.
- Return `data: [DONE]` as ordinary data with no semantic completion behavior.

Acceptance:

- Comment-only input produces no event.
- Heartbeats around or inside an event do not contaminate event data.
- Unknown fields remain harmless and `[DONE]` is returned unchanged as data.

Tests:

- Add unit cases for empty comments, heartbeat-only input, comments between data
  lines, colon spacing variants, unknown fields, and `[DONE]`.

Documentation:

- Explicitly distinguish parser behavior from transport behavior in
  `docs/architecture/streaming.md`: transparent transport forwards comments,
  while the generic framing parser ignores them semantically.

Reference: `docs/architecture/streaming.md#transparent-sse`,
`docs/architecture/streaming.md#sse-framing`.

Dependencies and out of scope: depends on `T036`. Retry fields, browser
reconnection state, OpenAI `[DONE]` observation, and heartbeats generated by the
gateway are excluded.

### T038 - Parse split and coalesced input reads

Goal: make generic SSE parsing independent of the underlying `io.Reader` chunk
boundaries.

Scope:

- Retain partial lines and partial events across reads.
- Emit several events in order when one read contains several complete events.
- Correctly handle splits inside field names, data, blank delimiters, and CRLF.
- Avoid exposing reader chunk boundaries in parser results.

Acceptance:

- `da` plus `ta: {...}\n\n` becomes one event.
- Three coalesced events are returned separately and in order.
- A CRLF pair split across reads is handled correctly.

Tests:

- Implement a deterministic custom reader that returns prescribed chunks.
- Cover splits in field name, value, LF/CRLF terminator, event separator, and
  multiple coalesced events.

Documentation:

- Keep the no-read-boundary guarantee explicit in the parser Go docs and
  `docs/architecture/streaming.md#sse-framing`.

Reference: `docs/architecture/streaming.md#sse-framing`,
`docs/architecture/testing.md#transport-integration`.

Dependencies and out of scope: depends on `T036`-`T037`. This parser still must
not be attached to the transparent stream path.

### T039 - Bound SSE event memory

Goal: prevent an unterminated or oversized SSE event from growing parser memory
without limit.

Scope:

- Enforce the constructor's explicit maximum against accumulated event data and
  framing according to one documented definition.
- Return a recognizable controlled error for an oversized event.
- Stop accumulating promptly after the limit is crossed and never panic.
- Define whether the parser is reusable after this terminal parse error; prefer
  a simple terminal-error contract.

Acceptance:

- Events below and exactly at the documented limit succeed.
- An event above the limit, including an unterminated line, fails predictably.
- Multiple data lines cannot bypass the cumulative limit.

Tests:

- Add unit cases below, at, and above the boundary, plus oversized unterminated
  input and cumulative multi-line data.
- Use a small test limit to exercise behavior without brittle memory benchmarks.

Documentation:

- Document exactly what bytes count toward the limit, the error contract, and
  parser state after failure in Go docs and
  `docs/architecture/streaming.md#sse-framing`.

Reference: `docs/architecture/streaming.md#sse-framing`.

Dependencies and out of scope: depends on `T035`-`T038`. No configuration file
setting, HTTP error mapping, stream termination policy, or observer integration
is added.

### T040 - Add a reusable streaming test upstream

Goal: consolidate the proven streaming scenarios into a small test-only harness
for subsequent observer and aggregation milestones.

Scope:

- Extract only duplicated test setup needed to script upstream writes, flushes,
  barriers, EOF, and cancellation observation.
- Keep the harness in `_test.go` code or an internal test-only package; do not
  add a production mock endpoint or production configuration.
- Preserve the existing behavioral assertions rather than replacing them with
  interface-only mocks.
- Demonstrate scenarios for normal JSON, SSE with `[DONE]`, SSE without
  `[DONE]`, split/coalesced writes, hanging stream, cancellation, and concurrent
  requests where already implemented by `T021`-`T039`.

Acceptance:

- Existing transport regressions use the harness where it genuinely removes
  duplication and still run through real HTTP servers.
- The harness exposes deterministic channels/barriers and always supports safe
  cleanup after test failure.
- Production binary and package APIs do not grow solely for tests.

Tests:

- Run the complete suite and retain direct assertions for incremental delivery,
  EOF close delay, cancellation, byte identity, and parallelism.
- Add a focused harness self-test only if its synchronization logic is
  non-trivial and not already exercised by transport tests.

Documentation:

- Expand `docs/architecture/testing.md#transport-integration` with the harness
  location, supported scripted behaviors, and the rule that real HTTP remains
  mandatory.
- Do not add user-facing deployment documentation for this test-only component.

Reference: `docs/architecture/testing.md#transport-integration`,
`docs/architecture/testing.md#regressions`.

Dependencies and out of scope: depends on `T021`-`T039`. Do not implement
SSE-to-JSON aggregation, OpenAI stream observation, authentication, SQLite,
limits, accounting, a Web UI, or a runnable mock service.
