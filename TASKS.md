# Active Tasks

This file contains the next twenty atomic tasks. An implementation agent reads
only `AGENTS.md`, `CURRENT.md`, its assigned task, and the architecture documents
linked by that task. Tasks are ordered by dependency and must not be implemented
out of order. Do not load `docs/tasks/backlog-full.md` during routine work.

Every implementation task must finish with `go fmt ./...`, `go test ./...`, and
`go build ./...`. Add behavioral tests in the same task as changed HTTP or
streaming behavior. Update `CURRENT.md` and commit after completing one task.

## Minimal OpenAI Request Inspection

### T041 - Define minimal request metadata

Goal: add the smallest OpenAI request metadata model without defining a complete
request schema.

Scope:

- Create `internal/protocol/openai` now that protocol-specific code belongs there.
- Define metadata for model, explicit stream state, and the two known output-token
  limits: `max_tokens` and `max_completion_tokens`.
- Represent stream absence separately from explicit `false`; pointers or a small
  tri-state type are acceptable.
- Keep the type independent of HTTP, transport, config, storage, and policy.

Acceptance and tests:

- Unit tests can construct metadata for absent, false, and true stream states.
- Zero, absent, and positive token limits remain distinguishable.
- Package and exported API documentation explains that this is partial metadata.

Reference: `docs/architecture/repository.md#initial-structure`,
`docs/architecture/repository.md#dependencies`.

Out of scope: JSON parsing, full messages, tools, provider schemas, validation,
body buffering, authentication, and limits.

### T042 - Parse request metadata from JSON bytes

Goal: extract the T041 fields from a bounded byte slice while tolerating unknown
OpenAI-compatible request fields.

Scope:

- Add one parser in `internal/protocol/openai` for `model`, `stream`,
  `max_tokens`, and `max_completion_tokens`.
- Use a narrow decode shape; do not define messages, tools, reasoning, or other
  payload fields.
- Return a controlled error for malformed JSON or wrong types in inspected fields.
- Do not reserialize or mutate the input bytes.

Acceptance and tests:

- Table tests cover model, stream true/false/absent, both token-limit fields,
  unknown nested fields, whitespace, malformed JSON, and wrong known-field types.
- Input bytes are unchanged after every parse attempt.

Reference: `docs/architecture/transport.md#request-body`,
`docs/architecture/repository.md#principles`.

Dependencies and out of scope: depends on T041. Do not read an `io.Reader`, choose
between the two token fields, or attach parsing to HTTP.

### T043 - Add bounded request-body inspection

Goal: inspect a known request body without unbounded buffering and reconstruct an
identical body for upstream forwarding.

Scope:

- Add a small helper at the HTTP/protocol boundary that reads at most an explicit
  positive byte limit plus one detection byte.
- If the entire body fits, return its bytes for metadata parsing and a replacement
  reader containing the exact original bytes.
- If it exceeds the limit, report inspection unavailable and reconstruct the full
  stream from the consumed prefix plus unread remainder without dropping bytes.
- Never close the incoming body inside the helper and never read a generic body to
  discover its total size.

Acceptance and tests:

- Exact-limit, below-limit, over-limit, empty, fragmented-reader, and read-error
  cases are deterministic and bounded.
- Reading the replacement yields byte-identical input for both inspected and
  over-limit bodies.
- An over-limit reader is not drained or fully buffered.

Reference: `docs/architecture/transport.md#request-body`,
`docs/architecture/testing.md#security-and-performance`.

Dependencies and out of scope: depends on T042. No YAML option, HTTP status,
tokenizer, logging capture, or generic endpoint integration.

### T044 - Inspect chat-completions requests in the proxy

Goal: make explicit request stream state available to response dispatch while
preserving transparent upstream request bytes.

Scope:

- Apply T043 only to `POST /v1/chat/completions` with a JSON media type; all other
  methods and `/v1/*` paths retain direct streaming behavior.
- Use one private constant for the temporary inspection bound; configuration is a
  later task.
- Parse metadata when the complete body fits. Malformed, over-limit, or unreadable
  metadata means unknown metadata and must not rewrite the payload.
- Preserve original `ContentLength`, request headers, bytes, cancellation, and
  configured upstream authority.
- Keep metadata local to the request; do not add global state or logs.

Acceptance and tests:

- Real HTTP tests prove byte identity for valid, malformed, and over-limit chat
  bodies and for an unknown `/v1/*` streaming body.
- Explicit stream false, true, and absent reach the response-dispatch code as
  distinct states using package-private instrumentation in tests.
- Existing cancellation and unknown-endpoint tests remain valid.

Reference: `docs/architecture/transport.md#generic-passthrough`,
`docs/architecture/transport.md#request-body`.

Dependencies and out of scope: depends on T043. Do not change response behavior,
reject malformed JSON, expose metadata downstream, or implement policy.

### T045 - Harden inspection media-type and body edge cases

Goal: close request-inspection ambiguity before metadata affects compatibility
behavior.

Scope:

- Classify request JSON with parsed media types: `application/json` and valid
  `+json` suffixes, including parameters and mixed case.
- Treat missing, repeated, or malformed request `Content-Type` as non-inspectable.
- Preserve a known empty body, unknown-length body, and read errors according to
  existing transparent proxy behavior.
- Ensure inspection never changes transfer encoding or invents a length.

Acceptance and tests:

- Table tests cover request media types and repeated headers.
- Real HTTP tests assert upstream bytes, `ContentLength`, and transfer encoding for
  known and unknown lengths after successful and skipped inspection.
- No request field controls actual upstream response classification.

Reference: `docs/architecture/transport.md#request-body`,
`docs/architecture/transport.md#headers`,
`docs/architecture/transport.md#response-classification`.

Dependencies and out of scope: depends on T044. Do not add request rejection,
body-size policy, or response aggregation.

## OpenAI Stream Observation

### T046 - Define an OpenAI stream observer

Goal: introduce a protocol-specific observer that consumes complete generic
`streaming.SSEEvent` values without controlling transport.

Scope:

- Put OpenAI-specific code in `internal/protocol/openai`; it may import the generic
  `internal/streaming` package, never the reverse.
- Define observer state and an `Observe(SSEEvent)`-style API.
- Parse JSON data into a narrow internal chunk shape and return a recognizable
  parse error without making the observer terminal.
- Do not connect the observer to transparent SSE transport yet.

Acceptance and tests:

- Valid JSON is accepted; malformed JSON returns a controlled error; a later valid
  event is still accepted.
- Event names and unknown JSON fields are harmless.
- Dependency direction and transport isolation are preserved.

Reference: `docs/architecture/repository.md#dependencies`,
`docs/architecture/streaming.md#sse-framing`.

Dependencies and out of scope: depends on T035-T040. No `[DONE]` semantics,
aggregation, HTTP integration, goroutines, telemetry, or complete OpenAI schema.

### T047 - Observe response identity metadata

Goal: retain response ID, model, and created timestamp from OpenAI stream chunks.

Scope:

- Extend T046's narrow chunk shape only with `id`, `model`, and `created`.
- Record the first non-empty ID/model and first present created value.
- Define deterministic behavior for conflicting later values; keep the first and
  expose no transport error.
- Provide a read-only metadata snapshot so callers cannot mutate observer state.

Acceptance and tests:

- Metadata can arrive together or across chunks and remains stable afterward.
- Missing fields and conflicting later values are covered.
- Malformed chunks do not erase previously observed metadata.

Reference: `docs/architecture/streaming.md#sse-framing`,
`docs/architecture/repository.md#principles`.

Dependencies and out of scope: depends on T046. Do not observe choices, usage,
`[DONE]`, or attach to HTTP.

### T048 - Observe choice and tool-call deltas

Goal: expose choice index, message/tool-call deltas, and finish reason as
observations without assigning them transport lifetime semantics.

Scope:

- Parse `choices[]` minimally: `index`, `delta.role`, `delta.content`, indexed
  `delta.tool_calls` (`index`, `id`, `type`, `function.name`, and
  `function.arguments`), and `finish_reason` with absence distinct from an empty
  value.
- Return observations in upstream array order and preserve choice indexes.
- Store the last non-nil finish reason per choice in the observer snapshot.
- Preserve function-argument fragments as strings; do not JSON-decode them.
- Never interpret finish reason as completion or reject unknown delta fields.

Acceptance and tests:

- Cover multiple choices, missing/empty content, role-only chunks, indexed tool
  calls, split function arguments, null/non-null finish reasons, unknown fields,
  and repeated updates.
- A finish reason does not mark the observer done.

Reference: `docs/architecture/streaming.md#termination`,
`docs/architecture/streaming.md#sse-to-json`.

Dependencies and out of scope: depends on T047. Usage, aggregation, validation of
assembled function arguments, and transport integration remain excluded.

### T049 - Observe normalized token usage

Goal: extract OpenAI and input/output naming variants into one usage snapshot.

Scope:

- Support `prompt_tokens`, `completion_tokens`, `input_tokens`, `output_tokens`,
  and `total_tokens` when present.
- Normalize prompt/input and completion/output counts without inventing missing
  totals or deriving prices.
- The latest explicitly present value wins; absence in a later chunk does not
  erase an earlier value.
- Reject negative or non-integer known usage values as observation errors while
  keeping the observer reusable.

Acceptance and tests:

- Table tests cover both naming families, partial usage, usage-only terminal
  chunks, later replacement, absent totals, invalid values, and prior-state
  preservation after error.

Reference: `docs/architecture/streaming.md#sse-framing`,
`docs/architecture/accounting.md`.

Dependencies and out of scope: depends on T046. No cost calculation, persistence,
token estimation, aggregation, or HTTP attachment.

### T050 - Observe the DONE sentinel

Goal: recognize the exact OpenAI `[DONE]` sentinel as observer metadata only.

Scope:

- Treat an event whose data is exactly `[DONE]` as observed completion metadata.
- Do not JSON-decode the sentinel and do not treat whitespace variants as exact
  unless the generic parser produced those exact bytes.
- Repeated `[DONE]` is idempotent; events after it may still be observed.
- Keep transparent transport and generic parser behavior unchanged.

Acceptance and tests:

- Exact, repeated, whitespace-variant, JSON-string, and post-DONE events are
  covered.
- `[DONE]` never causes an EOF, timer, cancellation, or synthetic event.

Reference: `docs/architecture/streaming.md#termination`,
`docs/architecture/streaming.md#sse-framing`.

Dependencies and out of scope: depends on T046-T049. No transport wiring or
aggregation completion policy.

### T051 - Make observation failure non-fatal by contract

Goal: provide a best-effort event-observation driver whose parse failures do not
stop later events or raw-byte delivery by its caller.

Scope:

- Add a small protocol-layer driver that repeatedly reads complete generic events
  and invokes the observer, reporting observation errors through a supplied
  callback or result collection.
- A malformed JSON event must not stop subsequent valid events; framing and size
  errors from the bounded generic reader remain terminal for observation only.
- Do not attach this pull-based driver to the transparent transport hot path,
  because reading complete events before writing would delay flushes.
- Document that any future live attachment must observe copied bytes only after
  downstream write and flush, and may drop/disable observation rather than block.

Acceptance and tests:

- A sequence valid/malformed/valid updates state from both valid events and
  reports one error.
- A generic parser size error ends observation predictably without panic.
- A source copy used by the test remains byte-identical regardless of observer
  errors.

Reference: `docs/architecture/streaming.md#transparent-sse`,
`docs/architecture/streaming.md#sse-framing`.

Dependencies and out of scope: depends on T046-T050. No production goroutine,
channel, transport hook, blocking telemetry, or accounting.

## SSE To JSON Compatibility

### T052 - Define the SSE aggregation mismatch predicate

Goal: select aggregation only when a known OpenAI client explicitly requested
non-stream output but the actual upstream response is SSE.

Scope:

- Add one pure predicate using T041 metadata and the actual T026 response mode.
- Return true only for explicit `stream:false` plus `ResponseModeSSE` on the
  inspected chat-completions path.
- Stream true, absent/unknown metadata, malformed/over-limit requests, JSON, and
  opaque responses remain transparent.
- Keep classification based on actual upstream headers.

Acceptance and tests:

- Exhaustively table-test stream tri-state against JSON/SSE/opaque and eligible
  versus unknown endpoints.
- No HTTP behavior changes in this task.

Reference: `docs/architecture/transport.md#response-classification`,
`docs/architecture/streaming.md#sse-to-json`.

Dependencies and out of scope: depends on T045. No accumulator, body buffering,
header rewriting, or observer attachment.

### T053 - Define a bounded chat accumulator

Goal: create a bounded in-memory model for one OpenAI chat-completion result.

Scope:

- Add an accumulator in `internal/protocol/openai` for response ID, model,
  created, choices keyed by upstream index, messages, tool calls, finish reasons,
  and normalized usage.
- Accept parsed observer results rather than raw HTTP or SSE bytes.
- Preserve deterministic upstream choice order without requiring contiguous
  indexes.
- Add an explicit positive maximum accumulated payload size covering content and
  function-argument fragments, with a terminal overflow error.

Acceptance and tests:

- Constructor validation, empty state, identity metadata, multiple/non-contiguous
  choice indexes, stable order, exact-limit payload, overflow, and terminal-error
  state are covered.

Reference: `docs/architecture/streaming.md#sse-to-json`,
`docs/architecture/repository.md#dependencies`.

Dependencies and out of scope: depends on T047-T049. No HTTP, JSON rendering,
pricing, config, or function-argument JSON decoding.

### T054 - Accumulate message deltas and finish reasons

Goal: reconstruct messages for all choices and retain their finish reasons.

Scope:

- Set each choice's role from its first non-empty role delta and keep it stable on
  later conflicting role values.
- Append content exactly in chunk order, including empty strings and UTF-8 data,
  while enforcing T053's byte bound.
- Preserve upstream choice indexes and first-observed choice order.
- Keep the latest non-nil finish reason per choice; finish reason must not mark
  aggregation complete because usage may arrive afterward.
- Do not decode content, normalize whitespace, or interpret reasoning fields.

Acceptance and tests:

- `TEST` plus `_OK` yields `TEST_OK`; split Unicode content, role-only chunks,
  empty deltas, conflicts, multiple indexes, finish-reason replacement,
  deltas after finish reason, exact byte bounds, and overflow are covered.

Reference: `docs/architecture/streaming.md#sse-to-json`.

Dependencies and out of scope: depends on T048 and T053. Usage, tools, and
rendering are excluded.

### T055 - Accumulate indexed tool-call deltas

Goal: reconstruct OpenAI tool calls whose metadata and arguments are split across
SSE chunks.

Scope:

- Identify tool calls by choice index and tool-call index and preserve their
  first-observed order.
- Keep the first non-empty ID, type, and function name stable on later conflicts.
- Concatenate `function.arguments` fragments exactly in event order while
  enforcing T053's shared payload bound.
- Do not JSON-decode, validate, normalize, or otherwise rewrite assembled
  arguments.

Acceptance and tests:

- Cover one and multiple choices, multiple tool calls, non-contiguous indexes,
  metadata split across chunks, conflicting metadata, fragmented JSON arguments,
  exact byte bounds, and overflow.

Reference: `docs/architecture/streaming.md#sse-to-json`.

Dependencies and out of scope: depends on T048 and T053-T054. No `[DONE]`, EOF
policy, HTTP integration, or execution/validation of tool calls.

### T056 - Accumulate the latest observed usage

Goal: include the latest explicitly observed normalized usage in the final result.

Scope:

- Copy usage values into accumulator-owned state; callers cannot mutate them.
- Merge partial later usage without erasing fields that are absent.
- Accept usage-only events after content and finish reason.
- Preserve absence rather than serializing invented zero values.

Acceptance and tests:

- OpenAI and input/output naming variants normalized by T049 produce the same
  accumulator state.
- Partial updates, replacements, usage after finish, and no-usage streams are
  covered.

Reference: `docs/architecture/streaming.md#sse-to-json`,
`docs/architecture/accounting.md`.

Dependencies and out of scope: depends on T049 and T053-T055. No price lookup,
derivation, persistence, token estimation, or HTTP.

### T057 - Render OpenAI-compatible chat JSON

Goal: render accumulator state as one non-stream OpenAI chat-completion response.

Scope:

- Produce `id`, `object:"chat.completion"`, `created`, `model`, all observed
  choices with `index`, `message.role`, `message.content`, optional indexed
  `message.tool_calls`, and `finish_reason`, plus usage when observed.
- Use standard JSON encoding and return bytes with no dependency on HTTP.
- Preserve optional/absent usage fields and valid JSON escaping.
- Return a controlled error if no meaningful message, tool-call, or finish
  metadata was accumulated.

Acceptance and tests:

- Compare decoded JSON structure for multiple choices, content, role, finish
  reason, identity, fragmented tool-call arguments, Unicode/escaping, usage
  present/absent, and empty-invalid state.
- Output is valid JSON and contains no streaming-only `delta` field.

Reference: `docs/architecture/streaming.md#sse-to-json`.

Dependencies and out of scope: depends on T053-T056. No HTTP headers, function
argument validation, tool execution, or pretty printing.

### T058 - Aggregate a bounded SSE stream through DONE

Goal: combine the generic reader, observer, accumulator, and renderer into a
bounded conversion function that completes on exact `[DONE]`.

Scope:

- Accept an `io.Reader` plus explicit event-size and accumulated-content limits.
- Process complete events sequentially; valid chunks update the accumulator.
- Exact `[DONE]` completes input semantics, but drain no bytes after it in this
  conversion-only path and return rendered JSON.
- Return controlled errors for malformed required chunks, oversized events,
  accumulated overflow, empty streams, and invalid final state.

Acceptance and tests:

- Realistic role/content/finish/usage/`[DONE]` and split-tool-call sequences render
  valid JSON, including multiple choices.
- Split/coalesced reads, malformed JSON, comments, unknown fields, bounds, empty
  input, and bytes after `[DONE]` are covered deterministically.

Reference: `docs/architecture/streaming.md#sse-framing`,
`docs/architecture/streaming.md#sse-to-json`.

Dependencies and out of scope: depends on T050 and T057. No HTTP integration,
transparent passthrough changes, function-argument validation, or tool execution.

### T059 - Complete aggregation on clean upstream EOF

Goal: support 9router streams that end physically after terminal chunks without
sending `[DONE]`.

Scope:

- Treat clean EOF after at least one meaningful valid OpenAI event as a conversion
  completion signal and render accumulated JSON.
- EOF before meaningful response data is a recognizable upstream/protocol error.
- Do not require finish reason and do not add grace, idle, or completion timers.
- Framing errors and truncated unterminated events remain errors according to the
  generic parser's complete-event contract.

Acceptance and tests:

- Terminal chunks plus EOF without `[DONE]` render the same result as the DONE
  case.
- Empty EOF, comment-only EOF, truncated event, usage-after-finish then EOF, and
  immediate close timing are covered without sleeps.

Reference: `docs/architecture/streaming.md#termination`,
`docs/architecture/streaming.md#sse-to-json`,
`docs/architecture/testing.md#regressions`.

Dependencies and out of scope: depends on T058. No synthetic `[DONE]`, timeout,
HTTP response behavior, or acceptance of incomplete SSE framing.

### T060 - Integrate SSE-to-JSON compatibility and regression

Goal: for an explicitly non-stream chat-completions request whose upstream
actually returns SSE, return one valid OpenAI-compatible JSON response instead of
raw SSE or an unmarshal failure.

Scope:

- Use T052 before committing downstream headers to choose transparent SSE or the
  bounded T058-T059 aggregation path.
- Aggregate the upstream body before writing downstream status/body. On success,
  preserve upstream status and safe headers except remove streaming/length headers,
  set `Content-Type: application/json`, and set the correct `Content-Length`.
- On aggregation failure before downstream bytes, return a controlled 502 without
  leaking upstream credentials, raw body, or parse details.
- Stream true/absent/unknown, malformed or over-limit requests, non-chat endpoints,
  JSON, and opaque responses retain existing transparent behavior.
- Client cancellation must cancel aggregation and upstream work promptly.

Acceptance and tests:

- Real upstream/gateway tests cover `stream:false` plus SSE with `[DONE]`, clean EOF
  without `[DONE]`, split/coalesced events, multiple choices, split tool calls,
  usage, non-200 success conversion, malformed SSE failure, and cancellation.
- Add a named regression proving the client receives valid JSON and never
  `failed to unmarshal` for the historical Bifrost mismatch.
- Existing byte-transparent SSE, first-fragment flush, EOF-close, parallelism,
  gzip, JSON, opaque, and unknown-endpoint regressions remain unchanged.

Reference: `docs/architecture/transport.md#response-classification`,
`docs/architecture/streaming.md#sse-to-json`,
`docs/architecture/testing.md#transport-integration`.

Dependencies and out of scope: depends on T045 and T052-T059. Do not add tool-call
execution or argument validation, authentication, SQLite, policy, accounting,
telemetry persistence, retries, or response buffering in transparent modes.
